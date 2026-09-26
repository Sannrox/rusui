package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

type reviewRequestResponse struct {
	SessionID       int64 `json:"session_id"`
	TurnID          int64 `json:"turn_id"`
	PendingRevision int   `json:"pending_revision"`
}

type reviewTurnStatus struct {
	TurnID          int64  `json:"turn_id"`
	TurnState       string `json:"turn_state"`
	PendingRevision int    `json:"pending_revision"`
	ClaimedRevision int    `json:"claimed_revision"`
}

type reviewSessionTurn struct {
	ID              int64
	State           string
	PendingRevision int
	ClaimedRevision int
}

type reviewResultResponse struct {
	RevisionID    int64            `json:"revision_id"`
	Artifact      json.RawMessage  `json:"artifact"`
	DryRunActions []map[string]any `json:"dry_run_actions"`
}

type reviewSessionResponse struct {
	reviewTurnStatus
	Turns        []reviewSessionTurn   `json:"turns"`
	ReviewResult *reviewResultResponse `json:"review_result"`
}

func reviewCLI(args []string) {
	fs := flag.NewFlagSet("review", flag.ExitOnError)
	url := fs.String("url", "http://127.0.0.1:8080", "plane URL")
	token := fs.String("token", os.Getenv("RUSUI_WORKER_SECRET"), "operator/worker token")
	timeout := fs.Duration("timeout", 30*time.Minute, "maximum wait for the review result; 0 waits indefinitely")
	pollInterval := fs.Duration("poll-interval", time.Second, "result polling interval")
	_ = fs.Parse(args)
	if fs.NArg() != 1 || *timeout < 0 || *pollInterval <= 0 {
		fmt.Fprintln(os.Stderr, "usage: rusui review [-url URL] [-token TOKEN] [-timeout DURATION] OWNER/REPO#PR")
		os.Exit(2)
	}
	repo, item, err := parseReviewTarget(fs.Arg(0))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	client, err := planeHTTP(*url)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	clientCopy := *client
	clientCopy.Timeout = 90 * time.Second
	client = &clientCopy

	body, _ := json.Marshal(map[string]any{"repo": repo, "item": item})
	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(*url, "/")+"/reviews", bytes.NewReader(body))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	req.Header.Set("Content-Type", "application/json")
	if *token != "" {
		req.Header.Set("Authorization", "Bearer "+*token)
	}
	res, err := client.Do(req)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	var started reviewRequestResponse
	if err := decodeReviewResponse(res, &started); err != nil {
		fmt.Fprintf(os.Stderr, "review: %v\n", err)
		os.Exit(1)
	}
	if started.SessionID == 0 || started.TurnID == 0 || started.PendingRevision == 0 {
		fmt.Fprintln(os.Stderr, "review: server returned an incomplete session")
		os.Exit(1)
	}
	baseURL := strings.TrimRight(*url, "/")
	waitingRevision := started.PendingRevision
	fmt.Printf("Review session %d for %s#%d; waiting for revision %d\n", started.SessionID, repo, item, waitingRevision)

	var deadline time.Time
	if *timeout > 0 {
		deadline = time.Now().Add(*timeout)
	}
	for {
		session, err := fetchReviewSession(client, baseURL, *token, started, true)
		if err != nil {
			fmt.Fprintf(os.Stderr, "review: %v\n", err)
			os.Exit(1)
		}
		var turnState string
		var pendingRevision, claimedRevision int
		turn, found := reviewTurnFor(session, started.TurnID)
		if found {
			turnState = turn.TurnState
			pendingRevision = turn.PendingRevision
			claimedRevision = turn.ClaimedRevision
		}
		if pendingRevision > waitingRevision {
			waitingRevision = pendingRevision
			fmt.Printf("Review session %d advanced; waiting for revision %d\n", started.SessionID, waitingRevision)
		}
		if session.ReviewResult == nil && found && turnState == "completed" && pendingRevision >= started.PendingRevision && claimedRevision >= pendingRevision {
			session, err = fetchReviewSession(client, baseURL, *token, started, false)
			if err != nil {
				fmt.Fprintf(os.Stderr, "review: %v\n", err)
				os.Exit(1)
			}
			turn, found = reviewTurnFor(session, started.TurnID)
			if !found {
				fmt.Fprintf(os.Stderr, "review: session %d no longer contains turn %d\n", started.SessionID, started.TurnID)
				os.Exit(1)
			}
			turnState = turn.TurnState
			pendingRevision = turn.PendingRevision
			if pendingRevision > waitingRevision {
				waitingRevision = pendingRevision
				fmt.Printf("Review session %d advanced; waiting for revision %d\n", started.SessionID, waitingRevision)
			}
		}
		if reviewResultIsCurrent(session.ReviewResult, started.PendingRevision, pendingRevision) {
			out, err := json.MarshalIndent(session.ReviewResult, "", "  ")
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			fmt.Println(string(out))
			return
		}
		if turnState == "failed" {
			fmt.Fprintf(os.Stderr, "review: session %d failed at revision %d\n", started.SessionID, waitingRevision)
			os.Exit(1)
		}
		if !deadline.IsZero() && time.Now().After(deadline) {
			fmt.Fprintf(os.Stderr, "review: timed out waiting for session %d revision %d\n", started.SessionID, waitingRevision)
			os.Exit(1)
		}
		time.Sleep(*pollInterval)
	}
}

func fetchReviewSession(client *http.Client, baseURL, token string, started reviewRequestResponse, reviewStatus bool) (reviewSessionResponse, error) {
	sessionURL := baseURL + "/sessions/" + strconv.FormatInt(started.SessionID, 10)
	if reviewStatus {
		sessionURL += "?view=review-status&turn_id=" + strconv.FormatInt(started.TurnID, 10)
	}
	getReq, err := http.NewRequest(http.MethodGet, sessionURL, nil)
	if err != nil {
		return reviewSessionResponse{}, err
	}
	if token != "" {
		getReq.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := client.Do(getReq)
	if err != nil {
		return reviewSessionResponse{}, err
	}
	var session reviewSessionResponse
	if err := decodeReviewResponse(res, &session); err != nil {
		return reviewSessionResponse{}, err
	}
	return session, nil
}

func reviewTurnFor(session reviewSessionResponse, turnID int64) (reviewTurnStatus, bool) {
	if session.TurnID == turnID {
		return session.reviewTurnStatus, true
	}
	for _, turn := range session.Turns {
		if turn.ID == turnID {
			return reviewTurnStatus{
				TurnID: turn.ID, TurnState: turn.State,
				PendingRevision: turn.PendingRevision, ClaimedRevision: turn.ClaimedRevision,
			}, true
		}
	}
	return reviewTurnStatus{}, false
}

func reviewResultIsCurrent(result *reviewResultResponse, firstRevision, pendingRevision int) bool {
	if result == nil || pendingRevision < firstRevision {
		return false
	}
	var artifact struct {
		ClaimedRevision int `json:"claimed_revision"`
	}
	return json.Unmarshal(result.Artifact, &artifact) == nil && artifact.ClaimedRevision >= pendingRevision
}

func parseReviewTarget(raw string) (string, int, error) {
	repo, number, ok := strings.Cut(raw, "#")
	if !ok || strings.Count(raw, "#") != 1 || strings.Count(repo, "/") != 1 || strings.TrimSpace(repo) != repo {
		return "", 0, fmt.Errorf("review target must be OWNER/REPO#PR")
	}
	owner, name, _ := strings.Cut(repo, "/")
	if owner == "" || name == "" || strings.TrimSpace(number) != number {
		return "", 0, fmt.Errorf("review target must be OWNER/REPO#PR")
	}
	item, err := strconv.Atoi(number)
	if err != nil || item <= 0 {
		return "", 0, fmt.Errorf("pull request number must be a positive integer")
	}
	return repo, item, nil
}

func decodeReviewResponse(res *http.Response, dst any) error {
	body, err := io.ReadAll(res.Body)
	closeErr := res.Body.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if res.StatusCode >= 300 {
		return fmt.Errorf("%s %s", res.Status, strings.TrimSpace(string(body)))
	}
	if err := json.Unmarshal(body, dst); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}
