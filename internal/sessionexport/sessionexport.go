// Package sessionexport is the signed operator session bundle (ADR 0100).
// HMAC-SHA256 follows the ADR 0082 SignReceipt/Verify shape under a dedicated
// plane-held key. rusui receipt and rusui diagnostics stay unsigned copies.
package sessionexport

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/sannrox/rusui/internal/ops"
	"github.com/sannrox/rusui/internal/receipt"
	"github.com/sannrox/rusui/internal/store"
)

const (
	// KeyEnv names the dedicated HMAC key. It is not RUSUI_CONFORMANCE_KEY,
	// the operator token, the worker secret, or a TLS key.
	KeyEnv = "RUSUI_SESSION_EXPORT_KEY"
	// Kind is the signed object name in the payload.
	Kind = "rusui-signed-session-bundle"
	// Schema is the payload schema version.
	Schema = 1
	// DefaultTTL is the signed expiry window when the operator omits one.
	DefaultTTL = 24 * time.Hour
	// OverlayOff is the overlay value that removes one admitted source.
	OverlayOff = "0"
	// OverlayPrefix keys SQLite overlay rows session_export_source:<plane_id>.
	OverlayPrefix = "session_export_source:"

	macDomain = "rusui-signed-session-bundle\x00"
)

var (
	ErrNoKey     = errors.New("session export: plane has no " + KeyEnv)
	ErrBadMAC    = errors.New("session export: signature invalid")
	ErrNoExpiry  = errors.New("session export: expiry required")
	ErrExpired   = errors.New("session export: expired")
	ErrMalformed = errors.New("session export: malformed bundle")
	ErrSource    = errors.New("session export: source not admitted")
)

// TurnMeta is transcript metadata for one turn: ids, seq bounds, state.
// Prompt bodies are absent.
type TurnMeta struct {
	ID    int64  `json:"id"`
	SeqLo int64  `json:"seq_lo"`
	SeqHi int64  `json:"seq_hi"`
	State string `json:"state"`
}

// Payload is the signed bytes. Expiry is required. Live grants, tokens,
// workspace bytes, guest handles, and terminal leases are absent.
type Payload struct {
	Schema          int              `json:"schema"`
	Kind            string           `json:"kind"`
	SourcePlaneID   string           `json:"source_plane_id"`
	SourceSessionID int64            `json:"source_session_id"`
	SourceRepo      string           `json:"source_repo,omitempty"`
	SourceProject   string           `json:"source_project,omitempty"`
	ExportedAt      time.Time        `json:"exported_at"`
	ExpiresAt       time.Time        `json:"expires_at"`
	SourceGitPin    string           `json:"source_git_pin,omitempty"`
	GuestName       string           `json:"guest_name,omitempty"`
	GuestPin        string           `json:"guest_pin,omitempty"`
	CreatedAt       time.Time        `json:"created_at"`
	Turns           []TurnMeta       `json:"turns"`
	Receipts        []receipt.Export `json:"receipts"`
}

// Envelope is the operator-portable document: payload bytes plus HMAC.
type Envelope struct {
	Payload json.RawMessage `json:"payload"`
	MAC     string          `json:"mac"`
}

// ParseKey decodes the hex key. Empty means the plane has no key, so both
// export and import are refused.
func ParseKey(s string) ([]byte, error) {
	if s == "" {
		return nil, nil
	}
	key, err := hex.DecodeString(s)
	if err != nil || len(key) < 32 {
		return nil, fmt.Errorf("session export: %s must be at least 32 hex-encoded bytes", KeyEnv)
	}
	return key, nil
}

// OverlayKey is the SQLite overlay row that may remove source.
func OverlayKey(planeID string) string {
	return OverlayPrefix + planeID
}

// Sign HMAC-SHA256s payload under key. Expiry must already be set.
func Sign(key []byte, p Payload) ([]byte, error) {
	if len(key) == 0 {
		return nil, ErrNoKey
	}
	if err := ready(p); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	env := Envelope{Payload: raw, MAC: mac(key, raw)}
	return json.Marshal(env)
}

// Verify checks unknown fields, MAC, and signed expiry. It does not admit
// the source; the caller applies policy.
func Verify(key []byte, raw []byte, now time.Time) (Payload, error) {
	if len(key) == 0 {
		return Payload{}, ErrNoKey
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var env Envelope
	if err := dec.Decode(&env); err != nil {
		return Payload{}, ErrMalformed
	}
	if dec.More() || len(env.Payload) == 0 || env.MAC == "" {
		return Payload{}, ErrMalformed
	}
	pdec := json.NewDecoder(bytes.NewReader(env.Payload))
	pdec.DisallowUnknownFields()
	var p Payload
	if err := pdec.Decode(&p); err != nil {
		return Payload{}, ErrMalformed
	}
	if pdec.More() {
		return Payload{}, ErrMalformed
	}
	if !hmac.Equal([]byte(env.MAC), []byte(mac(key, env.Payload))) {
		return Payload{}, ErrBadMAC
	}
	if err := ready(p); err != nil {
		return Payload{}, err
	}
	if !now.Before(p.ExpiresAt) {
		return Payload{}, ErrExpired
	}
	return p, nil
}

func ready(p Payload) error {
	if p.Schema != Schema || p.Kind != Kind || p.SourcePlaneID == "" || p.SourceSessionID < 1 {
		return ErrMalformed
	}
	if p.ExpiresAt.IsZero() {
		return ErrNoExpiry
	}
	return nil
}

func mac(key, payload []byte) string {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(macDomain))
	m.Write(payload)
	return hex.EncodeToString(m.Sum(nil))
}

// FromStore builds an unsigned payload of receipts and transcript metadata.
// secrets are redacted. Prompt bodies, guest handles, live grants, tokens,
// workspace bytes, and terminal leases are not copied.
func FromStore(s *store.Store, sessionID int64, planeID string, secrets []string, now time.Time, ttl time.Duration) (Payload, error) {
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	sess, err := store.GetSession(s, sessionID)
	if err != nil {
		return Payload{}, err
	}
	turns, err := store.ListTurnsForSession(s, sessionID)
	if err != nil {
		return Payload{}, err
	}
	bounds, err := store.TurnSeqBounds(s, sessionID)
	if err != nil {
		return Payload{}, err
	}
	meta := make([]TurnMeta, 0, len(turns))
	for _, t := range turns {
		b := bounds[t.ID]
		meta = append(meta, TurnMeta{ID: t.ID, SeqLo: b[0], SeqHi: b[1], State: t.State})
	}
	p := Payload{
		Schema:          Schema,
		Kind:            Kind,
		SourcePlaneID:   planeID,
		SourceSessionID: sess.ID,
		SourceRepo:      sess.Repo,
		SourceProject:   sess.Project,
		ExportedAt:      now.UTC().Truncate(time.Second),
		ExpiresAt:       now.UTC().Add(ttl).Truncate(time.Second),
		GuestName:       sess.GuestName,
		GuestPin:        sess.GuestPin,
		CreatedAt:       sess.CreatedAt.UTC(),
		Turns:           meta,
		Receipts:        []receipt.Export{},
	}
	exp, err := receipt.FromStore(s, sessionID, secrets)
	if err == nil {
		exp.GuestSessionID = ""
		p.SourceGitPin = exp.SourceGitPin
		p.Receipts = []receipt.Export{exp}
	} else if !errors.Is(err, receipt.ErrNoReview) {
		return Payload{}, err
	}
	if secrets != nil {
		p.SourceRepo = ops.Redact(p.SourceRepo, secrets)
		p.SourceProject = ops.Redact(p.SourceProject, secrets)
		p.SourceGitPin = ops.Redact(p.SourceGitPin, secrets)
		p.GuestName = ops.Redact(p.GuestName, secrets)
		p.GuestPin = ops.Redact(p.GuestPin, secrets)
	}
	return p, nil
}
