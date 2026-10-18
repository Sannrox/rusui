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

	"github.com/sannrox/rusui/internal/ops"
)

func haltCLI(args []string) {
	os.Exit(haltMain(args, os.Stdout))
}

func unhaltCLI(args []string) {
	os.Exit(unhaltMain(args, os.Stdout))
}

func haltMain(args []string, out io.Writer) int {
	return haltCommand("halt", "/halt", args, out)
}

func unhaltMain(args []string, out io.Writer) int {
	return haltCommand("unhalt", "/unhalt", args, out)
}

func haltCommand(name, path string, args []string, out io.Writer) int {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	url := fs.String("url", ops.PlaneBaseURL("127.0.0.1:8080", os.Getenv), "plane URL")
	token := fs.String("token", os.Getenv("RUSUI_WORKER_SECRET"), "worker token")
	scope := fs.String("scope", "", "tenant, project, or agent")
	target := fs.String("target", "", "halt target id")
	reason := fs.String("reason", "", "reason recorded on the event")
	operator := fs.String("operator", "", "operator recorded on the event")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if strings.TrimSpace(*scope) == "" || strings.TrimSpace(*target) == "" {
		fmt.Fprintf(os.Stderr, "usage: rusui %s -scope tenant|project|agent -target ID [-reason TEXT] [-operator NAME] [-url URL] [-token TOKEN]\n", name)
		return 2
	}
	body, err := json.Marshal(map[string]string{
		"scope": *scope, "target": *target, "reason": *reason, "operator": *operator,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return postJSONWorker(name, *url, path, *token, body, out)
}

func postJSONWorker(name, base, path, token string, body []byte, out io.Writer) int {
	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(base, "/")+path, bytes.NewReader(body))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	client, err := planeHTTP(base)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	res, err := client.Do(req)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer func() { _ = res.Body.Close() }()
	b, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 300 {
		fmt.Fprintf(os.Stderr, "%s: %s %s\n", name, res.Status, b)
		return 1
	}
	_, _ = out.Write(b)
	if len(b) == 0 || b[len(b)-1] != '\n' {
		_, _ = fmt.Fprintln(out)
	}
	return 0
}
