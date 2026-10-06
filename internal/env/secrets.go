package env

import (
	"fmt"
	"os/exec"
	"strings"
)

// SecretDir is outside the workspace so snapshots, WriteFile, and rusui
// read never see injected values (ADR 0066).
const SecretDir = "/run/rusui/secrets"

// SecretKeeper injects plane-held secret values into a running guest.
// Values travel on stdin, never argv.
type SecretKeeper interface {
	PutSecret(handle, id, value string) error
	DeleteSecrets(handle string) error
}

func validSecretFileID(id string) bool {
	if id == "" || len(id) > 32 {
		return false
	}
	for i, c := range id {
		ok := c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || (c == '_' && i > 0)
		if !ok {
			return false
		}
	}
	return id[0] >= 'a' && id[0] <= 'z'
}

func (c Container) PutSecret(handle, id, value string) error {
	k, ok := c.RT.(SecretKeeper)
	if !ok {
		return nil
	}
	return k.PutSecret(handle, id, value)
}

func (c Container) DeleteSecrets(handle string) error {
	k, ok := c.RT.(SecretKeeper)
	if !ok {
		return nil
	}
	return k.DeleteSecrets(handle)
}

func (d DockerCLI) PutSecret(handle, id, value string) error {
	if handle == "" {
		return fmt.Errorf("env: empty handle")
	}
	if !validSecretFileID(id) {
		return fmt.Errorf("env: invalid secret id")
	}
	if _, err := d.run("exec", handle, "mkdir", "-p", SecretDir); err != nil {
		return err
	}
	// The id is on argv; the value is stdin, so it never appears in the
	// host process list.
	cmd := exec.Command(d.bin(), "exec", "-i", "-e", "RUSUI_SECRET_ID="+id, handle, "sh", "-c", "cat > "+SecretDir+"/$RUSUI_SECRET_ID && chmod 400 "+SecretDir+"/$RUSUI_SECRET_ID")
	cmd.Stdin = strings.NewReader(value)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("env: inject secret: %w", err)
	}
	return nil
}

func (d DockerCLI) DeleteSecrets(handle string) error {
	if handle == "" {
		return nil
	}
	_, err := d.run("exec", handle, "rm", "-rf", SecretDir)
	return err
}
