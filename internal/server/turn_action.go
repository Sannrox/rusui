package server

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/sannrox/rusui/internal/acp"
	"github.com/sannrox/rusui/internal/engine"
)

const (
	turnActionTextCap   = 16 << 10
	turnActionFieldCap  = 512
	turnActionRedacted  = "[redacted]"
	turnActionMinSecret = 8
)

var (
	turnActionTokenShapes = regexp.MustCompile(`\b(?:gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}|sk-[A-Za-z0-9_-]{20,}|xai-[A-Za-z0-9_-]{20,}|xox[abpr]-[A-Za-z0-9-]{10,})`)
	turnActionAuthHeader  = regexp.MustCompile(`(?i)\b(bearer\s+|basic\s+|authorization:\s*token\s+)[A-Za-z0-9._~+/=-]{16,}`)
)

func sanitizeTurnActionBody(typ string, body any, secrets []string) (any, error) {
	if !engine.AllowedTurnActionType(typ) {
		return nil, fmt.Errorf("%w: %s", engine.ErrTurnActionType, typ)
	}
	if typ != acp.ActionUpdate {
		return body, nil
	}
	update := sessionUpdateMap(body)
	if update == nil {
		return nil, fmt.Errorf("action: session update required")
	}
	norm := acp.NormalizeSessionUpdate(update)
	if norm == nil {
		return nil, fmt.Errorf("action: session update not recordable")
	}
	return map[string]any{"update": cleanTurnActionValue(norm, secrets, 0)}, nil
}

func sessionUpdateMap(body any) map[string]any {
	m, ok := body.(map[string]any)
	if !ok {
		return nil
	}
	if update, ok := m["update"].(map[string]any); ok {
		return update
	}
	if _, ok := m["sessionUpdate"]; ok {
		return m
	}
	return nil
}

func cleanTurnActionValue(v any, secrets []string, depth int) any {
	if depth > 8 {
		return nil
	}
	switch x := v.(type) {
	case string:
		return truncateUTF8(redactTurnAction(x, secrets), turnActionTextCap)
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, value := range x {
			key := truncateUTF8(redactTurnAction(k, secrets), turnActionFieldCap)
			if k == "rawInput" {
				fields, _ := value.(map[string]any)
				input := make(map[string]any, len(fields))
				for name, field := range fields {
					text, _ := field.(string)
					input[truncateUTF8(redactTurnAction(name, secrets), turnActionFieldCap)] = truncateUTF8(redactTurnAction(text, secrets), turnActionFieldCap)
				}
				out[key] = input
			} else {
				out[key] = cleanTurnActionValue(value, secrets, depth+1)
			}
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, value := range x {
			out[i] = cleanTurnActionValue(value, secrets, depth+1)
		}
		return out
	default:
		return v
	}
}

func redactTurnAction(s string, secrets []string) string {
	for _, secret := range secrets {
		if len(secret) >= turnActionMinSecret {
			s = strings.ReplaceAll(s, secret, turnActionRedacted)
		}
	}
	s = turnActionTokenShapes.ReplaceAllString(s, turnActionRedacted)
	return turnActionAuthHeader.ReplaceAllString(s, "${1}"+turnActionRedacted)
}

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
