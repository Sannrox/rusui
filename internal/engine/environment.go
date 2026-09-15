package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/sannrox/rusui/internal/env"
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
	if err := e.maybeSetup(d, handle, spec.SourceHash); err != nil {
		_ = d.Destroy(handle)
		return nil, err
	}
	if err := startServices(d, handle); err != nil {
		_ = d.Destroy(handle)
		return nil, err
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
	return store.GetEnvironment(e.Store, id)
}

func (e *Engine) SleepEnvironment(id int64) (*store.Environment, error) {
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
	d, err := e.driverFor(envRow.Driver)
	if err != nil {
		return nil, err
	}
	if err := stopServices(d, envRow.Handle); err != nil {
		return nil, err
	}
	if err := d.Sleep(envRow.Handle); err != nil {
		return nil, err
	}
	now := e.now()
	envRow.State = store.EnvSleeping
	envRow.SleptAt = &now
	if err := store.UpdateEnvironment(e.Store, *envRow); err != nil {
		return nil, err
	}
	return envRow, nil
}

func (e *Engine) WakeEnvironment(id int64) (*store.Environment, error) {
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
	if envRow.ExpiresAt != nil && !e.now().Before(*envRow.ExpiresAt) {
		return nil, fmt.Errorf("env: expired")
	}
	d, err := e.driverFor(envRow.Driver)
	if err != nil {
		return nil, err
	}
	if err := d.Wake(envRow.Handle); err != nil {
		return nil, err
	}
	if r, ok := d.(env.Resumer); ok {
		if err := r.Resume(envRow.Handle); err != nil {
			return nil, err
		}
	}
	if err := startServices(d, envRow.Handle); err != nil {
		return nil, err
	}
	now := e.now()
	exp := now.Add(e.envTTL())
	envRow.State = store.EnvReady
	envRow.SleptAt = nil
	envRow.ExpiresAt = &exp
	if err := store.UpdateEnvironment(e.Store, *envRow); err != nil {
		return nil, err
	}
	return envRow, nil
}

func (e *Engine) ReapEnvironments() error {
	stale, err := store.ListExpiredEnvironments(e.Store, e.now())
	if err != nil {
		return err
	}
	for _, envRow := range stale {
		d, err := e.driverFor(envRow.Driver)
		if err != nil {
			e.exception("expire environment " + envRow.Name + ": " + err.Error())
			continue
		}
		_ = stopServices(d, envRow.Handle)
		if err := d.Destroy(envRow.Handle); err != nil {
			e.exception("expire environment " + envRow.Name + ": " + err.Error())
			continue
		}
		envRow.State = store.EnvExpired
		envRow.Handle = ""
		if err := store.UpdateEnvironment(e.Store, envRow); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) maybeSetup(d env.Driver, handle, hash string) error {
	p, ok := d.(env.Preparer)
	if !ok {
		return nil
	}
	if hash != "" {
		prepared, err := store.HasPreparedSourceHash(e.Store, hash)
		if err != nil {
			return err
		}
		if prepared {
			return nil
		}
	}
	return p.Setup(handle, hash)
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
	if pin == "" {
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
		suffix := pin
		if len(suffix) > 12 {
			suffix = suffix[:12]
		}
		created, err := e.ProvisionEnvironment(EnvSpec{Name: envRow.Name + "-" + suffix, Kind: kind, SourceHash: hash})
		if err != nil {
			return err
		}
		return store.SetSessionEnvironment(e.Store, sess.ID, created.ID)
	}
	d, err := e.driverFor(kind)
	if err != nil {
		return err
	}
	var handle string
	if sc, ok := d.(env.SpecCreator); ok {
		handle, err = sc.CreateSpec(env.Spec{Name: envRow.Name})
	} else {
		handle, err = d.Create(envRow.Name)
	}
	if err != nil {
		return err
	}
	if err := e.maybeSetup(d, handle, hash); err != nil {
		_ = d.Destroy(handle)
		return err
	}
	if err := startServices(d, handle); err != nil {
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
	return store.UpdateEnvironment(e.Store, *envRow)
}

func startServices(d env.Driver, handle string) error {
	s, ok := d.(env.ServiceCtl)
	if !ok {
		return nil
	}
	return s.StartServices(handle)
}

func stopServices(d env.Driver, handle string) error {
	s, ok := d.(env.ServiceCtl)
	if !ok {
		return nil
	}
	return s.StopServices(handle)
}
