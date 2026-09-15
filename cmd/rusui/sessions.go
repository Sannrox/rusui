package main

import (
	"bufio"
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
	return http.DefaultClient.Do(req)
}

func sessionsCLI(args []string) {
	fs := flag.NewFlagSet("sessions", flag.ExitOnError)
	url := fs.String("url", "http://127.0.0.1:8080", "plane URL")
	token := fs.String("token", os.Getenv("RUSUI_WORKER_SECRET"), "operator/worker token")
	project := fs.String("project", "", "filter by project slug")
	_ = fs.Parse(args)
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

func attachCLI(args []string) {
	fs := flag.NewFlagSet("attach", flag.ExitOnError)
	url := fs.String("url", "http://127.0.0.1:8080", "plane URL")
	token := fs.String("token", os.Getenv("RUSUI_WORKER_SECRET"), "operator/worker token")
	_ = fs.Parse(args)
	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "usage: rusui attach [-url URL] [-token TOKEN] SESSION_ID")
		os.Exit(2)
	}
	id := fs.Arg(0)
	if _, err := strconv.ParseInt(id, 10, 64); err != nil {
		fmt.Fprintln(os.Stderr, "session id")
		os.Exit(2)
	}
	res, err := planeClient(*url, *token, "/sessions/"+id+"/attach")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		b, _ := io.ReadAll(res.Body)
		fmt.Fprintf(os.Stderr, "attach: %s %s\n", res.Status, b)
		os.Exit(1)
	}
	sc := bufio.NewScanner(res.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		fmt.Println(sc.Text())
	}
}
