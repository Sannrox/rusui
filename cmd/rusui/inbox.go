package main

import (
	"flag"
	"fmt"
	"io"
	"os"
)

// inboxCLI prints the review results that still need a disposition, or
// with -all every item's latest result (GET /inbox). Record a decision
// with `rusui disposition -value ... RESULT_ID`.
func inboxCLI(args []string) {
	fs := flag.NewFlagSet("inbox", flag.ExitOnError)
	url := fs.String("url", "http://127.0.0.1:8080", "plane URL")
	token := fs.String("token", os.Getenv("RUSUI_OPERATOR_TOKEN"), "operator token")
	all := fs.Bool("all", false, "list every item, not only those needing a disposition")
	_ = fs.Parse(args)
	if fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "usage: rusui inbox [-url URL] [-token TOKEN] [-all]")
		os.Exit(2)
	}
	if err := resolveOperatorToken(fs, *url, token); err != nil {
		fmt.Fprintln(os.Stderr, "inbox:", err)
		os.Exit(1)
	}
	path := "/inbox"
	if *all {
		path += "?all=1"
	}
	res, err := planeClient(*url, *token, path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer func() { _ = res.Body.Close() }()
	b, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 300 {
		fmt.Fprintf(os.Stderr, "inbox: %s %s\n", res.Status, b)
		os.Exit(1)
	}
	_, _ = os.Stdout.Write(b)
}
