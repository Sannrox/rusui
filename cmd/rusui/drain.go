package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"os"
	"strings"

	"github.com/sannrox/rusui/internal/engine"
	"github.com/sannrox/rusui/internal/ops"
	"github.com/sannrox/rusui/internal/store"
)

func drainCLI(args []string) {
	os.Exit(drainMain(args, os.Stdout))
}

func drainMain(args []string, out io.Writer) int {
	fs := flag.NewFlagSet("drain", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	url := fs.String("url", ops.PlaneBaseURL("127.0.0.1:8080", os.Getenv), "plane URL")
	token := fs.String("token", os.Getenv("RUSUI_WORKER_SECRET"), "worker token")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	return postWorker("drain", *url, "/drain", *token, out)
}

func resumeCLI(args []string) {
	os.Exit(resumeMain(args, os.Stdout))
}

// resumeMain lifts the pause that drain set, globally or for -project.
func resumeMain(args []string, out io.Writer) int {
	fs := flag.NewFlagSet("resume", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	url := fs.String("url", ops.PlaneBaseURL("127.0.0.1:8080", os.Getenv), "plane URL")
	token := fs.String("token", os.Getenv("RUSUI_WORKER_SECRET"), "worker token")
	project := fs.String("project", "", "resume only this project (default: global)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	path := "/resume"
	if *project != "" {
		path += "?" + neturl.Values{"project": {*project}}.Encode()
	}
	return postWorker("resume", *url, path, *token, out)
}

// postWorker POSTs to a worker-authenticated plane endpoint and copies the
// JSON response to out.
func postWorker(name, base, path, token string, out io.Writer) int {
	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(base, "/")+path, nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	client, err := planeHTTP(base)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	res, err := client.Do(req)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer func() { _ = res.Body.Close() }()
	b, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 300 {
		fmt.Fprintf(os.Stderr, "%s: %s %s\n", name, res.Status, b)
		return 1
	}
	_, _ = out.Write(b)
	if len(b) == 0 || b[len(b)-1] != '\n' {
		_, _ = fmt.Fprintln(out)
	}
	return 0
}

func restoreCLI(args []string) {
	os.Exit(restoreMain(args, os.Stdout))
}

// restoreMain runs store.Restore on a copied database with the plane
// stopped and prints the inventory. Start the plane on -db afterwards.
func restoreMain(args []string, out io.Writer) int {
	fs := flag.NewFlagSet("restore", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	db := fs.String("db", "", "copied sqlite database to restore (required)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *db == "" {
		fmt.Fprintln(os.Stderr, "restore: -db is required")
		return 2
	}
	st, rep, err := store.Restore(*db)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if err := st.Close(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	b, err := json.Marshal(rep)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	_, _ = fmt.Fprintln(out, string(b))
	return 0
}

func diagnosticsCLI(args []string) {
	fs := flag.NewFlagSet("diagnostics", flag.ExitOnError)
	db := fs.String("db", "rusui.db", "sqlite path")
	pol := fs.String("policy", "policy.yaml", "policy file")
	addr := fs.String("addr", "127.0.0.1:8080", "listen address to check")
	url := fs.String("url", "", "plane base URL")
	out := fs.String("out", "rusui-diagnostics", "output directory")
	_ = fs.Parse(args)
	schema, paused, live, err := ops.InspectLive(*db)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	rep := ops.Diagnose(ops.Options{PolicyPath: *pol, Addr: *addr, PlaneURL: *url})
	drainRaw, err := json.Marshal(engine.DrainReport{Paused: paused, Live: live})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	b := ops.Bundle{
		Identity: ops.ArtifactIdentity{
			Binary: "rusui", Commit: GitCommit, Schema: schema, Topology: ops.Topology,
			GuestImage: os.Getenv("RUSUI_GUEST_IMAGE"),
		},
		Diagnose: rep,
		Drain:    drainRaw,
		Exclusions: []string{
			"live provider credentials",
			"guest filesystem",
		},
	}
	if err := ops.WriteBundle(*out, b, ops.SecretValues(os.Getenv)); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(*out)
}
