// Package provider is the one Go boundary for Grok, Claude, and Codex.
// Plane core speaks the events in this package. Each adapter speaks only
// its pinned provider protocol. An unknown protocol version is refused
// before a turn starts.
package provider

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	KindGrok   = "grok"
	KindClaude = "claude"
	KindCodex  = "codex"

	// Pinned protocol versions. A guest that answers with another version
	// is unsupported.
	GrokACPVersion      = 1
	ClaudeCodeVersion   = "2.1.283"
	ClaudeStreamProto   = "stream-json"
	CodexAppServerProto = "app-server-2026-04-15"
)

// Argv is the guest process for a provider. It is that provider's own CLI.
func Argv(kind string) ([]string, error) {
	switch kind {
	case "", KindGrok:
		return []string{"agent", "--permission-mode", "default", "agent", "stdio"}, nil
	case KindClaude:
		// stdio delegates every permission prompt to rusui as a
		// can_use_tool control request; without it Claude denies silently.
		// The Bash ask rule makes every shell command such a prompt,
		// including those Claude considers read-only (#442).
		return []string{"claude", "--print", "--input-format", ClaudeStreamProto, "--output-format", ClaudeStreamProto, "--verbose", "--permission-mode", "default", "--permission-prompt-tool", "stdio", "--settings", `{"permissions":{"ask":["Bash"]}}`}, nil
	case KindCodex:
		return []string{"codex", "app-server", "--listen", "stdio://"}, nil
	default:
		return nil, fmt.Errorf("provider: unknown guest %q", kind)
	}
}

// ProbeArgv is a version check. It does not start a session.
func ProbeArgv(kind string) ([]string, error) {
	switch kind {
	case "", KindGrok:
		return []string{"agent", "--version"}, nil
	case KindClaude:
		return []string{"claude", "--version"}, nil
	case KindCodex:
		return []string{"codex", "--version"}, nil
	default:
		return nil, fmt.Errorf("provider: unknown guest %q", kind)
	}
}

// PinnedVersion is the version string a probe must print.
func PinnedVersion(kind string) (string, error) {
	switch kind {
	case "", KindGrok:
		return fmt.Sprintf("acp:%d", GrokACPVersion), nil
	case KindClaude:
		return ClaudeCodeVersion, nil
	case KindCodex:
		return CodexAppServerProto, nil
	default:
		return "", fmt.Errorf("provider: unknown guest %q", kind)
	}
}

// CanRewind is false for every pinned provider. Revert is refused before
// any file change. A workspace checkpoint is not conversation rewind.
func CanRewind(string) bool { return false }

var ErrNoRewind = errors.New("provider cannot rewind the conversation")

// Revert refuses before touching the workspace.
func Revert(kind, workspace string) error {
	if CanRewind(kind) {
		return fmt.Errorf("provider: rewind is not implemented")
	}
	if workspace != "" {
		if _, err := os.Stat(workspace); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return ErrNoRewind
}

// Event is the vocabulary the CLI, console, and editor already store.
type Event struct {
	Kind         string // transcript, tool_call, permission, question, usage_limit, attachment
	Body         string
	OptionID     string
	Reset        string
	ResetMissing bool
	Tool         *ToolCall // set on a parsed tool_call
}

// ToolCall is one tool call or its outcome in ACP terms. A call carries
// its name, ACP kind, and a bounded input summary with status "pending";
// its outcome carries only the id and "completed" or "failed".
type ToolCall struct {
	ID     string
	Name   string
	Kind   string
	Input  map[string]string
	Status string
}

// Turn is one provider turn against a Rusui session cursor.
type Turn struct {
	Prompt      string
	Cursor      string
	Workspace   string
	Attachments []string
	// Observe, when set, receives each event as it is read, before the
	// turn ends, so a follower sees it live.
	Observe func(Event)
}

// Result is the cursor to store for the next process and the events from
// this process only. Resume must not replay events already stored.
type Result struct {
	Cursor string
	Events []Event
}

// Option is a provider option id. The id is sent back unchanged.
type Option struct {
	ID string `json:"id"`
}

// Decide answers a permission or question. raw is the provider message.
// allow is ignored when the provider omitted a reject id: the adapter still refuses.
type Decide func(options []Option, raw json.RawMessage) (optionID string, allow bool)

// Instance is one account and one configuration of one provider kind.
type Instance struct {
	Kind string
	ID   string
	Dir  string
}

const copiedHomeMarker = ".rusui-copied-from"

// NewInstance creates a plane-owned directory for one account. A directory
// marked as copied from another home is refused.
func NewInstance(kind, id, root string) (Instance, error) {
	if kind != KindGrok && kind != KindClaude && kind != KindCodex {
		return Instance{}, fmt.Errorf("provider: unknown guest %q", kind)
	}
	if id == "" || strings.Contains(id, "..") || strings.ContainsAny(id, `/\`) {
		return Instance{}, fmt.Errorf("provider: instance id")
	}
	dir := filepath.Join(root, kind, id)
	if _, err := os.Stat(filepath.Join(dir, copiedHomeMarker)); err == nil {
		return Instance{}, fmt.Errorf("provider: copied home refused")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Instance{}, err
	}
	return Instance{Kind: kind, ID: id, Dir: dir}, nil
}

// LaunchEnv is the guest environment for an instance. A HOME that points
// anywhere else is a misconfiguration. Ambient credentials and another
// instance's config path are removed.
func LaunchEnv(inst Instance, base []string) ([]string, error) {
	if _, err := os.Stat(filepath.Join(inst.Dir, copiedHomeMarker)); err == nil {
		return nil, fmt.Errorf("provider: copied home refused")
	}
	drop := map[string]bool{
		"HOME": true, "ANTHROPIC_API_KEY": true, "OPENAI_API_KEY": true,
		"XAI_API_KEY": true, "CLAUDE_CONFIG_DIR": true, "CODEX_HOME": true,
		"RUSUI_PROVIDER_INSTANCE": true,
	}
	var out []string
	for _, entry := range base {
		key, _, ok := strings.Cut(entry, "=")
		if !ok || drop[key] {
			if key == "HOME" {
				value := strings.TrimPrefix(entry, "HOME=")
				if value != "" && value != inst.Dir {
					return nil, fmt.Errorf("provider: HOME must not select another account")
				}
			}
			continue
		}
		out = append(out, entry)
	}
	switch inst.Kind {
	case KindClaude:
		out = append(out, "CLAUDE_CONFIG_DIR="+inst.Dir)
	case KindCodex:
		out = append(out, "CODEX_HOME="+inst.Dir)
	default:
		out = append(out, "RUSUI_PROVIDER_INSTANCE="+inst.Dir)
	}
	return out, nil
}

// AttachmentOutside reports whether path is outside the workspace. A path
// inside the workspace is rejected so an upload is not copied in to skip
// approval.
func AttachmentOutside(workspace, path string) error {
	if path == "" {
		return fmt.Errorf("provider: attachment path required")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	root, err := filepath.Abs(workspace)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return err
	}
	if rel == "." || (!strings.HasPrefix(rel, "..") && rel != "..") {
		return fmt.Errorf("provider: attachment must stay outside the workspace")
	}
	return nil
}

// Run speaks the pinned protocol on rw and returns this process's events.
// decide is used for permission requests. A missing reject option forces a deny.
func Run(ctx context.Context, kind string, inst Instance, rw io.ReadWriteCloser, turn Turn, decide Decide) (Result, error) {
	if ctx.Err() != nil {
		return Result{}, ctx.Err()
	}
	for _, path := range turn.Attachments {
		if err := AttachmentOutside(turn.Workspace, path); err != nil {
			return Result{}, err
		}
	}
	switch kind {
	case "", KindGrok:
		return runGrok(rw, turn, decide)
	case KindClaude:
		return runClaude(rw, turn, decide)
	case KindCodex:
		return runCodex(rw, inst, turn, decide)
	default:
		return Result{}, fmt.Errorf("provider: unknown guest %q", kind)
	}
}

func rejectMissing(options []Option, decide Decide, raw json.RawMessage) (string, bool) {
	hasReject := false
	for _, opt := range options {
		id := strings.ToLower(opt.ID)
		if strings.Contains(id, "deny") || strings.Contains(id, "reject") || id == "cancel" {
			hasReject = true
			break
		}
	}
	optionID, allow := "", false
	if decide != nil {
		optionID, allow = decide(options, raw)
	}
	if !hasReject {
		return "", false
	}
	if allow {
		return optionID, true
	}
	return optionID, false
}

func writeJSON(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = w.Write(append(b, '\n'))
	return err
}

func readJSON(r *bufio.Reader, v any) error {
	line, err := r.ReadBytes('\n')
	if err != nil {
		return err
	}
	return json.Unmarshal(line, v)
}

// Question is an async provider question stored with the session. It
// outlives the provider process. Dismiss closes it and sends nothing.
type Question struct {
	ID        string
	Body      string
	Answer    string
	Dismissed bool
}

// Questions is the durable record of async questions.
type Questions struct {
	items []Question
}

func (q *Questions) Add(item Question) { q.items = append(q.items, item) }

func (q *Questions) List() []Question {
	out := make([]Question, len(q.items))
	copy(out, q.items)
	return out
}

func (q *Questions) Dismiss(id string) {
	for i := range q.items {
		if q.items[i].ID == id {
			q.items[i].Dismissed = true
		}
	}
}

func (q *Questions) Answer(id, text string) {
	for i := range q.items {
		if q.items[i].ID == id {
			q.items[i].Answer = text
		}
	}
}
