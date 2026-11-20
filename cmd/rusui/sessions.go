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

func planeClient(url, token, path string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodGet, strings.TrimRight(url, "/")+path, nil)
	if err != nil {
		return nil, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	client, err := planeHTTP(url)
	if err != nil {
		return nil, err
	}
	return client.Do(req)
}

func sessionsCLI(args []string) {
	fs := flag.NewFlagSet("sessions", flag.ExitOnError)
	url := fs.String("url", "http://127.0.0.1:8080", "plane URL")
	token := fs.String("token", os.Getenv("RUSUI_WORKER_SECRET"), "operator/worker token")
	project := fs.String("project", "", "filter by project slug")
	_ = fs.Parse(args)
	if err := resolveOperatorToken(fs, *url, token); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	path := "/sessions"
	if *project != "" {
		path += "?project=" + *project
	}
	res, err := planeClient(*url, *token, path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 300 {
		fmt.Fprintf(os.Stderr, "sessions: %s %s\n", res.Status, b)
		os.Exit(1)
	}
	os.Stdout.Write(b)
	if len(b) == 0 || b[len(b)-1] != '\n' {
		fmt.Println()
	}
}

func promptCLI(args []string) {
	os.Exit(promptMain(args, os.Stdout, os.Stderr))
}

func promptMain(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("prompt", flag.ContinueOnError)
	fs.SetOutput(stderr)
	url := fs.String("url", "http://127.0.0.1:8080", "plane URL")
	token := fs.String("token", os.Getenv("RUSUI_WORKER_SECRET"), "operator/worker token")
	steer := fs.Bool("steer", false, "interrupt a running turn with this prompt")
	queue := fs.Bool("queue", false, "start this prompt as the next turn once the current turn ends")
	dropQueue := fs.Bool("drop-queue", false, "drop queued prompts that have not started; the current turn keeps running")
	var files pathList
	fs.Var(&files, "file", "attach a file to the prompt (repeatable)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	usage := func() int {
		_, _ = fmt.Fprintln(stderr, "usage: rusui prompt [-steer | -queue] [-file PATH] [-url URL] [-token TOKEN] SESSION_ID TEXT\n       rusui prompt -drop-queue [-url URL] [-token TOKEN] SESSION_ID")
		return 2
	}
	if (*steer && *queue) || (*dropQueue && (*steer || *queue || len(files) > 0)) {
		return usage()
	}
	if *steer && len(files) > 0 {
		_, _ = fmt.Fprintln(stderr, "attachments are not a steer")
		return 2
	}
	if (*dropQueue && fs.NArg() != 1) || (!*dropQueue && fs.NArg() < 2) {
		return usage()
	}
	id := fs.Arg(0)
	if _, err := strconv.ParseInt(id, 10, 64); err != nil {
		_, _ = fmt.Fprintln(stderr, "session id")
		return 2
	}
	if err := resolveOperatorToken(fs, *url, token); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	var req *http.Request
	var err error
	if *dropQueue {
		req, err = newDropQueueRequest(*url, *token, id)
	} else {
		prompt := strings.TrimSpace(strings.Join(fs.Args()[1:], " "))
		atts, loadErr := loadPromptAttachments(files)
		if loadErr != nil {
			_, _ = fmt.Fprintln(stderr, loadErr)
			return 1
		}
		req, err = newPromptRequest(*url, *token, id, prompt, *steer, *queue, atts)
	}
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	client, err := planeHTTP(*url)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	res, err := client.Do(req)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 300 {
		_, _ = fmt.Fprintf(stderr, "prompt: %s %s\n", res.Status, b)
		return 1
	}
	_, _ = stdout.Write(b)
	if len(b) == 0 || b[len(b)-1] != '\n' {
		_, _ = fmt.Fprintln(stdout)
	}
	return 0
}

func newPromptRequest(planeURL, token, id, prompt string, steer, queued bool, attachments []map[string]string) (*http.Request, error) {
	payload := map[string]any{"prompt": prompt, "steer": steer, "queued": queued}
	if len(attachments) > 0 {
		payload["attachments"] = attachments
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(planeURL, "/")+"/sessions/"+id+"/turns", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return req, nil
}

func newDropQueueRequest(planeURL, token, id string) (*http.Request, error) {
	req, err := http.NewRequest(http.MethodDelete, strings.TrimRight(planeURL, "/")+"/sessions/"+id+"/queued", nil)
	if err != nil {
		return nil, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return req, nil
}
