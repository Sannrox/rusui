package engine

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/sannrox/rusui/internal/env"
	"github.com/sannrox/rusui/internal/policy"
	"github.com/sannrox/rusui/internal/store"
)

const secretEnvPrefix = "RUSUI_SECRET_"

// SecretsFromEnv reads plane-held secret values from RUSUI_SECRET_<ID>.
// The suffix is lowercased to the policy secret id.
func SecretsFromEnv(environ []string) map[string]string {
	out := map[string]string{}
	for _, e := range environ {
		k, v, ok := strings.Cut(e, "=")
		if !ok || !strings.HasPrefix(k, secretEnvPrefix) {
			continue
		}
		id := strings.ToLower(strings.TrimPrefix(k, secretEnvPrefix))
		if policy.ValidSecretID(id) {
			out[id] = v
		}
	}
	return out
}

func (e *Engine) checkRequestedSecrets(proj policy.Project) error {
	if err := policy.AllowSecrets(proj.Secrets, e.RequestedSecrets); err != nil {
		return fmt.Errorf("%w: %w", ErrSecret, err)
	}
	return nil
}

// AssignmentSecrets is the id-to-value map a claimed turn may present.
// Empty RequestedSecrets uses the project allowlist. Missing plane values
// are omitted.
func (e *Engine) AssignmentSecrets(p policy.Project) map[string]string {
	ids := p.Secrets
	if len(e.RequestedSecrets) > 0 {
		ids = e.RequestedSecrets
	}
	if len(ids) == 0 || len(e.Secrets) == 0 {
		return nil
	}
	out := map[string]string{}
	for _, id := range ids {
		if v, ok := e.Secrets[id]; ok && v != "" {
			out[id] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (e *Engine) injectSecrets(envRow *store.Environment, repo string, turnID int64) error {
	if envRow == nil || envRow.Handle == "" {
		return nil
	}
	if repo == "" {
		if sess, err := store.GetSessionByEnvironment(e.Store, envRow.ID); err == nil {
			repo = sess.Repo
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	p, ok := e.projectForSecretRepo(repo)
	if !ok {
		return nil
	}
	if err := e.checkRequestedSecrets(p); err != nil {
		return err
	}
	ids := p.Secrets
	if len(e.RequestedSecrets) > 0 {
		ids = e.RequestedSecrets
	}
	d, err := e.driverFor(envRow.Driver)
	if err != nil {
		return err
	}
	k, ok := d.(env.SecretKeeper)
	if !ok {
		return nil
	}
	for _, id := range ids {
		v, have := e.Secrets[id]
		if !have || v == "" {
			continue
		}
		if err := k.PutSecret(envRow.Handle, id, v); err != nil {
			return err
		}
		detail := id
		if turnID > 0 {
			detail = fmt.Sprintf("%s turn %d", id, turnID)
		}
		if err := store.InsertEnvironmentReceipt(e.Store, envRow.ID, "secret", "succeeded", detail, e.now()); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) stripSecrets(envRow *store.Environment) error {
	if envRow == nil || envRow.Handle == "" {
		return nil
	}
	d, err := e.driverFor(envRow.Driver)
	if err != nil {
		return err
	}
	k, ok := d.(env.SecretKeeper)
	if !ok {
		return nil
	}
	return k.DeleteSecrets(envRow.Handle)
}

func (e *Engine) projectForSecretRepo(repo string) (policy.Project, bool) {
	if repo == "" {
		return policy.Project{}, false
	}
	if p, ok := e.PolicySnapshot().ProjectForRepo(repo); ok {
		return p, true
	}
	if slug, ok := policy.ParseProjectKey(repo); ok {
		return e.PolicySnapshot().Project(slug)
	}
	return policy.Project{}, false
}
