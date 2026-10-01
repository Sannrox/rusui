package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

type sessionReadView struct {
	SessionID       int64               `json:"session_id"`
	Kind            string              `json:"kind"`
	Prompt          string              `json:"prompt"`
	Transcript      []sessionReadEntryV `json:"transcript"`
	TranscriptState string              `json:"transcript_state"`
	Diff            string              `json:"diff"`
	DiffState       string              `json:"diff_state"`
}

type sessionReadEntryV struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	Body string `json:"body"`
}

func readCLI(args []string) {
	os.Exit(readMain(args, os.Stdout, os.Stderr))
}

func readMain(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("read", flag.ContinueOnError)
	fs.SetOutput(stderr)
	url := fs.String("url", "http://127.0.0.1:8080", "plane URL")
	token := fs.String("token", os.Getenv("RUSUI_OPERATOR_TOKEN"), "operator token")
	follow := fs.Bool("follow", false, "print the transcript, then new events and state until the work finishes")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		_, _ = fmt.Fprintln(stderr, "usage: rusui read [-follow] [-url URL] [-token TOKEN] SESSION_ID")
		return 2
	}
	if _, err := strconv.ParseInt(fs.Arg(0), 10, 64); err != nil {
		_, _ = fmt.Fprintln(stderr, "read: invalid session id")
		return 2
	}
	if *follow {
		return followRead(*url, *token, fs.Arg(0), stdout, stderr)
	}
	res, err := planeClient(*url, *token, "/sessions/"+fs.Arg(0)+"/read")
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	defer func() { _ = res.Body.Close() }()
	b, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 300 {
		_, _ = fmt.Fprintf(stderr, "read: %s %s\n", res.Status, bytesTrim(b))
		return 1
	}
	var view sessionReadView
	if err := json.Unmarshal(b, &view); err != nil {
		_, _ = fmt.Fprintln(stderr, "read: response")
		return 1
	}
	_, _ = fmt.Fprint(stdout, formatSessionRead(view))
	return 0
}

func formatSessionRead(view sessionReadView) string {
	var b strings.Builder
	fmt.Fprintf(&b, "session %d %s\n", view.SessionID, visibleText(view.Kind))
	fmt.Fprintf(&b, "prompt: %s\n", visibleText(view.Prompt))
	if view.TranscriptState != "" {
		fmt.Fprintf(&b, "transcript: %s\n", visibleText(view.TranscriptState))
	} else if len(view.Transcript) == 0 {
		b.WriteString("transcript: empty\n")
	} else {
		b.WriteString("transcript:\n")
		for _, entry := range view.Transcript {
			fmt.Fprintf(&b, "%s\n%s\n", visibleText(entry.Kind), visibleText(entry.Body))
		}
	}
	if view.DiffState != "" {
		fmt.Fprintf(&b, "diff: %s\n", visibleText(view.DiffState))
	} else {
		b.WriteString("diff:\n")
		diff := visibleText(view.Diff)
		b.WriteString(diff)
		if !strings.HasSuffix(diff, "\n") {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// visibleText keeps newlines and tabs and replaces other C0 controls and DEL,
// so workspace text cannot retarget the operator terminal.
func visibleText(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '\n' || r == '\t':
			b.WriteRune(r)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, "\\u%04x", r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func bytesTrim(b []byte) string {
	return strings.TrimSpace(string(b))
}
