package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/sannrox/rusui/internal/runner"
)

var (
	Version   = "dev"
	GitCommit = "unknown"
	BuildTime = "unknown"
)

func main() {
	base := flag.String("url", "http://127.0.0.1:8080", "plane URL")
	repo := flag.String("repo", "", "repository to claim")
	token := flag.String("token", os.Getenv("RUSUI_WORKER_SECRET"), "runner bootstrap token")
	name := flag.String("name", "local", "runner name")
	driver := flag.String("driver", "", "process driver command (space-separated)")
	once := flag.Bool("once", false, "claim at most one turn and exit")
	printVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()
	if *printVersion {
		fmt.Printf("rusui-runner %s commit=%s time=%s\n", Version, GitCommit, BuildTime)
		return
	}
	if *repo == "" || *driver == "" {
		fmt.Fprintln(os.Stderr, "need -repo and -driver")
		os.Exit(1)
	}
	c := &runner.Client{Base: *base, Bootstrap: *token, Repo: *repo, Name: *name}
	cmd := strings.Fields(*driver)
	for {
		if err := runner.OneTurn(context.Background(), c, cmd); err != nil {
			fmt.Fprintf(os.Stderr, "turn: %v\n", err)
		}
		if *once {
			return
		}
		time.Sleep(2 * time.Second)
	}
}
