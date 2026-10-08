package runner

import (
	"encoding/json"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/sannrox/rusui/internal/acp"
	"github.com/sannrox/rusui/internal/provider"
)

const (
	// transcriptTextCap bounds one recorded assistant text block.
	transcriptTextCap = 16 << 10
	// transcriptInputFieldCap bounds each recorded tool input field.
	transcriptInputFieldCap = 512
	redacted                = "[redacted]"
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

// transcriptRecorder gives every guest the same bounded, redacted transcript.
// Tool outcomes carry status only, never provider output or attachments.
type transcriptRecorder struct {
	rec         acp.Recorder
	secrets     []string
	mu          sync.Mutex
	message     strings.Builder
	messageKind string
	overflow    bool
}

func newTranscriptRecorder(a *Assignment, rec acp.Recorder) *transcriptRecorder {
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
	return &transcriptRecorder{rec: rec, secrets: secrets}
}

func (c *transcriptRecorder) redact(s string) string {
	for _, secret := range c.secrets {
		s = strings.ReplaceAll(s, secret, redacted)
	}
	s = tokenShapes.ReplaceAllString(s, redacted)
	return authHeader.ReplaceAllString(s, "${1}"+redacted)
}

func (c *transcriptRecorder) clean(s string, limit int) string {
	return truncateUTF8(c.redact(s), limit)
}

// Observe records one provider event. Permission and other kinds are
// recorded by their own paths and are ignored here.
func (c *transcriptRecorder) Observe(ev provider.Event) {
	if c == nil || c.rec == nil {
		return
	}
	var update map[string]any
	switch {
	case ev.Kind == "transcript":
		update = map[string]any{
			"sessionUpdate": "agent_message_chunk",
			"content":       map[string]any{"type": "text", "text": ev.Body},
		}
	case ev.Kind == "tool_call" && ev.Tool != nil && ev.Tool.Status == "pending":
		input := ev.Tool.Input
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
	_, _ = c.Record(acp.Receipt{Type: acp.ActionUpdate, Reason: acp.ReasonRecorded, Body: map[string]any{"update": update}})
}

// Record preserves authority receipt types; normalization affects recorded
// observations only. Permission decisions still consume the original request.
func (c *transcriptRecorder) Record(rec acp.Receipt) (string, error) {
	if c.rec == nil {
		return "", nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	switch rec.Type {
	case acp.ActionUpdate:
		raw, err := json.Marshal(rec.Body)
		if err != nil {
			return "", err
		}
		var params acp.SessionUpdateParams
		if err := json.Unmarshal(raw, &params); err != nil {
			return "", err
		}
		update := acp.NormalizeSessionUpdate(params.Update)
		if update == nil {
			return "", nil
		}
		kind, _ := update["sessionUpdate"].(string)
		if kind == "agent_message_chunk" || kind == "user_message_chunk" {
			if c.messageKind != "" && c.messageKind != kind {
				c.flushMessage()
			}
			c.messageKind = kind
			content, _ := update["content"].(map[string]any)
			text, _ := content["text"].(string)
			// Drop an oversized message whole: cutting an unredacted chunk can
			// retain a credential prefix whose remainder arrives in the next chunk.
			if c.message.Len()+len(text) > 2*transcriptTextCap {
				c.message.Reset()
				c.overflow = true
			}
			if !c.overflow {
				c.message.WriteString(text)
			}
			return "", nil
		}
		rec.Body = map[string]any{"update": c.cleanValue(update, 0)}
	}
	// Permission bodies are authority inputs used again by policy revalidation.
	// Keep their exact structure and bytes, separate from transcript summaries.
	c.flushMessage()
	return c.rec.Record(rec)
}

// Flush closes the final message even when a turn fails or is cancelled.
func (c *transcriptRecorder) Flush() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.flushMessage()
}

func (c *transcriptRecorder) flushMessage() {
	if c.messageKind == "" {
		return
	}
	text := "[message exceeds transcript limit]"
	if !c.overflow {
		text = c.clean(c.message.String(), transcriptTextCap)
	}
	_, _ = c.rec.Record(acp.Receipt{Type: acp.ActionUpdate, Reason: acp.ReasonRecorded, Body: map[string]any{"update": map[string]any{
		"sessionUpdate": c.messageKind,
		"content":       map[string]any{"type": "text", "text": text},
	}}})
	c.message.Reset()
	c.messageKind = ""
	c.overflow = false
}

func (c *transcriptRecorder) cleanValue(v any, depth int) any {
	if depth > 8 {
		return nil
	}
	switch x := v.(type) {
	case string:
		return c.clean(x, transcriptTextCap)
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, value := range x {
			key := c.clean(k, transcriptInputFieldCap)
			if k == "rawInput" {
				fields, _ := value.(map[string]any)
				input := make(map[string]any, len(fields))
				for name, field := range fields {
					text, _ := field.(string)
					input[c.clean(name, transcriptInputFieldCap)] = c.clean(text, transcriptInputFieldCap)
				}
				out[key] = input
			} else {
				out[key] = c.cleanValue(value, depth+1)
			}
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, value := range x {
			out[i] = c.cleanValue(value, depth+1)
		}
		return out
	default:
		return v
	}
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
