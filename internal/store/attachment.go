package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"
)

// PromptAttachment is a file attached to a prompt, stored outside the
// repository until the guest reads it (#508).
type PromptAttachment struct {
	ID        int64
	SessionID int64
	Seq       int
	Revision  int
	Name      string
	Digest    string
	MIME      string
	Body      []byte
}

// PromptFile is the operator-supplied attachment before it is stored.
type PromptFile struct {
	Name string
	Body []byte
}

func InsertPromptAttachmentsTx(tx *sql.Tx, sessionID int64, seq int, files []PromptFile, now time.Time) error {
	for _, f := range files {
		sum := sha256.Sum256(f.Body)
		digest := hex.EncodeToString(sum[:])
		mime := AttachmentMIME(f.Name, f.Body)
		if _, err := tx.Exec(`INSERT INTO prompt_attachments (session_id, seq, revision, name, digest, mime, body, created_at) VALUES (?,?,0,?,?,?,?,?)`,
			sessionID, seq, f.Name, digest, mime, f.Body, now.UTC().Format(time.RFC3339Nano)); err != nil {
			return err
		}
	}
	return nil
}

func BindPromptAttachmentsTx(tx *sql.Tx, sessionID int64, seq, revision int) error {
	_, err := tx.Exec(`UPDATE prompt_attachments SET revision=? WHERE session_id=? AND seq=?`, revision, sessionID, seq)
	return err
}

func ListPromptAttachments(s *Store, sessionID int64, revision int) ([]PromptAttachment, error) {
	rows, err := s.DB.Query(`SELECT id, session_id, seq, revision, name, digest, mime, body FROM prompt_attachments WHERE session_id=? AND revision=? ORDER BY id`, sessionID, revision)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []PromptAttachment
	for rows.Next() {
		var a PromptAttachment
		if err := rows.Scan(&a.ID, &a.SessionID, &a.Seq, &a.Revision, &a.Name, &a.Digest, &a.MIME, &a.Body); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func AttachmentName(name string) (string, error) {
	if name == "" || strings.ContainsAny(name, `/\`) || name == "." || name == ".." || strings.Contains(name, "..") {
		return "", fmt.Errorf("path")
	}
	if filepath.Base(name) != name {
		return "", fmt.Errorf("path")
	}
	return name, nil
}

func AttachmentMIME(name string, body []byte) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".pdf":
		return "application/pdf"
	}
	return http.DetectContentType(body)
}
