package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"time"
)

// envlogCLI prints the stored setup, resume, and service output of a
// session's environment (#334). `rusui logs` stays the action receipts (ADR 0031).
func envlogCLI(args []string) {
	fs := flag.NewFlagSet("envlog", flag.ExitOnError)
	base := fs.String("url", "http://127.0.0.1:8080", "plane URL")
	token := fs.String("token", os.Getenv("RUSUI_OPERATOR_TOKEN"), "operator token")
	kind := fs.String("kind", "", "filter by kind (setup, resume, service)")
	name := fs.String("name", "", "filter by service name")
	omitBody := fs.Bool("omit-body", false, "list captures without output bodies")
	_ = fs.Parse(args)
	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "usage: rusui envlog [-url URL] [-token TOKEN] [-kind KIND] [-name NAME] [-omit-body] SESSION_ID")
		os.Exit(2)
	}
	res, err := planeClient(*base, *token, envlogPath(fs.Arg(0), *kind, *name, *omitBody))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer func() { _ = res.Body.Close() }()
	b, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 300 {
		fmt.Fprintf(os.Stderr, "envlog: %s %s\n", res.Status, b)
		os.Exit(1)
	}
	if err := printCaptures(os.Stdout, b); err != nil {
		fmt.Fprintln(os.Stderr, "envlog:", err)
		os.Exit(1)
	}
}

// printCaptures renders the /envlog JSON as one text section per capture.
func printCaptures(w io.Writer, b []byte) error {
	var body struct {
		Captures []struct {
			Kind       string    `json:"kind"`
			Name       string    `json:"name"`
			Output     string    `json:"output"`
			Truncated  bool      `json:"truncated"`
			Failed     bool      `json:"failed"`
			RecordedAt time.Time `json:"recorded_at"`
		} `json:"captures"`
	}
	if err := json.Unmarshal(b, &body); err != nil {
		return err
	}
	if len(body.Captures) == 0 {
		_, err := fmt.Fprintln(w, "no setup, resume, or service output")
		return err
	}
	for _, c := range body.Captures {
		title := c.Kind
		if c.Name != "" {
			title += " " + c.Name
		}
		title += " " + c.RecordedAt.Format(time.RFC3339)
		if c.Failed {
			title += " failed"
		}
		if c.Truncated {
			title += " truncated to the last 1 MiB"
		}
		if !strings.HasSuffix(c.Output, "\n") {
			c.Output += "\n"
		}
		if _, err := fmt.Fprintf(w, "== %s\n%s", title, c.Output); err != nil {
			return err
		}
	}
	return nil
}

func envlogPath(sessionID, kind, name string, omitBody bool) string {
	path := "/sessions/" + sessionID + "/envlog"
	q := url.Values{}
	if kind != "" {
		q.Set("kind", kind)
	}
	if name != "" {
		q.Set("name", name)
	}
	if omitBody {
		q.Set("omit-body", "1")
	}
	if encoded := q.Encode(); encoded != "" {
		path += "?" + encoded
	}
	return path
}
