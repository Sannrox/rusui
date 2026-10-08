package engine

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/sannrox/rusui/internal/guest"
	"github.com/sannrox/rusui/internal/policy"
	"github.com/sannrox/rusui/internal/store"
)

var ErrGuestNotAllowed = errors.New("guest not allowed")

func (e *Engine) chooseGuest(project, requested string, pol *policy.Effective) (string, guest.Entry, error) {
	p, ok := pol.Project(project)
	if !ok {
		return "", guest.Entry{}, fmt.Errorf("guest: project not found")
	}
	name := requested
	if p.Guests != nil {
		if name == "" {
			name = p.Guests.Default
		}
		if !slices.Contains(p.Guests.Allowed, name) {
			return "", guest.Entry{}, fmt.Errorf("%w: %q by project %s", ErrGuestNotAllowed, name, project)
		}
	} else {
		fallback := e.DefaultGuest
		if fallback == "" {
			fallback = guest.KindGrok
		}
		if name == "" {
			name = fallback
		}
		if name != fallback {
			return "", guest.Entry{}, fmt.Errorf("%w: %q by project %s", ErrGuestNotAllowed, name, project)
		}
	}
	entry, ok := pol.Guests[name]
	if !ok {
		return "", guest.Entry{}, fmt.Errorf("guest: %q is not registered", name)
	}
	if _, builtin := guest.Builtin()[name]; !builtin && entry.Protocol == guest.ProtocolACP && e.Container == nil {
		return "", guest.Entry{}, fmt.Errorf("guest: generic ACP guests require a container driver")
	}
	return name, entry, nil
}

func (e *Engine) bindSessionGuestTx(tx *sql.Tx, sessionID int64, project, requested string, pol *policy.Effective) (string, guest.Entry, error) {
	name, pin, raw, err := store.SessionGuestTx(tx, sessionID)
	if err != nil {
		return "", guest.Entry{}, err
	}
	if name != "" {
		if requested != "" && requested != name {
			return "", guest.Entry{}, fmt.Errorf("guest: session is already pinned to %q", name)
		}
		if _, _, err := e.chooseGuest(project, name, pol); err != nil {
			return "", guest.Entry{}, err
		}
		var entry guest.Entry
		if err := json.Unmarshal([]byte(raw), &entry); err != nil {
			return "", guest.Entry{}, fmt.Errorf("guest: decode session pin: %w", err)
		}
		if entry.Pin != pin || len(entry.Argv) == 0 {
			return "", guest.Entry{}, fmt.Errorf("guest: invalid session pin")
		}
		return name, entry, nil
	}
	name, entry, err := e.chooseGuest(project, requested, pol)
	if err != nil {
		return "", guest.Entry{}, err
	}
	encoded, err := json.Marshal(entry)
	if err != nil {
		return "", guest.Entry{}, err
	}
	if err := store.SetSessionGuestTx(tx, sessionID, name, entry.Pin, string(encoded)); err != nil {
		return "", guest.Entry{}, err
	}
	return name, entry, nil
}

// SessionGuest returns the frozen spawn, refusing an invalid or revoked choice.
func (e *Engine) SessionGuest(sessionID int64) (string, guest.Entry, error) {
	sess, err := store.GetSession(e.Store, sessionID)
	if err != nil {
		return "", guest.Entry{}, err
	}
	pol := e.PolicySnapshot()
	project := sess.Project
	if project == "" {
		if p, ok := pol.ProjectForRepo(sess.Repo); ok {
			project = p.Slug
		}
	}
	var name string
	var entry guest.Entry
	err = e.Store.Tx(func(tx *sql.Tx) error {
		var err error
		name, entry, err = e.bindSessionGuestTx(tx, sessionID, project, "", pol)
		return err
	})
	return name, entry, err
}
