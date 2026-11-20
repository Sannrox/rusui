package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/sannrox/rusui/internal/store"
)

const (
	actionPreviewMint    = "preview.mint"
	actionPreviewShare   = "preview.share"
	actionPreviewComment = "preview.comment"
	actionWorkspacePut   = "workspace.put"
)

// recordOperatorEnvAction appends one session action for an operator
// environment mutation. The body must not carry grant tokens or file bytes.
func recordOperatorEnvAction(st *store.Store, sess *store.Session, typ, limit string, body any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	id, err := newOperatorActionID()
	if err != nil {
		return err
	}
	sid := sess.ID
	return store.InsertAction(st, store.Action{
		ID: fmt.Sprintf("%s-%d-%s", typ, sid, id), SessionID: &sid, Repo: sess.Repo, Item: sess.Item,
		Type: typ, ReasonCode: "recorded", EvidenceClass: "plane_observed",
		LimitSentence: limit, Body: string(payload),
	})
}

func newOperatorActionID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
