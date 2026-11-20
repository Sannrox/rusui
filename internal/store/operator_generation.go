package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
)

// HashOperatorToken is the durable identity of the current operator token.
// Rotation changes it and ends outstanding share grants.
func HashOperatorToken(tok string) string {
	sum := sha256.Sum256([]byte("operator-gen-v1:" + tok))
	return hex.EncodeToString(sum[:])
}

// EnsureOperatorGeneration binds the current operator token to a generation
// counter. A changed hash bumps the generation and revokes live share grants
// minted under the previous one.
func EnsureOperatorGeneration(s *Store, tokenHash string) (int, error) {
	if tokenHash == "" {
		return 0, nil
	}
	var gen int
	err := s.Tx(func(tx *sql.Tx) error {
		gen = 0
		var storedHash string
		var storedGen int
		err := tx.QueryRow(`SELECT token_hash, generation FROM operator_generation WHERE id=1`).Scan(&storedHash, &storedGen)
		if errors.Is(err, sql.ErrNoRows) {
			if _, err := tx.Exec(`INSERT INTO operator_generation (id, token_hash, generation) VALUES (1, ?, 1)`, tokenHash); err != nil {
				return err
			}
			gen = 1
			return nil
		}
		if err != nil {
			return err
		}
		if storedHash == tokenHash {
			gen = storedGen
			return nil
		}
		gen = storedGen + 1
		if _, err := tx.Exec(`UPDATE operator_generation SET token_hash=?, generation=? WHERE id=1`, tokenHash, gen); err != nil {
			return err
		}
		_, err = tx.Exec(`UPDATE preview_grants SET revoked=1 WHERE grant_class=? AND revoked=0 AND operator_generation<>?`, PreviewClassShare, gen)
		return err
	})
	return gen, err
}
