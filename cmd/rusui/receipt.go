package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/sannrox/rusui/internal/receipt"
)

func receiptCLI(args []string) {
	os.Exit(receiptMain(args, os.Stdout, os.Stderr))
}

// receiptMain fetches the portable review receipt for one session.
func receiptMain(args []string, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("receipt", flag.ContinueOnError)
	fs.SetOutput(errOut)
	url := fs.String("url", "http://127.0.0.1:8080", "plane URL")
	token := fs.String("token", os.Getenv("RUSUI_WORKER_SECRET"), "operator/worker token")
	format := fs.String("format", "md", "md or json")
	outDir := fs.String("out", "", "write r{id}.json and r{id}.md to this directory")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if err := resolveOperatorToken(fs, *url, token); err != nil {
		_, _ = fmt.Fprintln(errOut, err)
		return 1
	}
	if fs.NArg() != 1 || (*format != "md" && *format != "json") {
		_, _ = fmt.Fprintln(errOut, "usage: rusui receipt [-url URL] [-token TOKEN] [-format md|json] [-out DIR] SESSION_ID")
		return 2
	}
	if id, err := strconv.ParseInt(fs.Arg(0), 10, 64); err != nil || id < 1 {
		_, _ = fmt.Fprintln(errOut, "receipt: session id must be a positive integer")
		return 2
	}
	path := "/sessions/" + fs.Arg(0) + "/receipt"
	req, err := http.NewRequest(http.MethodGet, strings.TrimRight(*url, "/")+path, nil)
	if err != nil {
		_, _ = fmt.Fprintln(errOut, err)
		return 1
	}
	if *token != "" {
		req.Header.Set("Authorization", "Bearer "+*token)
	}
	if *outDir != "" || *format == "json" {
		req.Header.Set("Accept", "application/json")
	} else {
		req.Header.Set("Accept", "text/markdown")
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
		_, _ = fmt.Fprintf(errOut, "receipt: %s %s\n", res.Status, strings.TrimSpace(string(body)))
		return 1
	}
	if *outDir != "" {
		var exp receipt.Export
		if err := json.Unmarshal(body, &exp); err != nil {
			_, _ = fmt.Fprintln(errOut, "receipt:", err)
			return 1
		}
		if err := receipt.WriteFiles(*outDir, exp, nil); err != nil {
			_, _ = fmt.Fprintln(errOut, "receipt:", err)
			return 1
		}
		_, _ = fmt.Fprintf(out, "wrote %s/r%d.json\n", strings.TrimRight(*outDir, "/"), exp.ReviewRevisionID)
		_, _ = fmt.Fprintf(out, "wrote %s/r%d.md\n", strings.TrimRight(*outDir, "/"), exp.ReviewRevisionID)
		return 0
	}
	_, _ = out.Write(body)
	if len(body) == 0 || body[len(body)-1] != '\n' {
		_, _ = fmt.Fprintln(out)
	}
	return 0
}
