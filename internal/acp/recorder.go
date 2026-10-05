package acp

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/sannrox/rusui/internal/store"
)

// Receipt is one inbound ACP request (or a tool_call update) persisted
// as an actions row.
type Receipt struct {
	Type   string
	Reason string
	Body   any
}

// Recorder persists a receipt and returns the created action id. A
// permission request waits only on the id its own approval receipt returned.
type Recorder interface {
	Record(Receipt) (string, error)
}

type StoreRecorder struct {
	Store     *store.Store
	Repo      string
	Item      int
	SessionID *int64
	TurnID    *int64
}

func (r StoreRecorder) Record(rec Receipt) (string, error) {
	if r.Store == nil {
		return "", fmt.Errorf("acp: store required")
	}
	body, err := json.Marshal(rec.Body)
	if err != nil {
		return "", err
	}
	id := newActionID()
	err = store.InsertAction(r.Store, store.Action{
		ID:            id,
		SessionID:     r.SessionID,
		TurnID:        r.TurnID,
		Repo:          r.Repo,
		Item:          r.Item,
		Type:          rec.Type,
		ReasonCode:    rec.Reason,
		EvidenceClass: EvidenceObserved,
		LimitSentence: LimitSentence,
		Body:          string(body),
	})
	if err != nil {
		return "", err
	}
	return id, nil
}

func newActionID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}
