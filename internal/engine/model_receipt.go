package engine

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
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

type modelProxySummary struct {
	Generation   int              `json:"lease_generation"`
	Count        uint64           `json:"count"`
	StatusCounts map[int]uint64   `json:"status_counts"`
	Calls        []modelProxyCall `json:"calls"`
	OtherCalls   uint64           `json:"other_calls,omitempty"`
	finished     bool
	expires      time.Time
}

// ModelResponseRecorder reserves bounded diagnostics before forwarding a call.
// Its callback only updates that reservation, even after terminal eviction.
func (e *Engine) ModelResponseRecorder(g store.Grant, origin, path string) func(int) {
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
			e.modelMu.Unlock()
			return func(int) {}
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
					delete(e.modelSummaries, id)
					break
				}
			}
		}
		if len(e.modelSummaries) == modelSummaryTurns {
			warn := !e.modelSummaryOverflow
			e.modelSummaryOverflow = true
			e.modelMu.Unlock()
			if warn {
				e.Log.Printf("model receipts: active-turn capacity reached; dropping diagnostic summaries")
			}
			return func(int) {}
		}
		summary = &modelProxySummary{Generation: g.LeaseGeneration, StatusCounts: make(map[int]uint64)}
		// Requeued attempts have not written a terminal aggregate yet.
		if carry != nil {
			summary.Count, summary.OtherCalls = carry.Count, carry.OtherCalls
			summary.StatusCounts = maps.Clone(carry.StatusCounts)
			summary.Calls = slices.Clone(carry.Calls)
		}
		e.modelSummaries[turnID] = summary
	}
	if expires.After(summary.expires) {
		summary.expires = expires
	}
	e.modelMu.Unlock()
	return func(status int) {
		if status < 100 || status > 999 {
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
		summary.StatusCounts[status]++
		summary.addCall(modelProxyCall{Origin: origin, Path: path, Status: status, Count: 1, PathTruncated: truncated})
	}
}

func (e *Engine) recordModelSummaryTx(tx *sql.Tx, turnID int64) error {
	e.modelMu.Lock()
	summary := e.modelSummaries[turnID]
	if summary == nil {
		e.modelMu.Unlock()
		return nil
	}
	if summary.Count == 0 {
		summary.finished = true
		e.modelMu.Unlock()
		return nil
	}
	snapshot := *summary
	snapshot.Calls = slices.Clone(summary.Calls)
	snapshot.StatusCounts = maps.Clone(summary.StatusCounts)
	// Keep the snapshot for a transaction retry. Finished snapshots can be
	// evicted when a new turn needs capacity; these are best-effort diagnostics.
	summary.finished = true
	e.modelMu.Unlock()
	id := fmt.Sprintf("model-proxy-summary-%d", turnID)
	var existingBody string
	err := tx.QueryRow(`SELECT body FROM actions WHERE action_id=?`, id).Scan(&existingBody)
	existing := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var aggregate modelProxySummary
	if existing {
		if err := json.Unmarshal([]byte(existingBody), &aggregate); err != nil {
			return err
		}
		if aggregate.Generation >= snapshot.Generation {
			return nil
		}
		aggregate.Generation = snapshot.Generation
		aggregate.Count += snapshot.Count
		aggregate.OtherCalls += snapshot.OtherCalls
		for status, count := range snapshot.StatusCounts {
			aggregate.StatusCounts[status] += count
		}
		for _, call := range snapshot.Calls {
			aggregate.addCall(call)
		}
	} else {
		aggregate = snapshot
	}
	payload, err := json.Marshal(aggregate)
	if err != nil {
		return err
	}
	if existing {
		_, err := tx.Exec(`UPDATE actions SET body=? WHERE action_id=?`, string(payload), id)
		return err
	}
	turn, err := store.GetTurnTx(tx, turnID)
	if err != nil {
		return err
	}
	var repo string
	var item int
	if err := tx.QueryRow(`SELECT repo,item FROM sessions WHERE id=?`, turn.SessionID).Scan(&repo, &item); err != nil {
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
