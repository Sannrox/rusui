package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	envpkg "github.com/sannrox/rusui/internal/env"
	"github.com/sannrox/rusui/internal/ops"
	"github.com/sannrox/rusui/internal/setup"
)

func setupCLI(args []string) {
	os.Exit(setupMain(args, os.Stdout, os.Getenv, envpkg.LookRuntime, setup.PlatformServiceManager()))
}

// setupMain runs `rusui setup plan|apply` (ADR 0018).
func setupMain(args []string, out io.Writer, getenv func(string) string, look func() (envpkg.Runtime, error), services setup.ServiceManager) int {
	if len(args) == 0 || (args[0] != "plan" && args[0] != "apply" && args[0] != "remove-service") {
		fmt.Fprintln(os.Stderr, "usage: rusui setup plan|apply|remove-service [-state DIR] [-addr LOOPBACK:PORT] [-rotate-tls] [-rebuild-image] [-json]")
		return 2
	}
	mode := args[0]
	fs := flag.NewFlagSet("setup "+mode, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	state := fs.String("state", setup.DefaultStateDir(getenv), "state directory")
	addr := fs.String("addr", "127.0.0.1:8080", "loopback address for the user service")
	rotate := fs.Bool("rotate-tls", false, "replace existing plane TLS material")
	rebuild := fs.Bool("rebuild-image", false, "rebuild the reference guest image with --pull --no-cache")
	asJSON := fs.Bool("json", false, "print steps as JSON")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	stateDir, err := filepath.Abs(*state)
	if err != nil {
		fmt.Fprintln(os.Stderr, "setup: resolve state directory:", err)
		return 1
	}
	opt := setup.Options{StateDir: stateDir, Addr: *addr, RotateTLS: *rotate, RebuildImage: *rebuild, Getenv: getenv, ServiceManager: services}
	if mode == "remove-service" {
		step, err := setup.RemoveService(opt)
		steps := []setup.Step{step}
		if *asJSON {
			_ = json.NewEncoder(out).Encode(steps)
		} else {
			_, _ = fmt.Fprintf(out, "%-9s %-18s %s\n", step.Action, step.Item, step.Detail)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "setup:", err)
			return 1
		}
		return 0
	}
	if rt, err := look(); err == nil {
		if n, ok := rt.(setup.Network); ok {
			opt.Network = n
		}
	}
	var steps []setup.Step
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
	p := setup.PathsFor(stateDir)
	vals := setup.ReadEnv(p.Env)
	fileEnv := func(k string) string {
		if v, ok := vals[k]; ok && v != "" {
			return v
		}
		if services == nil {
			return getenv(k)
		}
		return ""
	}
	rep := ops.Diagnose(ops.Options{PolicyPath: p.Policy, Addr: *addr, Env: fileEnv, LookRuntime: look})
	if !*asJSON {
		_, _ = fmt.Fprintf(out, "\ndiagnose ready=%v\n", rep.Ready)
		for _, c := range rep.Checks {
			_, _ = fmt.Fprintf(out, "  %-12s %-14s %s\n", c.Name, c.Status, c.Detail)
		}
		if services == nil {
			_, _ = fmt.Fprintf(out, "\nstart: set -a; . %q; set +a; rusui -addr %q -db %q -policy %q\n", p.Env, *addr, p.DB, p.Policy)
		}
	}
	if !rep.Ready {
		return 1
	}
	return 0
}
