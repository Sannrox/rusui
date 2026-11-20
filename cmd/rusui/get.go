package main

import (
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"strconv"
	"strings"
)

func getCLI(args []string) {
	os.Exit(getMain(args, os.Stdout, os.Stderr))
}

func getMain(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("get", flag.ContinueOnError)
	fs.SetOutput(stderr)
	base := fs.String("url", "http://127.0.0.1:8080", "plane URL")
	token := fs.String("token", os.Getenv("RUSUI_OPERATOR_TOKEN"), "operator token")
	file := fs.String("file", "", "local file to write")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if err := resolveOperatorToken(fs, *base, token); err != nil {
		_, _ = fmt.Fprintln(stderr, "get:", err)
		return 1
	}
	if fs.NArg() != 2 || *file == "" {
		_, _ = fmt.Fprintln(stderr, "usage: rusui get [-url URL] [-token TOKEN] -file LOCAL SESSION_ID SRC")
		return 2
	}
	if _, err := strconv.ParseInt(fs.Arg(0), 10, 64); err != nil {
		_, _ = fmt.Fprintln(stderr, "get: invalid session id")
		return 2
	}
	path := "/sessions/" + fs.Arg(0) + "/workspace?path=" + url.QueryEscape(fs.Arg(1))
	res, err := planeClient(*base, *token, path)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode >= 300 {
		b, _ := io.ReadAll(res.Body)
		_, _ = fmt.Fprintf(stderr, "get: %s %s\n", res.Status, bytesTrim(b))
		return 1
	}
	media, _, _ := strings.Cut(res.Header.Get("Content-Type"), ";")
	if strings.TrimSpace(media) != "application/octet-stream" {
		_, _ = io.Copy(io.Discard, res.Body)
		_, _ = fmt.Fprintln(stderr, "get: not a file")
		return 1
	}
	dst, err := os.OpenFile(*file, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	defer func() { _ = dst.Close() }()
	if _, err := io.Copy(dst, res.Body); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}
