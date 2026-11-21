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

// dispositionCLI records the operator's judgment of one review result, or
// with no RESULT_ID prints the comment gate report (ADR 0038 D6/D8).
func dispositionCLI(args []string) {
	os.Exit(dispositionMain(args, os.Stdout))
}

func dispositionMain(args []string, out io.Writer) int {
	fs := flag.NewFlagSet("disposition", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	url := fs.String("url", "http://127.0.0.1:8080", "plane URL")
	token := fs.String("token", os.Getenv("RUSUI_OPERATOR_TOKEN"), "operator token")
	value := fs.String("value", "", "useful, neutral, or harmful")
	wrong := fs.Bool("wrong", false, "mark a finding as wrong (false recommendation)")
	note := fs.String("note", "", "optional note")
	var seconds *int64
	fs.Func("seconds", "optional supervision time in seconds", func(v string) error {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			return fmt.Errorf("seconds must be a non-negative integer")
		}
		seconds = &n
		return nil
	})
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if err := resolveOperatorToken(fs, *url, token); err != nil {
		fmt.Fprintln(os.Stderr, "disposition:", err)
		return 1
	}

	var res *http.Response
	var err error
	switch {
	case fs.NArg() == 0:
		res, err = planeClient(*url, *token, "/dispositions/comment-gate")
	case fs.NArg() == 1 && *value != "":
		payload := map[string]any{"disposition": *value, "wrong_finding": *wrong, "note": *note}
		if seconds != nil {
			payload["seconds"] = *seconds
		}
		body, _ := json.Marshal(payload)
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
		fmt.Fprintln(os.Stderr, "usage: rusui disposition [-url URL] [-token TOKEN] [-value useful|neutral|harmful [-wrong] [-note TEXT] [-seconds N] RESULT_ID]")
		return 2
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer func() { _ = res.Body.Close() }()
	b, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 300 {
		fmt.Fprintf(os.Stderr, "disposition: %s %s\n", res.Status, b)
		return 1
	}
	_, _ = out.Write(b)
	return 0
}
