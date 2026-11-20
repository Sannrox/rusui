package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"strconv"
)

func lsCLI(args []string) {
	os.Exit(lsMain(args, os.Stdout, os.Stderr))
}

func lsMain(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("ls", flag.ContinueOnError)
	fs.SetOutput(stderr)
	base := fs.String("url", "http://127.0.0.1:8080", "plane URL")
	token := fs.String("token", os.Getenv("RUSUI_OPERATOR_TOKEN"), "operator token")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if err := resolveOperatorToken(fs, *base, token); err != nil {
		_, _ = fmt.Fprintln(stderr, "ls:", err)
		return 1
	}
	if fs.NArg() < 1 || fs.NArg() > 2 {
		_, _ = fmt.Fprintln(stderr, "usage: rusui ls [-url URL] [-token TOKEN] SESSION_ID [PATH]")
		return 2
	}
	if _, err := strconv.ParseInt(fs.Arg(0), 10, 64); err != nil {
		_, _ = fmt.Fprintln(stderr, "ls: invalid session id")
		return 2
	}
	path := "/sessions/" + fs.Arg(0) + "/workspace"
	if fs.NArg() == 2 {
		path += "?path=" + url.QueryEscape(fs.Arg(1))
	}
	res, err := planeClient(*base, *token, path)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	defer func() { _ = res.Body.Close() }()
	b, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 300 {
		_, _ = fmt.Fprintf(stderr, "ls: %s %s\n", res.Status, bytesTrim(b))
		return 1
	}
	var view struct {
		Names []string `json:"names"`
	}
	if err := json.Unmarshal(b, &view); err != nil {
		_, _ = fmt.Fprintln(stderr, "ls: response")
		return 1
	}
	for _, name := range view.Names {
		_, _ = fmt.Fprintln(stdout, name)
	}
	return 0
}
