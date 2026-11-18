package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/sannrox/rusui/internal/store"
)

const (
	modelSummaryTurns     = 256
	modelSummaryCalls     = 64
	modelSummaryPathBytes = 1024
)

type modelProxyCall struct {
	Origin        string `json:"origin"`
	Path          string `json:"path"`
	Status        int    `json:"status"`
	Count         uint64 `json:"count"`
	PathTruncated bool   `json:"path_truncated,omitempty"`
}

// ModelUsage is the token usage the plane read from model responses it
// forwarded. Input excludes cache writes and cache reads.
type ModelUsage struct {
	Input      int64 `json:"input_tokens"`
	CacheWrite int64 `json:"cache_write_tokens"`
	CacheRead  int64 `json:"cache_read_tokens"`
	Output     int64 `json:"output_tokens"`
}

func (u *ModelUsage) add(v ModelUsage) {
	u.Input += v.Input
	u.CacheWrite += v.CacheWrite
	u.CacheRead += v.CacheRead
	u.Output += v.Output
}

type modelProxySummary struct {
	Generation   int              `json:"lease_generation"`
	Count        uint64           `json:"count"`
	StatusCounts map[int]uint64   `json:"status_counts"`
	Calls        []modelProxyCall `json:"calls"`
	OtherCalls   uint64           `json:"other_calls,omitempty"`
	Usage        ModelUsage       `json:"usage"`
	// UsageUnknown marks a summary that may miss usage: a response that
	// did not report all of it, or one still open when the turn finished.
	UsageUnknown bool `json:"usage_unknown,omitempty"`
	open         int  // calls whose usage is not yet reported
	finished     bool
	expires      time.Time
}

// ModelResponseRecorder reserves bounded diagnostics before forwarding a call.
// status records the response status. The call is open from now until usage
// reports its token usage, with ok false when the caller could not read all
// of it; a call that bills nothing reports a zero usage, and only the first
// report counts. A call never reported, such as one that got no response,
// stays open, so its turn's usage stays unknown. Both only update that
// reservation, even after terminal eviction.
func (e *Engine) ModelResponseRecorder(g store.Grant, origin, path string) (status func(int), usage func(ModelUsage, bool)) {
	turnID, expires := g.TurnID, g.ExpiresAt
	truncated := len(path) > modelSummaryPathBytes
	if truncated {
		path = path[:modelSummaryPathBytes]
	}
	// URL paths can share backing storage with a much larger request URI.
	path = strings.Clone(path)
	e.modelMu.Lock()
	if e.modelSummaries == nil {
		e.modelSummaries = make(map[int64]*modelProxySummary)
	}
	summary := e.modelSummaries[turnID]
	var carry *modelProxySummary
	if summary != nil {
		if g.LeaseGeneration < summary.Generation {
			// An older execution still spends tokens this summary never sees.
			summary.UsageUnknown = true
			e.modelMu.Unlock()
			return ignoreModelStatus, ignoreModelUsage
		}
		if g.LeaseGeneration > summary.Generation {
			if !summary.finished {
				carry = summary
			}
			summary = nil
			delete(e.modelSummaries, turnID)
		}
	}
	if summary == nil {
		if len(e.modelSummaries) == modelSummaryTurns {
			for id, old := range e.modelSummaries {
				if old.finished || !e.now().Before(old.expires) {
					if !old.finished {
						e.modelUsageLostAt = e.now()
					}
					delete(e.modelSummaries, id)
					break
				}
			}
		}
		if len(e.modelSummaries) == modelSummaryTurns {
			warn := !e.modelSummaryOverflow
			e.modelSummaryOverflow = true
			e.modelUsageLostAt = e.now()
			e.modelMu.Unlock()
			if warn {
				e.Log.Printf("model receipts: active-turn capacity reached; dropping diagnostic summaries")
			}
			return ignoreModelStatus, ignoreModelUsage
		}
		summary = &modelProxySummary{Generation: g.LeaseGeneration, StatusCounts: make(map[int]uint64)}
		// Requeued attempts have not written a terminal aggregate yet.
		if carry != nil {
			summary.Count, summary.OtherCalls = carry.Count, carry.OtherCalls
			summary.StatusCounts = maps.Clone(carry.StatusCounts)
			summary.Calls = slices.Clone(carry.Calls)
			summary.Usage = carry.Usage
			// Calls still open on the old attempt report to the old summary.
			summary.UsageUnknown = carry.UsageUnknown || carry.open > 0
		}
		e.modelSummaries[turnID] = summary
	}
	if expires.After(summary.expires) {
		summary.expires = expires
	}
	summary.open++
	e.modelMu.Unlock()
	status = func(code int) {
		if code < 100 || code > 999 {
			return
		}
		e.modelMu.Lock()
		defer e.modelMu.Unlock()
		// This is diagnostics for a request authenticated before forwarding.
		// A queued retry can still observe its prior response; counts carry forward.
		// Terminal or superseded reservations cannot change the stored summary.
		if summary.finished || e.modelSummaries[turnID] != summary {
			return
		}
		summary.Count++
		summary.StatusCounts[code]++
		summary.addCall(modelProxyCall{Origin: origin, Path: path, Status: code, Count: 1, PathTruncated: truncated})
	}
	var once sync.Once
	usage = func(u ModelUsage, ok bool) {
		once.Do(func() {
			e.modelMu.Lock()
			defer e.modelMu.Unlock()
			summary.open--
			if summary.finished {
				return
			}
			if !ok {
				summary.UsageUnknown = true
				return
			}
			summary.Usage.add(u)
		})
	}
	return status, usage
}

func ignoreModelStatus(int) {}

func ignoreModelUsage(ModelUsage, bool) {}

func (e *Engine) recordModelSummaryTx(tx *store.Tx, turnID int64) error {
	e.modelMu.Lock()
	summary := e.modelSummaries[turnID]
	if summary == nil {
		e.modelMu.Unlock()
		return nil
	}
	// A summary with no response still records a call that may have billed.
	if summary.Count == 0 && summary.open == 0 && !summary.UsageUnknown {
		summary.finished = true
		e.modelMu.Unlock()
		return nil
	}
	snapshot := *summary
	snapshot.Calls = slices.Clone(summary.Calls)
	snapshot.StatusCounts = maps.Clone(summary.StatusCounts)
	snapshot.UsageUnknown = summary.UsageUnknown || summary.open > 0
	// Keep the snapshot for a transaction retry. Finished snapshots can be
	// evicted when a new turn needs capacity; these are best-effort diagnostics.
	summary.finished = true
	e.modelMu.Unlock()
	// One row per lease generation: action rows are append-only (#682), so a
	// later generation records its own summary instead of rewriting an
	// earlier one. Readers that want a turn total sum the rows.
	id := fmt.Sprintf("model-proxy-summary-%d-%d", turnID, snapshot.Generation)
	if _, err := store.ActionBodyTx(tx, id); err == nil {
		return nil // already recorded, e.g. on a transaction retry
	} else if !errors.Is(err, store.ErrNoRows) {
		return err
	}
	payload, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	turn, err := store.GetTurnTx(tx, turnID)
	if err != nil {
		return err
	}
	repo, item, err := store.SessionSourceTx(tx, turn.SessionID)
	if err != nil {
		return err
	}
	return store.InsertActionTx(tx, store.Action{
		ID: id, SessionID: &turn.SessionID, TurnID: &turnID, Repo: repo, Item: item,
		Type: "model.proxy", ReasonCode: "recorded", EvidenceClass: "plane_observed",
		LimitSentence: "Bounded best-effort model-call summary; no request or response content, credentials, or query strings are recorded.",
		Body:          string(payload),
	})
}

func (summary *modelProxySummary) addCall(next modelProxyCall) {
	for i := range summary.Calls {
		call := &summary.Calls[i]
		if call.Origin == next.Origin && call.Path == next.Path && call.Status == next.Status && call.PathTruncated == next.PathTruncated {
			call.Count += next.Count
			return
		}
	}
	if len(summary.Calls) < modelSummaryCalls {
		summary.Calls = append(summary.Calls, next)
	} else {
		summary.OtherCalls += next.Count
	}
}
