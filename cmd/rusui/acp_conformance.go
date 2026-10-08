package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/sannrox/rusui/internal/acp"
	"github.com/sannrox/rusui/internal/guest"
	"github.com/sannrox/rusui/internal/policy"
	"gopkg.in/yaml.v3"
)

// acpConformanceCLI qualifies one policy ACP entry and prints the descriptor
// digest to copy into policy. It uses a temporary workspace and no plane or
// GitHub credential.
func acpConformanceCLI(args []string) {
	fs := flag.NewFlagSet("acp-conformance", flag.ExitOnError)
	policyPath := fs.String("policy", "policy.yaml", "policy file")
	name := fs.String("guest", "", "ACP guest name")
	_ = fs.Parse(args)
	if *name == "" {
		fmt.Fprintln(os.Stderr, "acp-conformance: -guest required")
		os.Exit(2)
	}
	raw, err := os.ReadFile(*policyPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "acp-conformance:", err)
		os.Exit(1)
	}
	var file policy.File
	if err := yaml.Unmarshal(raw, &file); err != nil {
		fmt.Fprintln(os.Stderr, "acp-conformance:", err)
		os.Exit(1)
	}
	entry, ok := file.Guests[*name]
	if !ok || entry.Protocol != "acp" {
		fmt.Fprintln(os.Stderr, "acp-conformance: guest is not an ACP registry entry")
		os.Exit(1)
	}
	if err := acp.Probe(context.Background(), entry); err != nil {
		fmt.Fprintln(os.Stderr, "acp-conformance: probe:", err)
		os.Exit(1)
	}
	dir, err := os.MkdirTemp("", "rusui-acp-conformance-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "acp-conformance:", err)
		os.Exit(1)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if err := acp.Conform(ctx, entry, dir); err != nil {
		fmt.Fprintln(os.Stderr, "acp-conformance:", err)
		os.Exit(1)
	}
	fmt.Println(guest.ConformanceDigest(entry))
}
