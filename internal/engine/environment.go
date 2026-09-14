package engine

import (
	"fmt"
	"time"

	"github.com/sannrox/rusui/internal/env"
	"github.com/sannrox/rusui/internal/store"
)

func (e *Engine) envDriver() env.Driver {
	if e.Env != nil {
		return e.Env
	}
	return env.Process{Root: "environments"}
}

func (e *Engine) envTTL() time.Duration {
	if e.EnvTTL > 0 {
		return e.EnvTTL
	}
	return store.EnvTTL
}

func (e *Engine) CreateEnvironment(name string) (*store.Environment, error) {
	if name == "" || name == store.LocalEnvironmentName {
		return nil, fmt.Errorf("env: name %q reserved", name)
	}
	d := e.envDriver()
	handle, err := d.Create(name)
	if err != nil {
		return nil, err
	}
	now := e.now()
	exp := now.Add(e.envTTL())
	id, err := store.InsertEnvironment(e.Store, store.Environment{
		Name:      name,
		Driver:    d.Kind(),
		State:     store.EnvReady,
		Handle:    handle,
		ExpiresAt: &exp,
		CreatedAt: now,
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
	if err := e.envDriver().Sleep(envRow.Handle); err != nil {
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
	if err := e.envDriver().Wake(envRow.Handle); err != nil {
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
	d := e.envDriver()
	for _, envRow := range stale {
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
