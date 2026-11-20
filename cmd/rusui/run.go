package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/sannrox/rusui/internal/server"
)

func runCLI(args []string) {
	os.Exit(runMain(args, os.Stdout, os.Stderr, isTerminal(os.Stderr)))
}

func runMain(args []string, stdout, stderr io.Writer, stderrTTY bool) int {
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
	if err := fs.Parse(args); err != nil {
		return 2
	}
	prompt := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if *project == "" || prompt == "" {
		_, _ = fmt.Fprintln(stderr, "usage: rusui run -project SLUG [-url URL] [-token TOKEN] [-guest NAME] [-idempotency-key KEY] [-effort KEY -repo REPO -ref REF -base-sha SHA -paths PATHS] PROMPT")
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
	return 0
}

// writeRunHandle prints the new session id and the documented follow, read,
// and attach commands so the operator can continue from the same terminal.
func writeRunHandle(stderr io.Writer, body []byte) {
	var created struct {
		SessionID int64 `json:"session_id"`
	}
	if err := json.Unmarshal(body, &created); err != nil || created.SessionID < 1 {
		return
	}
	_, _ = fmt.Fprintf(stderr, "session %d\n", created.SessionID)
	if parsed, err := server.ParseSessionURL(os.Getenv("RUSUI_SESSION_URL")); err == nil && parsed != nil {
		_, _ = fmt.Fprintln(stderr, parsed.For(created.SessionID))
	}
	_, _ = fmt.Fprintf(stderr, "rusui read -follow %d\n", created.SessionID)
	_, _ = fmt.Fprintf(stderr, "rusui read %d\n", created.SessionID)
	_, _ = fmt.Fprintf(stderr, "rusui attach %d\n", created.SessionID)
}
