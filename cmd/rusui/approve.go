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

func logsCLI(args []string) {
	fs := flag.NewFlagSet("logs", flag.ExitOnError)
	url := fs.String("url", "http://127.0.0.1:8080", "plane URL")
	token := fs.String("token", os.Getenv("RUSUI_WORKER_SECRET"), "operator/worker token")
	_ = fs.Parse(args)
	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "usage: rusui logs [-url URL] [-token TOKEN] SESSION_ID")
		os.Exit(2)
	}
	res, err := planeClient(*url, *token, "/sessions/"+fs.Arg(0)+"/logs")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 300 {
		fmt.Fprintf(os.Stderr, "logs: %s %s\n", res.Status, b)
		os.Exit(1)
	}
	os.Stdout.Write(b)
	if len(b) == 0 || b[len(b)-1] != '\n' {
		fmt.Println()
	}
}

func approveCLI(args []string) {
	fs := flag.NewFlagSet("approve", flag.ExitOnError)
	url := fs.String("url", "http://127.0.0.1:8080", "plane URL")
	token := fs.String("token", os.Getenv("RUSUI_WORKER_SECRET"), "operator/worker token")
	allow := fs.Bool("allow", false, "record allow")
	deny := fs.Bool("deny", false, "record deny")
	_ = fs.Parse(args)
	if fs.NArg() == 0 {
		res, err := planeClient(*url, *token, "/approvals")
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		if res.StatusCode >= 300 {
			fmt.Fprintf(os.Stderr, "approve: %s %s\n", res.Status, b)
			os.Exit(1)
		}
		os.Stdout.Write(b)
		if len(b) == 0 || b[len(b)-1] != '\n' {
			fmt.Println()
		}
		return
	}
	if !*allow && !*deny {
		fmt.Fprintln(os.Stderr, "usage: rusui approve [-allow|-deny] ACTION_ID")
		os.Exit(2)
	}
	dec := "deny"
	if *allow {
		dec = "allow"
	}
	body, _ := json.Marshal(map[string]string{"decision": dec})
	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(*url, "/")+"/approvals/"+fs.Arg(0), bytes.NewReader(body))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	req.Header.Set("Content-Type", "application/json")
	if *token != "" {
		req.Header.Set("Authorization", "Bearer "+*token)
	}
	client, err := planeHTTP(*url)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	res, err := client.Do(req)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		b, _ := io.ReadAll(res.Body)
		fmt.Fprintf(os.Stderr, "approve: %s %s\n", res.Status, b)
		os.Exit(1)
	}
}
