package engine

import "github.com/sannrox/rusui/internal/store"

// PinSession keeps the session's environment awake across idle sleep until
// unpin or archive. The same environment, not a second kind.
func (e *Engine) PinSession(sessionID int64) error {
	sess, err := store.GetSession(e.Store, sessionID)
	if err != nil {
		return err
	}
	if sess.Archived {
		return errArchived
	}
	_, err = store.SetEnvironmentPinned(e.Store, sess.EnvironmentID, true, e.now())
	return err
}

// UnpinSession restores idle sleep on the session's environment.
func (e *Engine) UnpinSession(sessionID int64) error {
	sess, err := store.GetSession(e.Store, sessionID)
	if err != nil {
		return err
	}
	_, err = store.SetEnvironmentPinned(e.Store, sess.EnvironmentID, false, e.now())
	return err
}
