package main

import (
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/sannrox/rusui/internal/ops"
)

func archiveCLI(args []string) {
	os.Exit(archiveMain("archive", args, os.Stdout))
}

func unarchiveCLI(args []string) {
	os.Exit(archiveMain("unarchive", args, os.Stdout))
}

func archiveMain(verb string, args []string, out io.Writer) int {
	fs := flag.NewFlagSet(verb, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	url := fs.String("url", ops.PlaneBaseURL("127.0.0.1:8080", os.Getenv), "plane URL")
	token := fs.String("token", os.Getenv("RUSUI_WORKER_SECRET"), "operator/worker token")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if err := resolveOperatorToken(fs, *url, token); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if fs.NArg() != 1 {
		fmt.Fprintf(os.Stderr, "usage: rusui %s [-url URL] [-token TOKEN] SESSION_ID\n", verb)
		return 2
	}
	id := fs.Arg(0)
	if _, err := strconv.ParseInt(id, 10, 64); err != nil {
		fmt.Fprintln(os.Stderr, "session id")
		return 2
	}
	return postWorker(verb, *url, "/sessions/"+id+"/"+verb, *token, out)
}

func newArchiveRequest(planeURL, token, id, verb string) (*http.Request, error) {
	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(planeURL, "/")+"/sessions/"+id+"/"+verb, nil)
	if err != nil {
		return nil, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return req, nil
}
