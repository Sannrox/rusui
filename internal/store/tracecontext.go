package store

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"strings"
)

// TraceContext is the W3C trace the plane stored when it admitted a turn.
// ParentID is the caller's span when the inbound header was valid; empty
// when this turn started the trace. SpanID is this turn's span, minted at
// admit so export and the guest see the same ids after restart.
type TraceContext struct {
	TraceID  string `json:"trace_id"`
	ParentID string `json:"parent_id,omitempty"`
	SpanID   string `json:"span_id"`
	Flags    string `json:"flags"`
	State    string `json:"tracestate,omitempty"`
}

// ParseTraceHeaders reads inbound W3C headers. A missing or unusable
// traceparent yields a zero value; admission then mints a fresh trace.
// The values are never used for identity, tenancy, or policy.
func ParseTraceHeaders(traceparent, tracestate string) TraceContext {
	tc, ok := parseTraceparent(traceparent)
	if !ok {
		return TraceContext{}
	}
	tc.State = cleanTracestate(tracestate)
	return tc
}

// MintTrace returns the durable context for a newly admitted turn. A valid
// inbound parent keeps that trace id; otherwise this turn starts a trace.
func MintTrace(inbound TraceContext) TraceContext {
	span := traceHex(8)
	if inbound.TraceID != "" && inbound.ParentID != "" {
		flags := inbound.Flags
		if flags == "" {
			flags = "00"
		}
		return TraceContext{
			TraceID:  inbound.TraceID,
			ParentID: inbound.ParentID,
			SpanID:   span,
			Flags:    flags,
			State:    inbound.State,
		}
	}
	return TraceContext{TraceID: traceHex(16), SpanID: span, Flags: "00"}
}

func (t TraceContext) empty() bool {
	return t.TraceID == "" || t.SpanID == ""
}

// GuestHeader is the traceparent the guest should parent its spans under:
// this turn's trace id and span id.
func (t TraceContext) GuestHeader() string {
	if t.empty() {
		return ""
	}
	flags := t.Flags
	if flags == "" {
		flags = "00"
	}
	return "00-" + t.TraceID + "-" + t.SpanID + "-" + flags
}

func encodeTraceContext(t TraceContext) (string, error) {
	if t.empty() {
		return "", nil
	}
	b, err := json.Marshal(t)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func decodeTraceContext(raw string) TraceContext {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return TraceContext{}
	}
	var t TraceContext
	if json.Unmarshal([]byte(raw), &t) != nil || t.empty() {
		return TraceContext{}
	}
	return t
}

func parseTraceparent(h string) (TraceContext, bool) {
	h = strings.ToLower(strings.TrimSpace(h))
	parts := strings.Split(h, "-")
	if len(parts) < 4 {
		return TraceContext{}, false
	}
	version, traceID, parentID, flags := parts[0], parts[1], parts[2], parts[3]
	if len(version) != 2 || version == "ff" || !isHex(version) {
		return TraceContext{}, false
	}
	if len(traceID) != 32 || !isHex(traceID) || allZero(traceID) {
		return TraceContext{}, false
	}
	if len(parentID) != 16 || !isHex(parentID) || allZero(parentID) {
		return TraceContext{}, false
	}
	if len(flags) != 2 || !isHex(flags) {
		return TraceContext{}, false
	}
	return TraceContext{TraceID: traceID, ParentID: parentID, Flags: flags}, true
}

func cleanTracestate(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > 1024 {
		return ""
	}
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] > 0x7e {
			return ""
		}
	}
	return s
}

func isHex(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func allZero(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] != '0' {
			return false
		}
	}
	return true
}

func traceHex(n int) string {
	b := make([]byte, n)
	for {
		if _, err := rand.Read(b); err != nil {
			// crypto/rand failure is treated as a fresh all-nonzero
			// fallback so admission never fails on tracing.
			for i := range b {
				b[i] = byte(i + 1)
			}
			break
		}
		if !allZero(hex.EncodeToString(b)) {
			break
		}
	}
	return hex.EncodeToString(b)
}
