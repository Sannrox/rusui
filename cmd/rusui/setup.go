package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	envpkg "github.com/sannrox/rusui/internal/env"
	"github.com/sannrox/rusui/internal/ops"
	"github.com/sannrox/rusui/internal/setup"
)

func setupCLI(args []string) {
	os.Exit(setupMain(args, os.Stdout, os.Getenv, envpkg.LookRuntime))
}

// setupMain runs `rusui setup plan|apply` (ADR 0018).
func setupMain(args []string, out io.Writer, getenv func(string) string, look func() (envpkg.Runtime, error)) int {
	if len(args) == 0 || (args[0] != "plan" && args[0] != "apply") {
		fmt.Fprintln(os.Stderr, "usage: rusui setup plan|apply [-state DIR] [-rotate-tls] [-rebuild-image] [-json]")
		return 2
	}
	mode := args[0]
	fs := flag.NewFlagSet("setup "+mode, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	state := fs.String("state", setup.DefaultStateDir(getenv), "state directory")
	rotate := fs.Bool("rotate-tls", false, "replace existing plane TLS material")
	rebuild := fs.Bool("rebuild-image", false, "rebuild the reference guest image with --pull --no-cache")
	asJSON := fs.Bool("json", false, "print steps as JSON")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	opt := setup.Options{StateDir: *state, RotateTLS: *rotate, RebuildImage: *rebuild, Getenv: getenv}
	if rt, err := look(); err == nil {
		if n, ok := rt.(setup.Network); ok {
			opt.Network = n
		}
	}
	var steps []setup.Step
	var err error
	if mode == "plan" {
		steps, err = setup.Plan(opt)
	} else {
		steps, err = setup.Apply(opt)
	}
	if *asJSON {
		_ = json.NewEncoder(out).Encode(steps)
	} else {
		for _, s := range steps {
			_, _ = fmt.Fprintf(out, "%-9s %-18s %s\n", s.Action, s.Item, s.Detail)
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "setup:", err)
		return 1
	}
	if mode == "plan" {
		return 0
	}
	p := setup.PathsFor(*state)
	vals := setup.ReadEnv(p.Env)
	fileEnv := func(k string) string {
		if v, ok := vals[k]; ok && v != "" {
			return v
		}
		return getenv(k)
	}
	rep := ops.Diagnose(ops.Options{PolicyPath: p.Policy, Addr: "127.0.0.1:8080", Env: fileEnv, LookRuntime: look})
	if !*asJSON {
		_, _ = fmt.Fprintf(out, "\ndiagnose ready=%v\n", rep.Ready)
		for _, c := range rep.Checks {
			_, _ = fmt.Fprintf(out, "  %-12s %-14s %s\n", c.Name, c.Status, c.Detail)
		}
		_, _ = fmt.Fprintf(out, "\nstart: set -a; . %q; set +a; rusui -db %q -policy %q\n", p.Env, p.DB, p.Policy)
	}
	if !rep.Ready {
		return 1
	}
	return 0
}
