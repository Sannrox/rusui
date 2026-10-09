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
)

// retryCLI requeues failed review work, like the Slack `retry` command:
// OWNER/REPO for every failed job, OWNER/REPO#ITEM for one.
func retryCLI(args []string) {
	fs := flag.NewFlagSet("retry", flag.ExitOnError)
	url := fs.String("url", "http://127.0.0.1:8080", "plane URL")
	token := fs.String("token", os.Getenv("RUSUI_OPERATOR_TOKEN"), "operator token")
	_ = fs.Parse(args)
	if err := resolveOperatorToken(fs, *url, token); err != nil {
		fmt.Fprintln(os.Stderr, "retry:", err)
		os.Exit(1)
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: rusui retry [-url URL] [-token TOKEN] OWNER/REPO[#ITEM]")
		os.Exit(2)
	}
	body, _ := json.Marshal(map[string]string{"target": fs.Arg(0)})
	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(*url, "/")+"/retry", bytes.NewReader(body))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+*token)
	client, err := planeHTTP(*url)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	res, err := client.Do(req)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer func() { _ = res.Body.Close() }()
	b, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 300 {
		fmt.Fprintf(os.Stderr, "retry: %s %s\n", res.Status, b)
		os.Exit(1)
	}
	_, _ = os.Stdout.Write(b)
}
