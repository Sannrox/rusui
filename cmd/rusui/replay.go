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
)

func replayCLI(args []string) {
	os.Exit(replayMain(args, os.Stdout, os.Stderr))
}

func replayMain(args []string, out, errw io.Writer) int {
	fs := flag.NewFlagSet("replay", flag.ContinueOnError)
	fs.SetOutput(errw)
	url := fs.String("url", "http://127.0.0.1:8080", "plane URL")
	token := fs.String("token", os.Getenv("RUSUI_WORKER_SECRET"), "operator/worker token")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if err := resolveOperatorToken(fs, *url, token); err != nil {
		_, _ = fmt.Fprintln(errw, err)
		return 1
	}
	if fs.NArg() != 2 {
		_, _ = fmt.Fprintln(errw, "usage: rusui replay [-url URL] [-token TOKEN] SESSION_ID TURN_ID")
		return 2
	}
	if _, err := strconv.ParseInt(fs.Arg(0), 10, 64); err != nil {
		_, _ = fmt.Fprintln(errw, "session id")
		return 2
	}
	if _, err := strconv.ParseInt(fs.Arg(1), 10, 64); err != nil {
		_, _ = fmt.Fprintln(errw, "turn id")
		return 2
	}
	path := "/sessions/" + fs.Arg(0) + "/turns/" + fs.Arg(1) + "/replay"
	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(*url, "/")+path, bytes.NewReader([]byte("{}")))
	if err != nil {
		_, _ = fmt.Fprintln(errw, err)
		return 1
	}
	req.Header.Set("Content-Type", "application/json")
	if *token != "" {
		req.Header.Set("Authorization", "Bearer "+*token)
	}
	client, err := planeHTTP(*url)
	if err != nil {
		_, _ = fmt.Fprintln(errw, err)
		return 1
	}
	res, err := client.Do(req)
	if err != nil {
		_, _ = fmt.Fprintln(errw, err)
		return 1
	}
	defer func() { _ = res.Body.Close() }()
	b, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 300 {
		_, _ = fmt.Fprintf(errw, "replay: %s %s\n", res.Status, b)
		return 1
	}
	var buf bytes.Buffer
	if err := json.Indent(&buf, b, "", "  "); err == nil {
		b = buf.Bytes()
	}
	_, _ = out.Write(b)
	if len(b) == 0 || b[len(b)-1] != '\n' {
		_, _ = fmt.Fprintln(out)
	}
	return 0
}
