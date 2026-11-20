package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/sannrox/rusui/internal/server"
)

type pathList []string

func (p *pathList) String() string { return strings.Join(*p, ",") }

func (p *pathList) Set(value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("empty path")
	}
	*p = append(*p, value)
	return nil
}

func loadPromptAttachments(paths []string) ([]map[string]string, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	out := make([]map[string]string, 0, len(paths))
	for _, path := range paths {
		body, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		out = append(out, map[string]string{
			"name":    filepath.Base(path),
			"content": base64.StdEncoding.EncodeToString(body),
		})
	}
	return out, nil
}

func runCLI(args []string) {
	os.Exit(runMain(args, os.Stdin, os.Stdout, os.Stderr, isTerminal(os.Stdin), isTerminal(os.Stderr)))
}

func runMain(args []string, stdin io.Reader, stdout, stderr io.Writer, stdinTTY, stderrTTY bool) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	url := fs.String("url", "http://127.0.0.1:8080", "plane URL")
	project := fs.String("project", "", "project slug")
	guest := fs.String("guest", "", "project-allowed guest (default from policy)")
	token := fs.String("token", os.Getenv("RUSUI_WORKER_SECRET"), "operator/worker token")
	idem := fs.String("idempotency-key", "", "idempotency key")
	effort := fs.String("effort", "", "stable effort key for a pinned implementation task")
	size := fs.String("size", "", "environment size: small, medium, or large")
	mode := fs.String("mode", "", "guest session mode: low, medium, high, or ultra")
	repo := fs.String("repo", "", "source repository")
	ref := fs.String("ref", "", "source ref")
	base := fs.String("base-sha", "", "pinned base commit")
	paths := fs.String("paths", "", "comma-separated allowed paths")
	follow := fs.Bool("follow", false, "print the create result, then follow until the session ends")
	var files pathList
	fs.Var(&files, "file", "attach a file to the first turn (repeatable)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	prompt := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if prompt == "" && !stdinTTY {
		b, err := io.ReadAll(stdin)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, err)
			return 1
		}
		prompt = strings.TrimSpace(string(b))
	}
	if *project == "" || prompt == "" {
		_, _ = fmt.Fprintln(stderr, "usage: rusui run -project SLUG [-url URL] [-token TOKEN] [-guest NAME] [-file PATH] [-follow] [-idempotency-key KEY] [-effort KEY -repo REPO -ref REF -base-sha SHA -paths PATHS] [PROMPT]")
		return 2
	}
	payload := map[string]any{"kind": "run", "prompt": prompt}
	if *guest != "" {
		payload["guest"] = *guest
	}
	if *size != "" {
		payload["size"] = *size
	}
	if *mode != "" {
		payload["mode"] = *mode
	}
	if *effort != "" || *repo != "" || *ref != "" || *base != "" || *paths != "" {
		payload["effort_key"] = *effort
		payload["repo"] = *repo
		payload["ref"] = *ref
		payload["base_sha"] = *base
		if *paths != "" {
			payload["allowed_paths"] = strings.Split(*paths, ",")
		}
	}
	if len(files) > 0 {
		atts, err := loadPromptAttachments(files)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, err)
			return 1
		}
		payload["attachments"] = atts
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(*url, "/")+"/projects/"+*project+"/sessions", bytes.NewReader(body))
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	req.Header.Set("Content-Type", "application/json")
	if *token != "" {
		req.Header.Set("Authorization", "Bearer "+*token)
	}
	if *idem != "" {
		req.Header.Set("Idempotency-Key", *idem)
	}
	client, err := planeHTTP(*url)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	res, err := client.Do(req)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 300 {
		_, _ = fmt.Fprintf(stderr, "run: %s %s\n", res.Status, b)
		return 1
	}
	_, _ = stdout.Write(b)
	if len(b) == 0 || b[len(b)-1] != '\n' {
		_, _ = fmt.Fprintln(stdout)
	}
	if stderrTTY {
		writeRunHandle(stderr, b)
	}
	if !*follow {
		return 0
	}
	var created struct {
		SessionID int64 `json:"session_id"`
	}
	if err := json.Unmarshal(b, &created); err != nil || created.SessionID < 1 {
		_, _ = fmt.Fprintln(stderr, "run: follow: response")
		return 1
	}
	return followRead(*url, *token, strconv.FormatInt(created.SessionID, 10), stdout, stderr, isTerminal(stdout))
}

// writeRunHandle prints the new session id and the documented follow, read,
// and attach commands so the operator can continue from the same terminal.
func writeRunHandle(stderr io.Writer, body []byte) {
	var created struct {
		SessionID   int64 `json:"session_id"`
		SnapshotHit bool  `json:"snapshot_hit"`
	}
	if err := json.Unmarshal(body, &created); err != nil || created.SessionID < 1 {
		return
	}
	label := "miss"
	if created.SnapshotHit {
		label = "hit"
	}
	_, _ = fmt.Fprintf(stderr, "session %d snapshot %s\n", created.SessionID, label)
	if parsed, err := server.ParseSessionURL(os.Getenv("RUSUI_SESSION_URL")); err == nil && parsed != nil {
		_, _ = fmt.Fprintln(stderr, parsed.For(created.SessionID))
	}
	_, _ = fmt.Fprintf(stderr, "rusui read -follow %d\n", created.SessionID)
	_, _ = fmt.Fprintf(stderr, "rusui read %d\n", created.SessionID)
	_, _ = fmt.Fprintf(stderr, "rusui attach %d\n", created.SessionID)
}
