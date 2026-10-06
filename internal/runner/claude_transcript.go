package runner

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/sannrox/rusui/internal/acp"
	"github.com/sannrox/rusui/internal/provider"
)

const (
	// claudeTextCap bounds one recorded assistant text block.
	claudeTextCap = 16 << 10
	// claudeInputFieldCap bounds each recorded tool input field.
	claudeInputFieldCap = 512
	redacted            = "[redacted]"
	// minSecretLen keeps short values such as "1" from being redacted
	// everywhere they happen to appear.
	minSecretLen = 8
)

// tokenShapes are credentials recognisable without knowing their value.
var tokenShapes = regexp.MustCompile(`\b(?:gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}|sk-[A-Za-z0-9_-]{20,}|xai-[A-Za-z0-9_-]{20,}|xox[abpr]-[A-Za-z0-9-]{10,})`)

// authHeader is the credential after an Authorization scheme.
var authHeader = regexp.MustCompile(`(?i)\b(bearer\s+|basic\s+|authorization:\s*token\s+)[A-Za-z0-9._~+/=-]{16,}`)

// secretKey names environment variables whose values are credentials.
var secretKey = regexp.MustCompile(`(?i)(TOKEN|SECRET|PASSWORD|PASSWD|API_?KEY|PRIVATE_?KEY|CREDENTIAL)`)

// claudeRecorder records a Claude turn's assistant text and tool calls as
// acp.session.update rows, in the update shapes an ACP guest sends
// (agent_message_chunk, tool_call, tool_call_update), so read and follow
// render them like any guest's. Every recorded string is redacted, then
// bounded; a tool result records only its status.
type claudeRecorder struct {
	rec     acp.Recorder
	secrets []string
}

func newClaudeRecorder(a *Assignment, rec acp.Recorder) *claudeRecorder {
	seen := map[string]bool{}
	var secrets []string
	add := func(v string) {
		if len(v) >= minSecretLen && !seen[v] {
			seen[v] = true
			secrets = append(secrets, v)
		}
	}
	if a != nil {
		add(a.TurnToken)
		add(a.GitHubToken)
	}
	for _, entry := range os.Environ() {
		if key, value, ok := strings.Cut(entry, "="); ok && secretKey.MatchString(key) {
			add(value)
		}
	}
	// Longest first, so a secret that contains another is removed whole.
	sort.Slice(secrets, func(i, j int) bool { return len(secrets[i]) > len(secrets[j]) })
	return &claudeRecorder{rec: rec, secrets: secrets}
}

func (c *claudeRecorder) redact(s string) string {
	for _, secret := range c.secrets {
		s = strings.ReplaceAll(s, secret, redacted)
	}
	s = tokenShapes.ReplaceAllString(s, redacted)
	return authHeader.ReplaceAllString(s, "${1}"+redacted)
}

func (c *claudeRecorder) clean(s string, limit int) string {
	return truncateUTF8(c.redact(s), limit)
}

// Observe records one provider event. Permission and other kinds are
// recorded by their own paths and are ignored here.
func (c *claudeRecorder) Observe(ev provider.Event) {
	if c == nil || c.rec == nil {
		return
	}
	var update map[string]any
	switch {
	case ev.Kind == "transcript":
		update = map[string]any{
			"sessionUpdate": "agent_message_chunk",
			"content":       map[string]any{"type": "text", "text": c.clean(ev.Body, claudeTextCap)},
		}
	case ev.Kind == "tool_call" && ev.Tool != nil && ev.Tool.Status == "pending":
		input := map[string]string{}
		for k, v := range ev.Tool.Input {
			input[k] = c.clean(v, claudeInputFieldCap)
		}
		update = map[string]any{
			"sessionUpdate": "tool_call",
			"toolCallId":    ev.Tool.ID,
			"title":         ev.Tool.Name,
			"kind":          ev.Tool.Kind,
			"status":        ev.Tool.Status,
			"rawInput":      input,
		}
	case ev.Kind == "tool_call" && ev.Tool != nil:
		update = map[string]any{
			"sessionUpdate": "tool_call_update",
			"toolCallId":    ev.Tool.ID,
			"status":        ev.Tool.Status,
		}
	default:
		return
	}
	_, _ = c.rec.Record(acp.Receipt{Type: acp.ActionUpdate, Reason: acp.ReasonRecorded, Body: map[string]any{"update": update}})
}

// truncateUTF8 cuts s to at most n bytes on a rune boundary and marks the cut.
func truncateUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}
