package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/sannrox/rusui/internal/env"
	"github.com/sannrox/rusui/internal/ops"
	"github.com/sannrox/rusui/internal/runner"
)

var (
	Version   = "dev"
	GitCommit = "unknown"
	BuildTime = "unknown"
)

type repoFlags []string

func (r *repoFlags) Set(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("repository must not be empty")
	}
	for _, existing := range *r {
		if strings.EqualFold(existing, value) {
			return fmt.Errorf("repository %q was listed more than once", value)
		}
	}
	*r = append(*r, value)
	return nil
}

func (r *repoFlags) String() string { return strings.Join(*r, ",") }

func main() {
	base := flag.String("url", "http://127.0.0.1:8080", "plane URL")
	var repos repoFlags
	flag.Var(&repos, "repo", "repository to claim (repeat to serve multiple repositories)")
	token := flag.String("token", os.Getenv("RUSUI_WORKER_SECRET"), "runner bootstrap token")
	name := flag.String("name", "local", "runner name")
	driver := flag.String("driver", "", "process driver command (space-separated)")
	acpHost := flag.Bool("acp", false, "host one ACP turn with the plane's guest (Grok or Claude Code) instead of the process driver")
	caFile := flag.String("ca", os.Getenv("RUSUI_PLANE_CA"), "plane CA for an https plane URL")
	once := flag.Bool("once", false, "claim at most one turn and exit")
	printVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()
	if *printVersion {
		fmt.Printf("rusui-runner %s commit=%s time=%s\n", Version, GitCommit, BuildTime)
		return
	}
	if len(repos) == 0 || (*driver == "" && !*acpHost) {
		fmt.Fprintln(os.Stderr, "need at least one -repo and -driver, or -repo and -acp")
		os.Exit(1)
	}
	c := &runner.Client{Base: *base, Bootstrap: *token, Repos: append([]string(nil), repos...), Name: *name}
	if strings.HasPrefix(*base, "https://") {
		if *caFile == "" {
			fmt.Fprintln(os.Stderr, "need -ca or RUSUI_PLANE_CA for an https plane URL")
			os.Exit(1)
		}
		hc, err := ops.TLSClient(*caFile)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		hc.Timeout = 30 * time.Second
		c.HTTP = hc
	}
	if rt, err := env.LookRuntime(); err == nil {
		if x, ok := rt.(env.StdioExecutor); ok {
			c.Exec = x
		}
	}
	cmd := strings.Fields(*driver)
	for {
		var err error
		if *acpHost {
			err = runner.OneACPTurn(context.Background(), c, runner.GuestHost(c))
		} else {
			err = runner.OneTurn(context.Background(), c, cmd)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "turn: %v\n", err)
		}
		if *once {
			return
		}
		time.Sleep(2 * time.Second)
	}
}
