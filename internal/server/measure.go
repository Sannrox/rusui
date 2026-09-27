package server

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/sannrox/rusui/internal/store"
)

func (s *Server) projectMeasurements(w http.ResponseWriter, r *http.Request) {
	if !s.OperatorBrowserOK(r) {
		http.Error(w, "auth", http.StatusUnauthorized)
		return
	}
	agg, err := store.ProjectAggregates(s.Eng.Store, r.PathValue("slug"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(agg)
}

func (s *Server) noteClaimedProvider(turnID int64) {
	_ = store.NoteProvider(s.Eng.Store, turnID, s.guest(), s.GuestVersion)
}

func (s *Server) exportMeasurement(turnID int64) {
	if s.OTelEndpoint == "" {
		return
	}
	m, ok, err := store.ClaimMeasurementExport(s.Eng.Store, turnID)
	if err != nil || !ok || m == nil {
		return
	}
	base := strings.TrimRight(s.OTelEndpoint, "/")
	now := time.Now().UTC()
	s.postOTLP(base+"/v1/traces", tracePayload(*m, now))
	s.postOTLP(base+"/v1/metrics", metricPayload(now))
}

func (s *Server) postOTLP(url string, payload any) {
	body, err := json.Marshal(payload)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return
	}
	_ = res.Body.Close()
}

func tracePayload(m store.Measurement, now time.Time) map[string]any {
	attrs := []map[string]any{
		otlpString("terminal_state", m.TerminalState),
		otlpString("provider", m.Provider),
		otlpString("provider_version", m.ProviderVersion),
		otlpString("resume", m.Resume),
		otlpString("permission_decision", m.PermissionDecision),
		otlpString("publication", m.Publication),
		otlpInt("turn_id", m.TurnID),
		otlpInt("session_id", m.SessionID),
	}
	if m.DurationMS != nil {
		attrs = append(attrs, otlpInt("duration_ms", *m.DurationMS))
	}
	start := now
	if m.DurationMS != nil {
		start = now.Add(-time.Duration(*m.DurationMS) * time.Millisecond)
	}
	return map[string]any{"resourceSpans": []any{map[string]any{
		"scopeSpans": []any{map[string]any{"spans": []any{map[string]any{
			"traceId":           randomHex(16),
			"spanId":            randomHex(8),
			"name":              "turn",
			"startTimeUnixNano": strconv.FormatInt(start.UnixNano(), 10),
			"endTimeUnixNano":   strconv.FormatInt(now.UnixNano(), 10),
			"attributes":        attrs,
		}}}},
	}}}
}

func metricPayload(now time.Time) map[string]any {
	nano := strconv.FormatInt(now.UnixNano(), 10)
	return map[string]any{"resourceMetrics": []any{map[string]any{
		"scopeMetrics": []any{map[string]any{"metrics": []any{map[string]any{
			"name": "rusui.turns",
			"sum": map[string]any{
				"aggregationTemporality": 1,
				"isMonotonic":            true,
				"dataPoints": []any{map[string]any{
					"asInt":             "1",
					"startTimeUnixNano": nano,
					"timeUnixNano":      nano,
				}},
			},
		}}}},
	}}}
}

func otlpString(key, value string) map[string]any {
	return map[string]any{"key": key, "value": map[string]any{"stringValue": value}}
}

func otlpInt(key string, value int64) map[string]any {
	return map[string]any{"key": key, "value": map[string]any{"intValue": strconv.FormatInt(value, 10)}}
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
