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

type Recorder interface {
	Record(Receipt) error
}

type StoreRecorder struct {
	Store     *store.Store
	Repo      string
	Item      int
	SessionID *int64
	TurnID    *int64
}

func (r StoreRecorder) Record(rec Receipt) error {
	if r.Store == nil {
		return fmt.Errorf("acp: store required")
	}
	body, err := json.Marshal(rec.Body)
	if err != nil {
		return err
	}
	return store.InsertAction(r.Store, store.Action{
		ID:            newActionID(),
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
}

func newActionID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}
