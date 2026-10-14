package store

import (
	"database/sql"
	"errors"
	"strings"
)

const (
	ExecutionUser  = "user"
	ExecutionSpace = "space"
)

var ErrExecutionModeImmutable = errors.New("execution mode is immutable")

func NormalizeExecutionMode(mode string) string {
	mode = strings.TrimSpace(mode)
	if mode == "" {
		return ExecutionSpace
	}
	return mode
}

func ValidExecutionMode(mode string) bool {
	switch NormalizeExecutionMode(mode) {
	case ExecutionUser, ExecutionSpace:
		return true
	}
	return false
}

func NormalizeActor(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return DefaultTenantID
	}
	return id
}

func SetSessionExecutionTx(tx *sql.Tx, sessionID int64, mode, act string) error {
	if !ValidExecutionMode(mode) {
		return errors.New("execution_mode")
	}
	mode = NormalizeExecutionMode(mode)
	act = strings.TrimSpace(act)
	_, err := tx.Exec(`UPDATE sessions SET execution_mode=?, act=CASE WHEN ?='' THEN COALESCE(NULLIF(act,''), created_by, tenant_id, 'default') ELSE ? END WHERE id=?`,
		mode, act, act, sessionID)
	return err
}

func SetSessionExecution(s *Store, sessionID int64, mode, act string) error {
	return s.Tx(func(tx *sql.Tx) error {
		return SetSessionExecutionTx(tx, sessionID, mode, act)
	})
}

func ChangeSessionExecution(s *Store, sessionID int64, mode, act string) error {
	sess, err := GetSession(s, sessionID)
	if err != nil {
		return err
	}
	if sess != nil {
		return ErrExecutionModeImmutable
	}
	return sql.ErrNoRows
}
