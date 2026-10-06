package engine

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"

	"github.com/sannrox/rusui/internal/gh"
	"github.com/sannrox/rusui/internal/store"
)

var errBadWebhookSignature = fmt.Errorf("bad signature")

// ErrBadWebhookSignature is a refused session-webhook delivery.
var ErrBadWebhookSignature = errBadWebhookSignature

func webhookPath(sessionID int64) string {
	return fmt.Sprintf("/hooks/sessions/%d", sessionID)
}

func newWebhookSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// EnsureSessionWebhook mints a signing secret for the session if it has none.
// The secret stays on the plane; the guest does not listen (ADR 0070).
func (e *Engine) EnsureSessionWebhook(sessionID int64) (urlPath, secret string, err error) {
	sess, err := store.GetSession(e.Store, sessionID)
	if err != nil {
		return "", "", err
	}
	if sess.Archived {
		return "", "", errArchived
	}
	secret, ok, err := store.GetSessionWebhook(e.Store, sessionID)
	if err != nil {
		return "", "", err
	}
	if ok {
		return webhookPath(sessionID), secret, nil
	}
	secret, err = newWebhookSecret()
	if err != nil {
		return "", "", err
	}
	if err := store.PutSessionWebhook(e.Store, sessionID, secret, e.now()); err != nil {
		return "", "", err
	}
	secret, ok, err = store.GetSessionWebhook(e.Store, sessionID)
	if err != nil || !ok {
		if err == nil {
			err = fmt.Errorf("session webhook")
		}
		return "", "", err
	}
	return webhookPath(sessionID), secret, nil
}

// DeliverSessionWebhook stores a signed delivery for one session. A valid
// signature wakes that environment and queues a prompt containing the
// delivery id. A bad signature is stored as a refusal and does not wake.
// An archived session does not accept delivery.
func (e *Engine) DeliverSessionWebhook(sessionID int64, sig string, body []byte, deliveryID string) error {
	if deliveryID == "" {
		sum := sha256.Sum256(body)
		deliveryID = hex.EncodeToString(sum[:])
	}
	sess, err := store.GetSession(e.Store, sessionID)
	if err != nil {
		return err
	}
	secret, ok, err := store.GetSessionWebhook(e.Store, sessionID)
	if err != nil {
		return err
	}
	if !ok {
		return sql.ErrNoRows
	}
	if sess.Archived {
		if _, err := store.InsertWebhookDelivery(e.Store, sessionID, deliveryID, false, "archived", e.now()); err != nil {
			return err
		}
		return errArchived
	}
	if !gh.Verify(secret, sig, body) {
		if _, err := store.InsertWebhookDelivery(e.Store, sessionID, deliveryID, false, "bad signature", e.now()); err != nil {
			return err
		}
		return errBadWebhookSignature
	}
	inserted, err := store.InsertWebhookDelivery(e.Store, sessionID, deliveryID, true, "", e.now())
	if err != nil {
		return err
	}
	if !inserted {
		return nil
	}
	if _, err := e.WakeSessionEnvironment(sessionID, "session webhook"); err != nil {
		return err
	}
	_, _, _, err = e.PromptQueued(sessionID, "webhook delivery "+deliveryID)
	return err
}
