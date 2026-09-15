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

func runCLI(args []string) {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	url := fs.String("url", "http://127.0.0.1:8080", "plane URL")
	project := fs.String("project", "", "project slug")
	token := fs.String("token", os.Getenv("RUSUI_WORKER_SECRET"), "operator/worker token")
	idem := fs.String("idempotency-key", "", "idempotency key")
	_ = fs.Parse(args)
	prompt := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if *project == "" || prompt == "" {
		fmt.Fprintln(os.Stderr, "usage: rusui run -project SLUG [-url URL] [-token TOKEN] [-idempotency-key KEY] PROMPT")
		os.Exit(2)
	}
	body, _ := json.Marshal(map[string]string{"kind": "run", "prompt": prompt})
	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(*url, "/")+"/projects/"+*project+"/sessions", bytes.NewReader(body))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	req.Header.Set("Content-Type", "application/json")
	if *token != "" {
		req.Header.Set("Authorization", "Bearer "+*token)
	}
	if *idem != "" {
		req.Header.Set("Idempotency-Key", *idem)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 300 {
		fmt.Fprintf(os.Stderr, "run: %s %s\n", res.Status, b)
		os.Exit(1)
	}
	os.Stdout.Write(b)
	if len(b) == 0 || b[len(b)-1] != '\n' {
		fmt.Println()
	}
}
