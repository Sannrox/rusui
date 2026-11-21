package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"os"
	"strconv"
	"strings"

	"github.com/sannrox/rusui/internal/sessionexport"
)

func exportSessionCLI(args []string) {
	os.Exit(exportSessionMain(args, os.Stdout, os.Stderr))
}

func importSessionCLI(args []string) {
	os.Exit(importSessionMain(args, os.Stdout, os.Stderr))
}

// exportSessionMain fetches a signed operator session bundle.
func exportSessionMain(args []string, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("export-session", flag.ContinueOnError)
	fs.SetOutput(errOut)
	url := fs.String("url", "http://127.0.0.1:8080", "plane URL")
	token := fs.String("token", os.Getenv("RUSUI_OPERATOR_TOKEN"), "operator token")
	ttl := fs.Duration("ttl", sessionexport.DefaultTTL, "signed expiry window")
	dest := fs.String("out", "", "write the bundle to this file (default stdout)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if err := resolveOperatorToken(fs, *url, token); err != nil {
		_, _ = fmt.Fprintln(errOut, err)
		return 1
	}
	if fs.NArg() != 1 || *ttl <= 0 {
		_, _ = fmt.Fprintln(errOut, "usage: rusui export-session [-url URL] [-token TOKEN] [-ttl DURATION] [-out FILE] SESSION_ID")
		return 2
	}
	if id, err := strconv.ParseInt(fs.Arg(0), 10, 64); err != nil || id < 1 {
		_, _ = fmt.Fprintln(errOut, "export-session: session id must be a positive integer")
		return 2
	}
	path := "/sessions/" + fs.Arg(0) + "/export-session?ttl=" + ttl.String()
	req, err := http.NewRequest(http.MethodGet, strings.TrimRight(*url, "/")+path, nil)
	if err != nil {
		_, _ = fmt.Fprintln(errOut, err)
		return 1
	}
	if *token != "" {
		req.Header.Set("Authorization", "Bearer "+*token)
	}
	client, err := planeHTTP(*url)
	if err != nil {
		_, _ = fmt.Fprintln(errOut, err)
		return 1
	}
	res, err := client.Do(req)
	if err != nil {
		_, _ = fmt.Fprintln(errOut, err)
		return 1
	}
	defer func() { _ = res.Body.Close() }()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		_, _ = fmt.Fprintln(errOut, err)
		return 1
	}
	if res.StatusCode >= 300 {
		_, _ = fmt.Fprintf(errOut, "export-session: %s %s\n", res.Status, strings.TrimSpace(string(body)))
		return 1
	}
	if *dest != "" {
		if err := os.WriteFile(*dest, body, 0o600); err != nil {
			_, _ = fmt.Fprintln(errOut, "export-session:", err)
			return 1
		}
		_, _ = fmt.Fprintln(out, *dest)
		return 0
	}
	_, _ = out.Write(body)
	if len(body) == 0 || body[len(body)-1] != '\n' {
		_, _ = fmt.Fprintln(out)
	}
	return 0
}

// importSessionMain posts a signed bundle and prints the minted session id.
func importSessionMain(args []string, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("import-session", flag.ContinueOnError)
	fs.SetOutput(errOut)
	url := fs.String("url", "http://127.0.0.1:8080", "plane URL")
	token := fs.String("token", os.Getenv("RUSUI_OPERATOR_TOKEN"), "operator token")
	project := fs.String("project", "", "project that receives the new session")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if err := resolveOperatorToken(fs, *url, token); err != nil {
		_, _ = fmt.Fprintln(errOut, err)
		return 1
	}
	if fs.NArg() != 1 || *project == "" {
		_, _ = fmt.Fprintln(errOut, "usage: rusui import-session [-url URL] [-token TOKEN] -project SLUG FILE")
		return 2
	}
	raw, err := os.ReadFile(fs.Arg(0))
	if err != nil {
		_, _ = fmt.Fprintln(errOut, "import-session:", err)
		return 1
	}
	q := neturl.Values{"project": {*project}}
	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(*url, "/")+"/import-session?"+q.Encode(), bytes.NewReader(raw))
	if err != nil {
		_, _ = fmt.Fprintln(errOut, err)
		return 1
	}
	if *token != "" {
		req.Header.Set("Authorization", "Bearer "+*token)
	}
	req.Header.Set("Content-Type", "application/json")
	client, err := planeHTTP(*url)
	if err != nil {
		_, _ = fmt.Fprintln(errOut, err)
		return 1
	}
	res, err := client.Do(req)
	if err != nil {
		_, _ = fmt.Fprintln(errOut, err)
		return 1
	}
	defer func() { _ = res.Body.Close() }()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		_, _ = fmt.Fprintln(errOut, err)
		return 1
	}
	if res.StatusCode >= 300 {
		_, _ = fmt.Fprintf(errOut, "import-session: %s %s\n", res.Status, strings.TrimSpace(string(body)))
		return 1
	}
	_, _ = out.Write(body)
	if len(body) == 0 || body[len(body)-1] != '\n' {
		_, _ = fmt.Fprintln(out)
	}
	return 0
}
