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
	"time"

	"github.com/sannrox/rusui/internal/guest"
	"github.com/sannrox/rusui/internal/ops"
	"github.com/sannrox/rusui/internal/policy"
	"gopkg.in/yaml.v3"
)

func acpConformanceCLI(args []string) {
	os.Exit(acpConformanceMain(args, os.Stdout, os.Stderr))
}

// acpConformanceMain asks the plane to qualify one policy ACP entry inside
// its pinned guest image and prints the signed receipt to paste beside the
// entry (ADR 0082). The plane runs the suite and holds the signing key.
func acpConformanceMain(args []string, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("acp-conformance", flag.ContinueOnError)
	fs.SetOutput(errOut)
	policyPath := fs.String("policy", "policy.yaml", "policy file")
	name := fs.String("guest", "", "ACP guest name")
	url := fs.String("url", ops.PlaneBaseURL("127.0.0.1:8080", os.Getenv), "plane URL")
	token := fs.String("token", os.Getenv("RUSUI_WORKER_SECRET"), "worker token")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *name == "" {
		_, _ = fmt.Fprintln(errOut, "acp-conformance: -guest required")
		return 2
	}
	// The entry has no receipt yet, so the file is decoded without policy
	// validation, which would refuse it.
	raw, err := os.ReadFile(*policyPath)
	if err != nil {
		_, _ = fmt.Fprintln(errOut, "acp-conformance:", err)
		return 1
	}
	var file policy.File
	if err := yaml.Unmarshal(raw, &file); err != nil {
		_, _ = fmt.Fprintln(errOut, "acp-conformance:", err)
		return 1
	}
	entry, ok := file.Guests[*name]
	if !ok || entry.Protocol != guest.ProtocolACP {
		_, _ = fmt.Fprintln(errOut, "acp-conformance: guest is not an ACP registry entry")
		return 1
	}
	if _, builtin := guest.Builtin()[*name]; builtin {
		_, _ = fmt.Fprintln(errOut, "acp-conformance: built-in guests keep their pins")
		return 1
	}
	entry.Receipt = nil
	receipt, err := requestReceipt(*url, *token, entry)
	if err != nil {
		_, _ = fmt.Fprintln(errOut, "acp-conformance:", err)
		return 1
	}
	b, err := yaml.Marshal(struct {
		Receipt guest.Receipt `yaml:"receipt"`
	}{receipt})
	if err != nil {
		_, _ = fmt.Fprintln(errOut, "acp-conformance:", err)
		return 1
	}
	_, _ = out.Write(b)
	return 0
}

// requestReceipt posts entry to the plane's conformance endpoint.
func requestReceipt(base, token string, entry guest.Entry) (guest.Receipt, error) {
	body, err := json.Marshal(entry)
	if err != nil {
		return guest.Receipt{}, err
	}
	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(base, "/")+"/guests/conformance", bytes.NewReader(body))
	if err != nil {
		return guest.Receipt{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	shared, err := planeHTTP(base)
	if err != nil {
		return guest.Receipt{}, err
	}
	// planeHTTP may return the shared default client; bound a copy.
	client := *shared
	client.Timeout = 2 * time.Minute
	res, err := client.Do(req)
	if err != nil {
		return guest.Receipt{}, err
	}
	defer func() { _ = res.Body.Close() }()
	b, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
	if res.StatusCode != http.StatusOK {
		return guest.Receipt{}, fmt.Errorf("plane: %s: %s", res.Status, strings.TrimSpace(string(b)))
	}
	var receipt guest.Receipt
	if err := json.Unmarshal(b, &receipt); err != nil {
		return guest.Receipt{}, err
	}
	return receipt, nil
}
