package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

// dispositionCLI records the operator's judgment of one review result, or
// with no RESULT_ID prints the comment gate report (ADR 0038 D6/D8).
func dispositionCLI(args []string) {
	fs := flag.NewFlagSet("disposition", flag.ExitOnError)
	url := fs.String("url", "http://127.0.0.1:8080", "plane URL")
	token := fs.String("token", os.Getenv("RUSUI_OPERATOR_TOKEN"), "operator token")
	value := fs.String("value", "", "useful, neutral, or harmful")
	wrong := fs.Bool("wrong", false, "mark a finding as wrong (false recommendation)")
	note := fs.String("note", "", "optional note")
	_ = fs.Parse(args)
	if err := resolveOperatorToken(fs, *url, token); err != nil {
		fmt.Fprintln(os.Stderr, "disposition:", err)
		os.Exit(1)
	}

	var res *http.Response
	var err error
	switch {
	case fs.NArg() == 0:
		res, err = planeClient(*url, *token, "/dispositions/comment-gate")
	case fs.NArg() == 1 && *value != "":
		body, _ := json.Marshal(map[string]any{"disposition": *value, "wrong_finding": *wrong, "note": *note})
		var req *http.Request
		req, err = http.NewRequest(http.MethodPost, strings.TrimRight(*url, "/")+"/results/"+fs.Arg(0)+"/dispositions", bytes.NewReader(body))
		if err != nil {
			break
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+*token)
		var client *http.Client
		if client, err = planeHTTP(*url); err == nil {
			res, err = client.Do(req)
		}
	default:
		fmt.Fprintln(os.Stderr, "usage: rusui disposition [-url URL] [-token TOKEN] [-value useful|neutral|harmful [-wrong] [-note TEXT] RESULT_ID]")
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer func() { _ = res.Body.Close() }()
	b, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 300 {
		fmt.Fprintf(os.Stderr, "disposition: %s %s\n", res.Status, b)
		os.Exit(1)
	}
	_, _ = os.Stdout.Write(b)
}
