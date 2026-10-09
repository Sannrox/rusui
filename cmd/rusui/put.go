package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
)

func putCLI(args []string) {
	os.Exit(putMain(args, os.Stdout, os.Stderr))
}

func putMain(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("put", flag.ContinueOnError)
	fs.SetOutput(stderr)
	base := fs.String("url", "http://127.0.0.1:8080", "plane URL")
	token := fs.String("token", os.Getenv("RUSUI_OPERATOR_TOKEN"), "operator token")
	file := fs.String("file", "", "local file to copy")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if err := resolveOperatorToken(fs, *base, token); err != nil {
		_, _ = fmt.Fprintln(stderr, "put:", err)
		return 1
	}
	if fs.NArg() != 2 || *file == "" {
		_, _ = fmt.Fprintln(stderr, "usage: rusui put [-url URL] [-token TOKEN] -file LOCAL SESSION_ID DEST")
		return 2
	}
	if _, err := strconv.ParseInt(fs.Arg(0), 10, 64); err != nil {
		_, _ = fmt.Fprintln(stderr, "put: invalid session id")
		return 2
	}
	src, err := os.Open(*file)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	defer func() { _ = src.Close() }()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", fs.Arg(1))
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	if _, err := io.Copy(fw, src); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	if err := mw.Close(); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	dest := strings.TrimRight(*base, "/") + "/sessions/" + fs.Arg(0) + "/workspace?path=" + url.QueryEscape(fs.Arg(1))
	req, err := http.NewRequest(http.MethodPost, dest, &body)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if *token != "" {
		req.Header.Set("Authorization", "Bearer "+*token)
	}
	client, err := planeHTTP(*base)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	res, err := client.Do(req)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	defer func() { _ = res.Body.Close() }()
	b, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 300 {
		_, _ = fmt.Fprintf(stderr, "put: %s %s\n", res.Status, b)
		return 1
	}
	return 0
}
