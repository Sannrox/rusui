package engine

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/sannrox/rusui/internal/acp"
	"github.com/sannrox/rusui/internal/flight"
	"github.com/sannrox/rusui/internal/store"
)

// NoteRead records that this turn read ontology object o. The objects
// are copied into the flight record when the turn completes.
func (e *Engine) NoteRead(turnID int64, o flight.Object) {
	if e == nil || o.ID == "" {
		return
	}
	e.flightMu.Lock()
	defer e.flightMu.Unlock()
	if e.flightReads == nil {
		e.flightReads = make(map[int64][]flight.Object)
	}
	e.flightReads[turnID] = append(e.flightReads[turnID], o)
}

func (e *Engine) takeReads(turnID int64) []flight.Object {
	e.flightMu.Lock()
	defer e.flightMu.Unlock()
	objs := e.flightReads[turnID]
	delete(e.flightReads, turnID)
	return objs
}

func (e *Engine) recordFlightTx(tx *sql.Tx, turnID int64, receiptID string) error {
	turn, err := store.GetTurnTx(tx, turnID)
	if err != nil {
		return err
	}
	sess, err := store.GetSessionTx(tx, turn.SessionID)
	if err != nil {
		return err
	}
	acts, err := store.ListActionsForTurnTx(tx, turnID)
	if err != nil {
		return err
	}
	rec := flight.Normalize(flight.Record{
		SessionID: turn.SessionID,
		TurnID:    turnID,
		ReceiptID: receiptID,
		Prompt:    sess.Prompt,
		Model:     flight.Model{Name: sess.GuestName, Settings: sess.GuestConfig},
		Tools:     toolsFromActions(acts),
		Snapshot:  flight.Snapshot{Objects: e.takeReads(turnID)},
	})
	return store.InsertFlightRecordTx(tx, rec, e.now())
}

func toolsFromActions(acts []store.Action) []flight.Tool {
	var out []flight.Tool
	seen := map[string]int{}
	for _, a := range acts {
		if a.Type != acp.ActionUpdate {
			continue
		}
		update := sessionUpdate(a.Body)
		kind, _ := update["sessionUpdate"].(string)
		id, _ := update["toolCallId"].(string)
		if id == "" {
			continue
		}
		switch kind {
		case "tool_call":
			name, _ := update["title"].(string)
			if name == "" {
				name, _ = update["kind"].(string)
			}
			input, _ := update["rawInput"].(map[string]any)
			t := flight.Tool{ID: id, Name: name, Kind: flight.Classify(name), Input: input}
			if i, ok := seen[id]; ok {
				out[i] = t
				continue
			}
			seen[id] = len(out)
			out = append(out, t)
		case "tool_call_update":
			status, _ := update["status"].(string)
			if i, ok := seen[id]; ok {
				out[i].Result = status
			}
		}
	}
	return out
}

func sessionUpdate(body string) map[string]any {
	var raw map[string]any
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		return nil
	}
	if nested, ok := raw["update"].(map[string]any); ok {
		raw = nested
	}
	if _, ok := raw["sessionUpdate"]; ok {
		return raw
	}
	return nil
}

func receiptID(turnID int64, gen, claimed int) string {
	return fmt.Sprintf("%d:%d:%d", turnID, gen, claimed)
}
