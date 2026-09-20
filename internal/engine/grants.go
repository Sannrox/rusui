package engine

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/sannrox/rusui/internal/store"
)

// IssuePrepareGrant mints a read-only grant for snapshot prepare. It cannot
// push and is not a GitHub or model secret.
func (e *Engine) IssuePrepareGrant(repo string) (string, time.Time, error) {
	if repo == "" {
		return "", time.Time{}, fmt.Errorf("grant: repo required")
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", time.Time{}, err
	}
	token := hex.EncodeToString(raw)
	sum := sha256.Sum256([]byte(token))
	exp := e.now().Add(GrantTTL)
	if err := store.PutGrant(e.Store, store.Grant{
		TokenHash: hex.EncodeToString(sum[:]),
		Kind:      store.GrantPrepare,
		Repo:      repo,
		CanPush:   false,
		ExpiresAt: exp,
	}); err != nil {
		return "", time.Time{}, err
	}
	return token, exp, nil
}
