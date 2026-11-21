package engine

import (
	"fmt"
	"slices"
	"time"

	"github.com/sannrox/rusui/internal/sessionexport"
	"github.com/sannrox/rusui/internal/store"
)

// ExportSession signs one session as an operator bundle. A plane without
// the dedicated key refuses. Guests never receive the key.
func (e *Engine) ExportSession(sessionID int64, ttl time.Duration, secrets []string) ([]byte, error) {
	if len(e.SessionExportKey) == 0 {
		return nil, sessionexport.ErrNoKey
	}
	if ttl <= 0 {
		ttl = sessionexport.DefaultTTL
	}
	planeID, err := store.PlaneID(e.Store)
	if err != nil {
		return nil, err
	}
	p, err := sessionexport.FromStore(e.Store, sessionID, planeID, secrets, e.now(), ttl)
	if err != nil {
		return nil, err
	}
	return sessionexport.Sign(e.SessionExportKey, p)
}

// ImportSession verifies MAC, expiry, and policy admit of the source, then
// mints a new session. It does not keep the source session id, restore a
// lease, grant, terminal, or guest handle, or call Restore.
func (e *Engine) ImportSession(project string, raw []byte) (int64, error) {
	if len(e.SessionExportKey) == 0 {
		return 0, sessionexport.ErrNoKey
	}
	p, err := sessionexport.Verify(e.SessionExportKey, raw, e.now())
	if err != nil {
		return 0, err
	}
	admitted, err := e.sessionExportAdmitted(p.SourcePlaneID)
	if err != nil {
		return 0, err
	}
	if !admitted {
		return 0, sessionexport.ErrSource
	}
	pol := e.PolicySnapshot()
	proj, ok := pol.Project(project)
	if !ok {
		return 0, fmt.Errorf("policy")
	}
	repo := p.SourceRepo
	if repo == "" {
		repo = "import:" + p.SourcePlaneID
	}
	var sessionID int64
	err = e.Store.Tx(func(tx *store.Tx) error {
		id, err := store.InsertImportedSessionTx(tx, project, repo, p.GuestName, p.GuestPin, e.now())
		if err != nil {
			return err
		}
		if err := store.SetSessionTenantTx(tx, id, sessionTenant(proj)); err != nil {
			return err
		}
		sessionID = id
		return nil
	})
	return sessionID, err
}

func (e *Engine) sessionExportAdmitted(source string) (bool, error) {
	if source == "" {
		return false, nil
	}
	pol := e.PolicySnapshot()
	if !slices.Contains(pol.SessionExportSources, source) {
		return false, nil
	}
	var overlay string
	err := e.Store.Tx(func(tx *store.Tx) error {
		v, err := store.OverlayGet(tx, sessionexport.OverlayKey(source))
		overlay = v
		return err
	})
	if err != nil {
		return false, err
	}
	if overlay == sessionexport.OverlayOff {
		return false, nil
	}
	return true, nil
}
