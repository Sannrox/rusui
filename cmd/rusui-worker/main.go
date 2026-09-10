package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

var (
	Version      = "dev"
	GitCommit    = "unknown"
	BuildTime    = "unknown"
	GitMajor     = ""
	GitMinor     = ""
	PlatformName = ""
	BuildArch    = ""
	BuildOs      = ""
)

func main() {
	base := flag.String("url", "http://127.0.0.1:8080", "server")
	repo := flag.String("repo", "", "repo")
	token := flag.String("token", os.Getenv("RUSUI_WORKER_SECRET"), "worker token")
	codex := flag.String("codex", "codex", "cli")
	printVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()
	if *printVersion {
		fmt.Printf("rusui-worker %s commit=%s time=%s\n", Version, GitCommit, BuildTime)
		return
	}
	if *repo == "" {
		logf("need -repo")
		os.Exit(1)
	}
	for {
		c, err := claim(*base, *token, *repo)
		if err != nil {
			logf("claim: %v", err)
			time.Sleep(time.Second)
			continue
		}
		if c == nil {
			time.Sleep(2 * time.Second)
			continue
		}
		dir, _ := os.MkdirTemp("", "rusui-job-*")
		in := filepath.Join(dir, "input.v1.json")
		outp := filepath.Join(dir, "output.v1.json")
		b, _ := json.MarshalIndent(c.Input, "", "  ")
		_ = os.WriteFile(in, b, 0o600)
		cmd := exec.Command(*codex, "exec", "--output", outp, in)
		cmd.Dir = dir
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + dir}
		if err := cmd.Run(); err != nil {
			_ = fail(*base, *token, c)
			continue
		}
		raw, err := os.ReadFile(outp)
		if err != nil {
			_ = fail(*base, *token, c)
			continue
		}
		_ = complete(*base, *token, c, raw)
	}
}

type claimResp struct {
	JobID           int64          `json:"job_id"`
	LeaseGeneration int            `json:"lease_generation"`
	ClaimedRevision int            `json:"claimed_revision"`
	Input           map[string]any `json:"input"`
}

func claim(base, token, repo string) (*claimResp, error) {
	body, _ := json.Marshal(map[string]string{"repo": repo})
	req, _ := http.NewRequest("POST", base+"/jobs/claim", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode == 204 {
		return nil, nil
	}
	if res.StatusCode != 200 {
		b, _ := io.ReadAll(res.Body)
		return nil, fmt.Errorf("%s %s", res.Status, b)
	}
	var c claimResp
	json.NewDecoder(res.Body).Decode(&c)
	return &c, nil
}

func complete(base, token string, c *claimResp, artifact []byte) error {
	var art any
	json.Unmarshal(artifact, &art)
	body, _ := json.Marshal(map[string]any{
		"lease_generation": c.LeaseGeneration,
		"claimed_revision": c.ClaimedRevision,
		"artifact":         art,
	})
	req, _ := http.NewRequest("POST", fmt.Sprintf("%s/jobs/%d/complete", base, c.JobID), bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	res.Body.Close()
	return nil
}

func fail(base, token string, c *claimResp) error {
	body, _ := json.Marshal(map[string]any{
		"lease_generation": c.LeaseGeneration,
		"claimed_revision": c.ClaimedRevision,
	})
	req, _ := http.NewRequest("POST", fmt.Sprintf("%s/jobs/%d/fail", base, c.JobID), bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	res.Body.Close()
	return nil
}

func logf(f string, a ...any) { fmt.Fprintf(os.Stderr, f+"\n", a...) }
