package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/sannrox/rusui/internal/env"
	"github.com/sannrox/rusui/internal/policy"
	"github.com/sannrox/rusui/internal/snapshot"
	"github.com/sannrox/rusui/internal/store"
)

func SourceHash(image, pin string, setup []byte) string {
	h := sha256.New()
	h.Write([]byte(image))
	h.Write([]byte{0})
	h.Write([]byte(pin))
	h.Write([]byte{0})
	h.Write(setup)
	return hex.EncodeToString(h.Sum(nil))
}

type EnvSpec struct {
	Name        string
	Kind        string
	SourceHash  string
	Repo        string
	Pin         string
	CPUMillis   int
	MemoryBytes int64
}

func (e *Engine) envDriver() env.Driver {
	if e.Env != nil {
		return e.Env
	}
	return env.Process{Root: "environments"}
}

func (e *Engine) driverFor(kind string) (env.Driver, error) {
	switch kind {
	case "", env.KindProcess:
		return e.envDriver(), nil
	case env.KindContainer:
		if e.Container == nil {
			return nil, fmt.Errorf("env: container driver not configured")
		}
		return e.Container, nil
	default:
		return nil, fmt.Errorf("env: unknown driver %s", kind)
	}
}

func (e *Engine) envTTL() time.Duration {
	if e.EnvTTL > 0 {
		return e.EnvTTL
	}
	return store.EnvTTL
}

func (e *Engine) CreateEnvironment(name string) (*store.Environment, error) {
	return e.ProvisionEnvironment(EnvSpec{Name: name})
}

func (e *Engine) ProvisionEnvironment(spec EnvSpec) (*store.Environment, error) {
	if spec.Name == "" || spec.Name == store.LocalEnvironmentName {
		return nil, fmt.Errorf("env: name %q reserved", spec.Name)
	}
	d, err := e.driverFor(spec.Kind)
	if err != nil {
		return nil, err
	}
	var handle string
	if sc, ok := d.(env.SpecCreator); ok {
		handle, err = sc.CreateSpec(env.Spec{
			Name: spec.Name, CPUMillis: spec.CPUMillis, MemoryBytes: spec.MemoryBytes,
		})
	} else {
		handle, err = d.Create(spec.Name)
	}
	if err != nil {
		return nil, err
	}
	var caps provisionCaptures
	caps.setup, err = e.prepareWorkspace(d, handle, spec.Repo, spec.Pin, spec.SourceHash)
	if err != nil {
		_ = d.Destroy(handle)
		if caps.setup == nil {
			return nil, err
		}
		return nil, &ProvisionError{Err: err, captures: caps}
	}
	caps.services, caps.started, err = startServices(d, handle)
	if err != nil {
		_ = d.Destroy(handle)
		return nil, &ProvisionError{Err: err, captures: caps}
	}
	now := e.now()
	exp := now.Add(e.envTTL())
	id, err := store.InsertEnvironment(e.Store, store.Environment{
		Name:        spec.Name,
		Driver:      d.Kind(),
		State:       store.EnvReady,
		Handle:      handle,
		SourceHash:  spec.SourceHash,
		ExpiresAt:   &exp,
		CPUMillis:   spec.CPUMillis,
		MemoryBytes: spec.MemoryBytes,
		CreatedAt:   now,
	})
	if err != nil {
		_ = d.Destroy(handle)
		return nil, err
	}
	e.recordProvision(id, caps)
	return store.GetEnvironment(e.Store, id)
}

// ProvisionError is a failed provision. It carries the setup and service
// output gathered before any environment row existed.
type ProvisionError struct {
	Err      error
	captures provisionCaptures
}

func (p *ProvisionError) Error() string { return p.Err.Error() }
func (p *ProvisionError) Unwrap() error { return p.Err }

type provisionCaptures struct {
	setup    *env.Capture
	services []env.Capture
	started  bool
}

func (e *Engine) recordProvision(envID int64, caps provisionCaptures) {
	if caps.setup != nil {
		e.recordCaptures(envID, env.CaptureSetup, []env.Capture{*caps.setup})
	}
	if caps.started {
		e.recordCaptures(envID, env.CaptureService, caps.services)
	}
}

// recordCaptures replaces the environment's stored output of kind (#334).
// A capture that did not run clears the kind. A storage failure is
// reported, not turned into a hook failure.
func (e *Engine) recordCaptures(envID int64, kind string, caps []env.Capture) {
	rows := make([]store.CaptureRow, 0, len(caps))
	for _, c := range caps {
		if c.Ran {
			rows = append(rows, store.CaptureRow{Name: c.Name, Output: c.Output, Truncated: c.Truncated, Failed: c.Failed})
		}
	}
	if err := store.ReplaceEnvironmentCaptures(e.Store, envID, kind, rows, e.now()); err != nil {
		e.exception(fmt.Sprintf("env %d: store %s output: %v", envID, kind, err))
	}
}

// resumeAndStart runs resume and the declared services after a wake and
// stores their output.
func (e *Engine) resumeAndStart(d env.Driver, envID int64, handle string) error {
	if r, ok := d.(env.Resumer); ok {
		c, err := r.Resume(handle)
		e.recordCaptures(envID, env.CaptureResume, []env.Capture{c})
		if err != nil {
			return err
		}
	}
	return e.startServicesFor(d, envID, handle)
}

func (e *Engine) startServicesFor(d env.Driver, envID int64, handle string) error {
	caps, started, err := startServices(d, handle)
	if started {
		e.recordCaptures(envID, env.CaptureService, caps)
	}
	return err
}

func (e *Engine) SleepEnvironment(id int64) (*store.Environment, error) {
	release, ok := e.beginEnvironmentOperation(id)
	if !ok {
		return nil, store.ErrEnvironmentBusy
	}
	defer release()
	envRow, err := store.GetEnvironment(e.Store, id)
	if err != nil {
		return nil, err
	}
	if envRow.Name == store.LocalEnvironmentName {
		return nil, fmt.Errorf("env: local environment is not slept")
	}
	if envRow.State != store.EnvReady {
		return nil, fmt.Errorf("env: sleep requires ready, have %s", envRow.State)
	}
	reserved, err := store.ReserveEnvironmentSleep(e.Store, id, e.now())
	if err != nil {
		return nil, err
	}
	if !reserved {
		return nil, store.ErrEnvironmentBusy
	}
	return e.sleepReservedEnvironment(envRow)
}

func (e *Engine) sleepReservedEnvironment(envRow *store.Environment) (*store.Environment, error) {
	current, err := store.GetEnvironment(e.Store, envRow.ID)
	if err != nil {
		return nil, e.failSleep(envRow, err, nil)
	}
	if current.State != store.EnvSleeping {
		return nil, fmt.Errorf("env: sleep reservation lost, have %s", current.State)
	}
	envRow = current
	d, err := e.driverFor(envRow.Driver)
	if err != nil {
		return nil, e.failSleep(envRow, err, nil)
	}
	if err := stopServices(d, envRow.Handle); err != nil {
		recoveryErr := e.startServicesFor(d, envRow.ID, envRow.Handle)
		return nil, e.failSleep(envRow, err, recoveryErr)
	}
	if err := d.Sleep(envRow.Handle); err != nil {
		recoveryErr := d.Wake(envRow.Handle)
		if recoveryErr == nil {
			recoveryErr = e.resumeAndStart(d, envRow.ID, envRow.Handle)
		}
		return nil, e.failSleep(envRow, err, recoveryErr)
	}
	now := e.now()
	envRow.State = store.EnvSleeping
	envRow.SleptAt = &now
	if err := store.UpdateEnvironment(e.Store, *envRow); err != nil {
		return nil, e.failSleep(envRow, err, fmt.Errorf("database remains reserved as sleeping"))
	}
	if err := store.InsertEnvironmentReceipt(e.Store, envRow.ID, "sleep", "succeeded", "", now); err != nil {
		return nil, err
	}
	return envRow, nil
}

func (e *Engine) failSleep(envRow *store.Environment, cause, recoveryErr error) error {
	detail := cause.Error()
	if recoveryErr == nil {
		if err := store.RestoreEnvironmentReady(e.Store, envRow.ID); err != nil {
			detail += "; recovery succeeded but state restore failed: " + err.Error()
		} else {
			detail += "; environment recovered to ready"
		}
	} else {
		detail += "; recovery failed: " + recoveryErr.Error()
	}
	if err := store.InsertEnvironmentReceipt(e.Store, envRow.ID, "sleep", "failed", detail, e.now()); err != nil {
		return fmt.Errorf("env %d sleeping failed: %s; recording receipt: %w", envRow.ID, detail, err)
	}
	return fmt.Errorf("env %d sleeping failed: %s", envRow.ID, detail)
}

func (e *Engine) WakeEnvironment(id int64) (*store.Environment, error) {
	return e.wakeEnvironment(id, "")
}

// operatorWakeWait bounds how long an operator action waits for another
// wake or sleep of the same environment to finish.
const operatorWakeWait = 2 * time.Minute

// WakeSessionEnvironment wakes the session's sleeping environment before an
// operator action (#330): terminal, preview, or prompt. The same environment
// id and handle come back; cause is recorded on the wake receipt. A wake or
// sleep already in flight is waited for rather than failed. Any other state
// is returned unchanged for the caller to judge.
func (e *Engine) WakeSessionEnvironment(sessionID int64, cause string) (*store.Environment, error) {
	sess, err := store.GetSession(e.Store, sessionID)
	if err != nil {
		return nil, err
	}
	if sess.Archived {
		return nil, errArchived
	}
	deadline := time.Now().Add(operatorWakeWait)
	for {
		envRow, err := store.GetEnvironment(e.Store, sess.EnvironmentID)
		if err != nil {
			return nil, err
		}
		if envRow.State != store.EnvSleeping || envRow.Handle == "" {
			return envRow, nil
		}
		woke, err := e.wakeEnvironment(envRow.ID, cause)
		if err == nil {
			return woke, nil
		}
		if !errors.Is(err, store.ErrEnvironmentBusy) {
			// Another wake may have finished between the read above and
			// taking the operation; rereading joins it.
			if cur, gerr := store.GetEnvironment(e.Store, envRow.ID); gerr == nil && cur.State != store.EnvSleeping {
				continue
			}
			return nil, err
		}
		if time.Now().After(deadline) {
			return nil, err
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (e *Engine) wakeEnvironment(id int64, detail string) (*store.Environment, error) {
	release, ok := e.beginEnvironmentOperation(id)
	if !ok {
		return nil, store.ErrEnvironmentBusy
	}
	defer release()
	envRow, err := store.GetEnvironment(e.Store, id)
	if err != nil {
		return nil, err
	}
	if envRow.State == store.EnvExpired {
		return nil, fmt.Errorf("env: expired")
	}
	if envRow.State != store.EnvSleeping {
		return nil, fmt.Errorf("env: wake requires sleeping, have %s", envRow.State)
	}
	d, err := e.driverFor(envRow.Driver)
	if err != nil {
		return nil, e.failWake(envRow, err)
	}
	if err := d.Wake(envRow.Handle); err != nil {
		return nil, e.failWake(envRow, withRollbackError(err, rollbackWake(d, envRow.Handle)))
	}
	if err := e.resumeAndStart(d, envRow.ID, envRow.Handle); err != nil {
		return nil, e.failWake(envRow, withRollbackError(err, rollbackWake(d, envRow.Handle)))
	}
	now := e.now()
	exp := now.Add(e.envTTL())
	envRow.State = store.EnvReady
	envRow.SleptAt = nil
	envRow.ExpiresAt = &exp
	if err := store.UpdateEnvironment(e.Store, *envRow); err != nil {
		return nil, e.failWake(envRow, withRollbackError(err, rollbackWake(d, envRow.Handle)))
	}
	if err := store.InsertEnvironmentReceipt(e.Store, envRow.ID, "wake", "succeeded", detail, now); err != nil {
		return nil, err
	}
	return envRow, nil
}

func rollbackWake(d env.Driver, handle string) error {
	return errors.Join(stopServices(d, handle), d.Sleep(handle))
}

func withRollbackError(cause, rollbackErr error) error {
	if rollbackErr == nil {
		return cause
	}
	return fmt.Errorf("%w; rollback failed: %w", cause, rollbackErr)
}

func (e *Engine) failWake(envRow *store.Environment, cause error) error {
	detail := cause.Error()
	if err := store.InsertEnvironmentReceipt(e.Store, envRow.ID, "wake", "failed", detail, e.now()); err != nil {
		return fmt.Errorf("env %d wake failed in sleeping state: %s; recording receipt: %w", envRow.ID, detail, err)
	}
	return fmt.Errorf("env %d wake failed in sleeping state: %s", envRow.ID, detail)
}

func (e *Engine) SleepIdleEnvironments() error {
	if e.EnvIdleSleep <= 0 {
		return nil
	}
	rows, err := store.ListIdleContainerEnvironments(e.Store, e.now(), e.envTTL(), e.EnvIdleSleep)
	if err != nil {
		return err
	}
	var errs []error
	for i := range rows {
		err := e.sleepIdleEnvironment(&rows[i])
		if err != nil && !errors.Is(err, store.ErrEnvironmentBusy) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (e *Engine) sleepIdleEnvironment(envRow *store.Environment) error {
	release, ok := e.beginEnvironmentOperation(envRow.ID)
	if !ok {
		return store.ErrEnvironmentBusy
	}
	defer release()
	reserved, err := store.ReserveIdleEnvironmentSleep(e.Store, envRow.ID, e.now(), e.envTTL(), e.EnvIdleSleep)
	if err != nil {
		return err
	}
	if !reserved {
		return store.ErrEnvironmentBusy
	}
	_, err = e.sleepReservedEnvironment(envRow)
	return err
}

// The per-environment entry is an in-progress marker, not a mutex held over
// runtime I/O. Competing operations fail fast and callers retry on a later poll.
func (e *Engine) beginEnvironmentOperation(id int64) (func(), bool) {
	e.envOperationMu.Lock()
	if e.envOperations == nil {
		e.envOperations = make(map[int64]struct{})
	}
	if _, ok := e.envOperations[id]; ok {
		e.envOperationMu.Unlock()
		return nil, false
	}
	e.envOperations[id] = struct{}{}
	e.envOperationMu.Unlock()
	return func() {
		e.envOperationMu.Lock()
		delete(e.envOperations, id)
		e.envOperationMu.Unlock()
	}, true
}

func (e *Engine) environmentOperationInProgress(id int64) bool {
	e.envOperationMu.Lock()
	defer e.envOperationMu.Unlock()
	_, ok := e.envOperations[id]
	return ok
}

// ReapEnvironments no longer destroys an environment because a TTL
// elapsed (ADR 0063). Archive sleeps; it does not destroy. Recover still
// calls this so a later destroy rule can hook the same path.
func (e *Engine) ReapEnvironments() error {
	return nil
}

func (e *Engine) canProvision() bool {
	return e.Container != nil || e.Env != nil
}

func (e *Engine) imageIdentity() string {
	if c, ok := e.Container.(env.Container); ok && c.Image != "" {
		return c.Image
	}
	return env.KindProcess
}

func (e *Engine) EnsureSessionEnvironment(turnID int64, item snapshot.Item) error {
	if !e.canProvision() {
		return nil
	}
	pin := item.GitPin()
	if pin == "" && !policy.IsProjectKey(item.Repo) {
		return nil
	}
	turn, err := store.GetTurn(e.Store, turnID)
	if err != nil {
		return err
	}
	sess, err := store.GetSession(e.Store, turn.SessionID)
	if err != nil {
		return err
	}
	if sess.Archived {
		return errArchived
	}
	if sess.EnvironmentID == store.DefaultEnvironmentID {
		return nil
	}
	envRow, err := store.GetEnvironment(e.Store, sess.EnvironmentID)
	if err != nil {
		return err
	}
	kind := env.KindProcess
	if e.Container != nil {
		kind = env.KindContainer
	}
	hash := SourceHash(e.imageIdentity(), pin, nil)
	if envRow.Handle != "" && envRow.SourceHash == hash {
		if envRow.State == store.EnvSleeping {
			started := e.now()
			_, err := e.WakeEnvironment(envRow.ID)
			if err != nil {
				return err
			}
			wake := e.now().Sub(started)
			return store.NoteWake(e.Store, turnID, wake)
		}
		if envRow.State != store.EnvReady {
			return fmt.Errorf("env: session environment %d is %s", envRow.ID, envRow.State)
		}
		now := e.now()
		exp := now.Add(e.envTTL())
		envRow.ExpiresAt = &exp
		return store.UpdateEnvironment(e.Store, *envRow)
	}
	if envRow.Handle != "" && envRow.SourceHash != hash {
		d, err := e.driverFor(envRow.Driver)
		if err != nil {
			return err
		}
		_ = stopServices(d, envRow.Handle)
		_ = d.Destroy(envRow.Handle)
		envRow.State = store.EnvExpired
		envRow.Handle = ""
		if err := store.UpdateEnvironment(e.Store, *envRow); err != nil {
			return err
		}
		if err := store.InsertEnvironmentReceipt(e.Store, envRow.ID, "expire", "succeeded", "source changed", e.now()); err != nil {
			return err
		}
		suffix := pin
		if len(suffix) > 12 {
			suffix = suffix[:12]
		}
		spec := EnvSpec{Name: envRow.Name + "-" + suffix, Kind: kind, SourceHash: hash, Repo: item.Repo, Pin: pin}
		e.applySessionSize(&spec, sess)
		return e.replaceSessionEnvironment(sess.ID, envRow, spec)
	}
	if envRow.State == store.EnvExpired {
		// An expired environment is never refilled: its id named the old
		// guest, so the session moves to a new environment (#415).
		spec := EnvSpec{Name: replacementName(envRow.Name, envRow.ID), Kind: kind, SourceHash: hash, Repo: item.Repo, Pin: pin}
		e.applySessionSize(&spec, sess)
		return e.replaceSessionEnvironment(sess.ID, envRow, spec)
	}
	d, err := e.driverFor(kind)
	if err != nil {
		return err
	}
	cpu, mem := SizeLimits(e.SessionSize(sess))
	var handle string
	if sc, ok := d.(env.SpecCreator); ok {
		handle, err = sc.CreateSpec(env.Spec{Name: envRow.Name, CPUMillis: cpu, MemoryBytes: mem})
	} else {
		handle, err = d.Create(envRow.Name)
	}
	if err != nil {
		return err
	}
	setup, err := e.prepareWorkspace(d, handle, item.Repo, pin, hash)
	if setup != nil {
		e.recordCaptures(envRow.ID, env.CaptureSetup, []env.Capture{*setup})
	}
	if err != nil {
		_ = d.Destroy(handle)
		return err
	}
	if err := e.startServicesFor(d, envRow.ID, handle); err != nil {
		_ = d.Destroy(handle)
		return err
	}
	now := e.now()
	exp := now.Add(e.envTTL())
	envRow.Driver = d.Kind()
	envRow.State = store.EnvReady
	envRow.Handle = handle
	envRow.SourceHash = hash
	envRow.ExpiresAt = &exp
	envRow.CPUMillis = cpu
	envRow.MemoryBytes = mem
	return store.UpdateEnvironment(e.Store, *envRow)
}

func (e *Engine) applySessionSize(spec *EnvSpec, sess *store.Session) {
	cpu, mem := SizeLimits(e.SessionSize(sess))
	spec.CPUMillis = cpu
	spec.MemoryBytes = mem
}

// replaceSessionEnvironment provisions spec as a new environment and moves
// the session to it, recording the replacement on old.
func (e *Engine) replaceSessionEnvironment(sessionID int64, old *store.Environment, spec EnvSpec) error {
	created, err := e.ProvisionEnvironment(spec)
	if err != nil {
		// The session still names the old environment; keep the
		// failed setup output where the operator reads it.
		if pe, ok := errors.AsType[*ProvisionError](err); ok {
			e.recordProvision(old.ID, pe.captures)
		}
		return err
	}
	if err := store.ReplaceSessionEnvironment(e.Store, sessionID, old.ID, created.ID, e.now()); err != nil {
		// Another replacement won; do not leave this guest running unowned.
		if d, derr := e.driverFor(created.Driver); derr == nil {
			_ = stopServices(d, created.Handle)
			_ = d.Destroy(created.Handle)
		}
		created.State, created.Handle = store.EnvExpired, ""
		return errors.Join(err, store.UpdateEnvironment(e.Store, *created))
	}
	return nil
}

// replacementName names the environment that replaces id. It drops an
// earlier -r<id> suffix, so repeated replacement keeps names bounded.
func replacementName(name string, id int64) string {
	if i := strings.LastIndex(name, "-r"); i > 0 {
		if _, err := strconv.ParseUint(name[i+2:], 10, 64); err == nil {
			name = name[:i]
		}
	}
	return name + "-r" + strconv.FormatInt(id, 10)
}

// startServices reports started=false when the driver has no services.
func startServices(d env.Driver, handle string) (caps []env.Capture, started bool, err error) {
	s, ok := d.(env.ServiceCtl)
	if !ok {
		return nil, false, nil
	}
	caps, err = s.StartServices(handle)
	return caps, true, err
}

func stopServices(d env.Driver, handle string) error {
	s, ok := d.(env.ServiceCtl)
	if !ok {
		return nil
	}
	return s.StopServices(handle)
}
