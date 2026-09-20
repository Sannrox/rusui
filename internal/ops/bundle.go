package ops

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// ArtifactIdentity is the verifiable package identity for one upgrade.
type ArtifactIdentity struct {
	Binary     string `json:"binary"`
	Commit     string `json:"commit"`
	Schema     int    `json:"schema"`
	Topology   string `json:"topology"`
	GuestImage string `json:"guest_image,omitempty"`
}

type Bundle struct {
	Identity   ArtifactIdentity `json:"identity"`
	Diagnose   Report           `json:"diagnose"`
	Drain      json.RawMessage  `json:"drain,omitempty"`
	Exclusions []string         `json:"exclusions"`
}

func Redact(s string, secrets []string) string {
	out := s
	for _, sec := range secrets {
		if sec == "" || len(sec) < 6 {
			continue
		}
		out = strings.ReplaceAll(out, sec, "[redacted]")
	}
	return out
}

func SecretValues(getenv func(string) string) []string {
	if getenv == nil {
		getenv = os.Getenv
	}
	keys := []string{
		"RUSUI_WORKER_SECRET", "RUSUI_WEBHOOK_SECRET", "RUSUI_SLACK_SECRET",
		"RUSUI_GITHUB_TOKEN", "GITHUB_TOKEN", "XAI_API_KEY", "RUSUI_XAI_API_KEY",
		"RUSUI_SLACK_BOT_TOKEN",
	}
	var out []string
	for _, k := range keys {
		if v := getenv(k); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func WriteBundle(dir string, b Bundle, secrets []string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return err
	}
	raw = []byte(Redact(string(raw), secrets))
	if err := os.WriteFile(filepath.Join(dir, "diagnostics.json"), raw, 0o600); err != nil {
		return err
	}
	excl := strings.Join(append([]string{
		"environment secret values",
		"guest workspace dirt",
		"GitHub and model provider tokens",
		"SQLite WAL contents copied separately",
	}, b.Exclusions...), "\n")
	return os.WriteFile(filepath.Join(dir, "exclusions.txt"), []byte(Redact(excl+"\n", secrets)), 0o600)
}
