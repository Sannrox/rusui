package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/sannrox/rusui/internal/ops"
)

func diagnoseCLI(args []string) {
	os.Exit(diagnoseMain(args, os.Stdout))
}

func diagnoseMain(args []string, out io.Writer) int {
	fs := flag.NewFlagSet("diagnose", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	pol := fs.String("policy", "policy.yaml", "policy file")
	addr := fs.String("addr", "127.0.0.1:8080", "listen address to check")
	url := fs.String("url", "", "plane base URL (GET /healthz)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	rep := ops.Diagnose(ops.Options{
		PolicyPath: *pol,
		Addr:       *addr,
		PlaneURL:   *url,
	})
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	if err := enc.Encode(rep); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if !rep.Ready {
		return 1
	}
	return 0
}
