package main

import (
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
)

// topCLI prints one live CPU and memory sample for managed environments
// the operator can list (#779). Sleeping, archived, and process
// environments keep their state and omit a sample.
func topCLI(args []string) {
	os.Exit(topMain(args, os.Stdout, os.Stderr))
}

func topMain(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("top", flag.ContinueOnError)
	fs.SetOutput(stderr)
	base := fs.String("url", "http://127.0.0.1:8080", "plane URL")
	token := fs.String("token", os.Getenv("RUSUI_WORKER_SECRET"), "operator/worker token")
	project := fs.String("project", "", "filter by project slug")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if err := resolveOperatorToken(fs, *base, token); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	path := "/sessions?include=stats"
	if *project != "" {
		path += "&project=" + url.QueryEscape(*project)
	}
	res, err := planeClient(*base, *token, path)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	defer func() { _ = res.Body.Close() }()
	b, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 300 {
		_, _ = fmt.Fprintf(stderr, "top: %s %s\n", res.Status, b)
		return 1
	}
	_, _ = stdout.Write(b)
	if len(b) == 0 || b[len(b)-1] != '\n' {
		_, _ = fmt.Fprintln(stdout)
	}
	return 0
}
