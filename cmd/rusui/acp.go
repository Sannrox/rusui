package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strconv"

	"github.com/sannrox/rusui/internal/acp"
)

func acpCLI(args []string) {
	fs := flag.NewFlagSet("acp", flag.ExitOnError)
	url := fs.String("url", "http://127.0.0.1:8080", "plane URL")
	token := fs.String("token", os.Getenv("RUSUI_OPERATOR_TOKEN"), "operator token (worker secret accepted)")
	session := fs.String("session", "", "existing rusui session id to load")
	_ = fs.Parse(args)
	if *token == "" {
		*token = os.Getenv("RUSUI_WORKER_SECRET")
	}
	if *token == "" {
		fmt.Fprintln(os.Stderr, "acp: set -token or RUSUI_OPERATOR_TOKEN")
		os.Exit(2)
	}
	agent := &acp.EditorAgent{
		In:    os.Stdin,
		Out:   os.Stdout,
		Plane: &acp.HTTPPlane{Base: *url, Token: *token},
	}
	if *session != "" {
		if _, err := strconv.ParseInt(*session, 10, 64); err != nil {
			fmt.Fprintln(os.Stderr, "acp: session id")
			os.Exit(2)
		}
	}
	if err := agent.Serve(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
