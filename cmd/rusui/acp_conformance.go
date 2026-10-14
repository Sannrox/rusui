package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/sannrox/rusui/internal/acp"
	"github.com/sannrox/rusui/internal/env"
	"github.com/sannrox/rusui/internal/guest"
	"github.com/sannrox/rusui/internal/policy"
	"gopkg.in/yaml.v3"
)

// acpConformanceCLI qualifies one policy ACP entry inside its guest image and
// prints the conformance digest to copy into policy. It uses a throwaway
// container and no plane or GitHub credential.
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
	if !ok || entry.Protocol != guest.ProtocolACP {
		fmt.Fprintln(os.Stderr, "acp-conformance: guest is not an ACP registry entry")
		os.Exit(1)
	}
	rt, err := env.LookRuntime()
	if err != nil {
		fmt.Fprintln(os.Stderr, "acp-conformance:", err)
		os.Exit(1)
	}
	docker, ok := rt.(env.DockerCLI)
	if !ok {
		fmt.Fprintln(os.Stderr, "acp-conformance: container runtime cannot exec guests")
		os.Exit(1)
	}
	if _, pinned := guest.ImageDigest(entry.Image); !pinned {
		// Turns run exactly the qualified content, so a tag is resolved
		// once here and the operator pins the entry to it.
		if id, err := docker.ImageID(entry.Image); err == nil {
			fmt.Fprintf(os.Stderr, "acp-conformance: pin the guest image by digest, for example image: %s\n", id)
		} else {
			fmt.Fprintln(os.Stderr, "acp-conformance: pin the guest image by digest")
		}
		os.Exit(1)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	digest, err := qualifyInImage(ctx, docker, entry)
	if err != nil {
		fmt.Fprintln(os.Stderr, "acp-conformance:", err)
		os.Exit(1)
	}
	fmt.Println(digest)
}

// guestRuntime is the container exec surface turns use for a guest.
type guestRuntime interface {
	env.Runtime
	env.StdioExecutor
	ExecOutput(id string, cmd []string) ([]byte, bool, error)
}

// qualifyInImage starts a throwaway container from entry.Image, runs the
// version probe and the ACP subset in it through the same exec path as a
// turn, and returns the conformance digest for the entry.
func qualifyInImage(ctx context.Context, rt guestRuntime, entry guest.Entry) (string, error) {
	digest, err := guest.ConformanceDigest(entry)
	if err != nil {
		return "", err
	}
	if len(entry.Argv) == 0 || len(entry.Probe) == 0 {
		return "", fmt.Errorf("guest requires argv and probe")
	}
	var suffix [4]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return "", err
	}
	container := env.Container{RT: rt, Image: entry.Image}
	handle, err := container.Create("acp-conformance-" + hex.EncodeToString(suffix[:]))
	if err != nil {
		return "", err
	}
	defer func() { _ = container.Destroy(handle) }()
	if _, _, err := rt.ExecOutput(handle, entry.Probe); err != nil {
		return "", fmt.Errorf("probe: %w", err)
	}
	stdin, stdout, stop, err := rt.ExecStdio(handle, entry.Argv, []string{"RUSUI_ACP_CONFORMANCE=1"})
	if err != nil {
		return "", err
	}
	defer stop()
	if err := acp.Conform(ctx, stdout, stdin, env.WorkspaceDir); err != nil {
		return "", err
	}
	return digest, nil
}
