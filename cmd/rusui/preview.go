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

func previewCLI(args []string) {
	os.Exit(previewMain(args, os.Stdout, os.Stderr))
}

// previewMain mints a preview grant for one environment port and prints
// the preview-origin URL. The operator token stays on the plane request;
// stdout is only that URL (grant as `?g=`).
func previewMain(args []string, out, errw io.Writer) int {
	fs := flag.NewFlagSet("preview", flag.ContinueOnError)
	fs.SetOutput(errw)
	base := fs.String("url", "http://127.0.0.1:8080", "plane URL")
	token := fs.String("token", os.Getenv("RUSUI_OPERATOR_TOKEN"), "operator token")
	port := fs.Int("port", 0, "environment port to preview")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 || *port < 1024 || *port > 65535 {
		_, _ = fmt.Fprintln(errw, "usage: rusui preview [-url URL] [-token TOKEN] -port PORT SESSION_ID")
		return 2
	}
	if _, err := strconv.ParseInt(fs.Arg(0), 10, 64); err != nil {
		_, _ = fmt.Fprintln(errw, "preview: session id")
		return 2
	}
	req, err := newPreviewRequest(*base, *token, fs.Arg(0), *port)
	if err != nil {
		_, _ = fmt.Fprintln(errw, err)
		return 1
	}
	client, err := planeHTTP(*base)
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
	body, err := io.ReadAll(res.Body)
	if err != nil {
		_, _ = fmt.Fprintln(errw, err)
		return 1
	}
	if res.StatusCode >= 300 {
		_, _ = fmt.Fprintf(errw, "preview: %s %s\n", res.Status, strings.TrimSpace(string(body)))
		return 1
	}
	var got struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(body, &got); err != nil || strings.TrimSpace(got.URL) == "" {
		_, _ = fmt.Fprintln(errw, "preview: missing url")
		return 1
	}
	_, _ = fmt.Fprintln(out, got.URL)
	return 0
}

func newPreviewRequest(planeURL, token, id string, port int) (*http.Request, error) {
	body, err := json.Marshal(map[string]int{"port": port})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(planeURL, "/")+"/sessions/"+id+"/preview", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return req, nil
}
