package engine

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/sannrox/rusui/internal/policy"
	"github.com/sannrox/rusui/internal/store"
	"github.com/sannrox/rusui/internal/sumika"
)

const (
	SumikaReconcileEvery   = time.Minute
	localCancelGracePeriod = 30 * time.Second
)

type LocalSessionResult struct {
	Session *store.Session
	Process *store.Process
}

func (e *Engine) localRuntimeClient() (sumika.Client, error) {
	path, err := sumika.DefaultSocketPath()
	if err != nil {
		return nil, err
	}
	return sumika.NewSocketClient(path), nil
}

func (e *Engine) StartLocalSession(project, idem string) (LocalSessionResult, error) {
	pol := e.PolicySnapshot()
	p, ok := pol.Project(project)
	if !ok || !p.AllowsKind(policy.KindLocal) || p.LocalRuntime == nil {
		return LocalSessionResult{}, fmt.Errorf("policy")
	}
	if err := validateLocalRuntimePath(p.LocalRuntime.Cwd); err != nil {
		return LocalSessionResult{}, err
	}
	var sessionID int64
	created := false
	err := e.Store.Tx(func(tx *sql.Tx) error {
		paused, err := store.Paused(tx, project)
		if err != nil {
			return err
		}
		if paused {
			return errPaused
		}
		if idem != "" {
			id, found, err := store.LookupIdempotencyTx(tx, idem)
			if err != nil {
				return err
			}
			if found {
				var kind, existingProject string
				if err := tx.QueryRow(`SELECT kind, project FROM sessions WHERE id=?`, id).Scan(&kind, &existingProject); err != nil {
					return err
				}
				if kind != store.SessionKindLocal || existingProject != project {
					return fmt.Errorf("idempotency key already belongs to a different session")
				}
				sessionID = id
				return nil
			}
		}
		sessionID, err = store.InsertLocalSessionTx(tx, project, e.now())
		if err != nil {
			return err
		}
		if idem != "" {
			if err := store.PutIdempotencyTx(tx, idem, sessionID); err != nil {
				return err
			}
		}
		created = true
		return nil
	})
	if err != nil {
		return LocalSessionResult{}, err
	}
	if created {
		e.sumikaMu.Lock()
		_, startErr := e.startLocalProcess(sessionID)
		e.sumikaMu.Unlock()
		result, err := e.localSessionResult(sessionID)
		if err != nil {
			return LocalSessionResult{}, err
		}
		return result, startErr
	}
	return e.localSessionResult(sessionID)
}

func (e *Engine) RestartLocalProcess(sessionID int64) (LocalSessionResult, error) {
	e.sumikaMu.Lock()
	defer e.sumikaMu.Unlock()
	sess, err := store.GetSession(e.Store, sessionID)
	if err != nil {
		return LocalSessionResult{}, err
	}
	if sess.Kind != store.SessionKindLocal || sess.State != "open" {
		return LocalSessionResult{}, fmt.Errorf("session is not an open local session")
	}
	p, ok := e.PolicySnapshot().Project(sess.Project)
	if !ok || !p.AllowsKind(policy.KindLocal) || p.LocalRuntime == nil {
		return LocalSessionResult{}, fmt.Errorf("policy")
	}
	if err := validateLocalRuntimePath(p.LocalRuntime.Cwd); err != nil {
		return LocalSessionResult{}, err
	}
	process, startErr := e.startLocalProcess(sessionID)
	if process == nil {
		if startErr == nil {
			startErr = fmt.Errorf("local process generation was not reserved")
		}
		return LocalSessionResult{}, startErr
	}
	result, err := e.localSessionResult(sessionID)
	if err != nil {
		return LocalSessionResult{}, err
	}
	return result, startErr
}

func (e *Engine) startLocalProcess(sessionID int64) (*store.Process, error) {
	sess, err := store.GetSession(e.Store, sessionID)
	if err != nil {
		return nil, err
	}
	p, ok := e.PolicySnapshot().Project(sess.Project)
	if !ok || !p.AllowsKind(policy.KindLocal) || p.LocalRuntime == nil {
		return nil, fmt.Errorf("policy")
	}
	identityHash, err := localProcessIdentityHash(p.LocalRuntime.Argv, p.LocalRuntime.Cwd)
	if err != nil {
		return nil, err
	}
	process, err := store.StartSumikaProcess(e.Store, sessionID, identityHash, e.now())
	if err != nil {
		return nil, err
	}
	client, err := e.localRuntimeClient()
	if err != nil {
		updated, observeErr := e.observeLocalProcess(process, store.ProcessUnknown)
		if updated == nil {
			updated = process
		}
		return updated, errors.Join(err, observeErr)
	}
	info, err := client.Start(process.Name, p.LocalRuntime.Argv, p.LocalRuntime.Cwd, sess.Project)
	if err != nil {
		// A failed Start response does not prove Sumika failed before spawning.
		updated, observeErr := e.observeLocalProcess(process, store.ProcessUnknown)
		if updated == nil {
			updated = process
		}
		return updated, errors.Join(err, observeErr)
	}
	if !matchesLocalProcess(info, process) {
		lost, observeErr := e.observeLocalProcess(process, store.ProcessLost)
		e.exception(fmt.Sprintf("sumika process name collision for local session %d; process left untouched", sessionID))
		if observeErr != nil {
			return process, observeErr
		}
		return lost, fmt.Errorf("sumika process identity collision")
	}
	updated, observeErr := e.observeLocalProcess(process, mapSumikaStatus(info.Status))
	if updated == nil {
		updated = process
	}
	return updated, observeErr
}

func forceLocalCancel(process *store.Process, now time.Time) bool {
	return process.CancelRequestedAt != nil && !now.Before(process.CancelRequestedAt.Add(localCancelGracePeriod))
}

func validateLocalRuntimePath(cwd string) error {
	info, err := os.Stat(cwd)
	if err != nil {
		return fmt.Errorf("local runtime cwd: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("local runtime cwd is not a directory")
	}
	return nil
}

func localProcessIdentityHash(argv []string, cwd string) (string, error) {
	encoded, err := json.Marshal(struct {
		Argv []string `json:"argv"`
		Cwd  string   `json:"cwd"`
	}{Argv: argv, Cwd: cwd})
	if err != nil {
		return "", err
	}
	identity := append([]byte("rusui-sumika-process-v1\x00"), encoded...)
	hash := sha256.Sum256(identity)
	return hex.EncodeToString(hash[:]), nil
}

func matchesLocalProcess(info sumika.Session, process *store.Process) bool {
	identityHash, err := localProcessIdentityHash(info.Argv, info.Cwd)
	return err == nil && info.Name == process.Name && identityHash == process.IdentityHash
}

func mapSumikaStatus(status sumika.Status) string {
	switch status {
	case sumika.StatusRunning:
		return store.ProcessRunning
	case sumika.StatusIdle:
		return store.ProcessIdle
	case sumika.StatusBlocked:
		return store.ProcessBlocked
	case sumika.StatusDead:
		return store.ProcessDead
	default:
		return store.ProcessUnknown
	}
}

func (e *Engine) observeLocalProcess(process *store.Process, state string) (*store.Process, error) {
	return store.ObserveSumikaProcess(e.Store, process.ID, process.Generation, process.Revision, state, e.now())
}

func (e *Engine) localSessionResult(sessionID int64) (LocalSessionResult, error) {
	sess, err := store.GetSession(e.Store, sessionID)
	if err != nil {
		return LocalSessionResult{}, err
	}
	process, err := store.LatestSumikaProcess(e.Store, sessionID)
	if errors.Is(err, sql.ErrNoRows) {
		return LocalSessionResult{Session: sess}, nil
	}
	if err != nil {
		return LocalSessionResult{}, err
	}
	return LocalSessionResult{Session: sess, Process: process}, nil
}

func (e *Engine) CancelLocalSession(sessionID int64) (bool, error) {
	e.sumikaMu.Lock()
	defer e.sumikaMu.Unlock()
	sess, err := store.GetSession(e.Store, sessionID)
	if err != nil {
		return false, err
	}
	if sess.Kind != store.SessionKindLocal {
		return false, fmt.Errorf("session is not local")
	}
	process, err := store.LatestSumikaProcess(e.Store, sessionID)
	if errors.Is(err, sql.ErrNoRows) {
		err := store.CancelLocalSessionWithoutProcess(e.Store, sessionID)
		return err == nil, err
	}
	if err != nil {
		return false, err
	}
	if process.State == store.ProcessDead {
		err := store.CancelLocalSessionAfterDeath(e.Store, sessionID, process.ID, process.Generation, process.Revision)
		return err == nil, err
	}
	process, err = store.RequestSumikaCancel(e.Store, process.ID, process.Generation, process.Revision, e.now())
	if err != nil {
		return false, err
	}
	client, err := e.localRuntimeClient()
	if err != nil {
		_, observeErr := e.observeLocalProcess(process, store.ProcessUnknown)
		return false, errors.Join(err, observeErr)
	}
	infos, err := client.List()
	if err != nil {
		_, observeErr := e.observeLocalProcess(process, store.ProcessUnknown)
		return false, errors.Join(err, observeErr)
	}
	if process.State == store.ProcessLost {
		return false, fmt.Errorf("lost Sumika process cannot be cancelled without a current identity")
	}
	var current *sumika.Session
	for i := range infos {
		if infos[i].Name == process.Name {
			current = &infos[i]
			break
		}
	}
	if current == nil {
		if process.State != store.ProcessLost {
			_, observeErr := e.observeLocalProcess(process, store.ProcessLost)
			if observeErr != nil {
				return false, observeErr
			}
		}
		return false, fmt.Errorf("sumika process absent; cancellation unconfirmed")
	}
	if !matchesLocalProcess(*current, process) {
		_, observeErr := e.observeLocalProcess(process, store.ProcessLost)
		e.exception(fmt.Sprintf("sumika process identity collision during cancellation for local session %d; process left untouched", sessionID))
		if observeErr != nil {
			return false, observeErr
		}
		return false, fmt.Errorf("sumika process identity collision")
	}
	if current.Status == sumika.StatusDead {
		_, err := e.observeLocalProcess(process, store.ProcessDead)
		if err != nil {
			return false, err
		}
		sess, err = store.GetSession(e.Store, sessionID)
		return err == nil && sess.State == "cancelled", err
	}
	process, err = e.observeLocalProcess(process, mapSumikaStatus(current.Status))
	if err != nil {
		return false, err
	}
	info, err := client.Kill(process.Name, forceLocalCancel(process, e.now()))
	if err != nil {
		_, observeErr := e.observeLocalProcess(process, store.ProcessUnknown)
		return false, errors.Join(err, observeErr)
	}
	if !matchesLocalProcess(info, process) {
		_, observeErr := e.observeLocalProcess(process, store.ProcessLost)
		e.exception(fmt.Sprintf("sumika process identity changed during cancellation for local session %d; process left untouched", sessionID))
		if observeErr != nil {
			return false, observeErr
		}
		return false, fmt.Errorf("sumika process identity collision")
	}
	_, err = e.observeLocalProcess(process, mapSumikaStatus(info.Status))
	if err != nil {
		return false, err
	}
	sess, err = store.GetSession(e.Store, sessionID)
	return err == nil && sess.State == "cancelled", err
}

// ReconcileSumika accepts only current observations from the local daemon.
// A missing process after a successful List is recorded lost, never restarted.
func (e *Engine) ReconcileSumika() error {
	e.sumikaMu.Lock()
	defer e.sumikaMu.Unlock()
	sessions, err := store.ListLocalSessions(e.Store)
	if err != nil {
		return err
	}
	if len(sessions) == 0 && !policyAllowsLocal(e.PolicySnapshot()) {
		return nil
	}
	client, err := e.localRuntimeClient()
	if err != nil {
		return errors.Join(err, store.MarkRuntimeObservationsUnknown(e.Store, e.now()))
	}
	infos, err := client.List()
	if err != nil {
		return errors.Join(err, store.MarkRuntimeObservationsUnknown(e.Store, e.now()))
	}
	byName := make(map[string]sumika.Session, len(infos))
	for _, info := range infos {
		byName[info.Name] = info
	}
	known := make(map[string]bool)
	unresolved := make(map[string]bool)
	var first error
	for _, sess := range sessions {
		if sess.State != "open" {
			continue
		}
		process, err := store.LatestSumikaProcess(e.Store, sess.ID)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			if first == nil {
				first = err
			}
			continue
		}
		if process.State == store.ProcessDead {
			continue
		}
		if process.State == store.ProcessLost {
			unresolved[process.Name] = true
			continue
		}
		known[process.Name] = true
		info, found := byName[process.Name]
		if !found {
			if process.State != store.ProcessDead && process.State != store.ProcessLost {
				_, err = e.observeLocalProcess(process, store.ProcessLost)
				if err != nil && first == nil {
					first = err
				}
			}
			continue
		}
		if !matchesLocalProcess(info, process) {
			if process.State != store.ProcessLost {
				_, err = e.observeLocalProcess(process, store.ProcessLost)
				if err != nil && first == nil {
					first = err
				}
			}
			e.exception(fmt.Sprintf("sumika process name collision for local session %d; process left untouched", sess.ID))
			continue
		}
		process, err = e.observeLocalProcess(process, mapSumikaStatus(info.Status))
		if err != nil {
			if first == nil {
				first = err
			}
			continue
		}
		if process.CancelRequestedAt != nil && info.Status != sumika.StatusDead {
			killed, err := client.Kill(process.Name, forceLocalCancel(process, e.now()))
			if err != nil {
				_, observeErr := e.observeLocalProcess(process, store.ProcessUnknown)
				if first == nil {
					first = errors.Join(err, observeErr)
				}
			} else if !matchesLocalProcess(killed, process) {
				if _, observeErr := e.observeLocalProcess(process, store.ProcessLost); observeErr != nil && first == nil {
					first = observeErr
				}
				e.exception(fmt.Sprintf("sumika process name collision during cancellation for local session %d; process left untouched", sess.ID))
			} else if _, observeErr := e.observeLocalProcess(process, mapSumikaStatus(killed.Status)); observeErr != nil && first == nil {
				first = observeErr
			}
		}
	}
	for _, info := range infos {
		if strings.HasPrefix(info.Name, "rusui-") && info.Status != sumika.StatusDead && !known[info.Name] {
			if unresolved[info.Name] {
				e.exception(fmt.Sprintf("Sumika process %q has an unresolved local identity; process left untouched", info.Name))
			} else {
				e.exception(fmt.Sprintf("orphan Sumika process %q left untouched", info.Name))
			}
		}
	}
	return first
}

func policyAllowsLocal(p *policy.Effective) bool {
	if p == nil {
		return false
	}
	for _, project := range p.Projects {
		if project.AllowsKind(policy.KindLocal) {
			return true
		}
	}
	return false
}

type trackedAttach struct {
	conn       net.Conn
	store      *store.Store
	now        func() time.Time
	onError    func(error)
	processID  int64
	processGen int64
	attachGen  int64
	once       sync.Once
}

func (c *trackedAttach) Read(p []byte) (int, error) {
	n, err := c.conn.Read(p)
	if err != nil {
		c.end()
	}
	return n, err
}

func (c *trackedAttach) Write(p []byte) (int, error) { return c.conn.Write(p) }

func (c *trackedAttach) Close() error {
	err := c.conn.Close()
	c.end()
	return err
}

func (c *trackedAttach) end() {
	c.once.Do(func() {
		_, err := store.EndSumikaAttach(c.store, c.processID, c.processGen, c.attachGen, store.AttachDetached, c.now())
		if err != nil && !errors.Is(err, store.ErrStaleAttach) {
			c.onError(fmt.Errorf("record local attach detach: %w", err))
		}
	})
}

// AttachLocalSession opens Sumika's raw PTY stream and records its Attach generation.
func (e *Engine) AttachLocalSession(sessionID int64) (*store.Attach, io.ReadWriteCloser, error) {
	sess, err := store.GetSession(e.Store, sessionID)
	if err != nil {
		return nil, nil, err
	}
	if sess.Kind != store.SessionKindLocal {
		return nil, nil, fmt.Errorf("session is not local")
	}
	process, err := store.LatestSumikaProcess(e.Store, sessionID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, store.ErrProcessNotAttachable
	}
	if err != nil {
		return nil, nil, err
	}
	if process.State == store.ProcessUnknown {
		if err := e.ReconcileSumika(); err != nil {
			return nil, nil, err
		}
	}
	e.sumikaMu.Lock()
	defer e.sumikaMu.Unlock()
	sess, err = store.GetSession(e.Store, sessionID)
	if err != nil {
		return nil, nil, err
	}
	if sess.Kind != store.SessionKindLocal || sess.State != "open" {
		return nil, nil, store.ErrProcessNotAttachable
	}
	pol := e.PolicySnapshot()
	if pol == nil {
		return nil, nil, fmt.Errorf("policy")
	}
	p, ok := pol.Project(sess.Project)
	if !ok || !p.AllowsKind(policy.KindLocal) || p.LocalRuntime == nil {
		return nil, nil, fmt.Errorf("policy")
	}
	process, err = store.LatestSumikaProcess(e.Store, sessionID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, store.ErrProcessNotAttachable
	}
	if err != nil {
		return nil, nil, err
	}
	if (process.State != store.ProcessRunning && process.State != store.ProcessIdle && process.State != store.ProcessBlocked) || process.CancelRequestedAt != nil {
		return nil, nil, store.ErrProcessNotAttachable
	}
	client, err := e.localRuntimeClient()
	if err != nil {
		return nil, nil, err
	}
	infos, err := client.List()
	if err != nil {
		return nil, nil, err
	}
	found := false
	for _, info := range infos {
		if info.Name == process.Name {
			found = matchesLocalProcess(info, process)
			break
		}
	}
	if !found {
		return nil, nil, fmt.Errorf("sumika process is absent or its identity does not match policy")
	}
	info, conn, err := client.Attach(process.Name)
	if err != nil {
		return nil, nil, err
	}
	if !matchesLocalProcess(info, process) || !info.Focused {
		_ = conn.Close()
		return nil, nil, fmt.Errorf("sumika process identity collision during attach")
	}
	attach, err := store.BeginSumikaAttach(e.Store, process.ID, process.Generation, e.now())
	if err != nil {
		_ = conn.Close()
		return nil, nil, err
	}
	return attach, &trackedAttach{
		conn: conn, store: e.Store, now: e.now,
		onError:   func(err error) { e.exception(err.Error()) },
		processID: process.ID, processGen: process.Generation, attachGen: attach.Generation,
	}, nil
}
