package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// PromptAttachment is a file attached to a prompt, stored outside the
// repository until the guest reads it (#508). Bytes live on disk; SQLite
// keeps digest and path (#560).
type PromptAttachment struct {
	ID        int64
	SessionID int64
	Seq       int
	Revision  int
	Name      string
	Digest    string
	MIME      string
	Path      string
	Size      int
	Body      []byte
}

// PromptFile is the operator-supplied attachment before it is stored.
type PromptFile struct {
	Name string
	Body []byte
}

func (s *Store) AttachmentFile(digest string) string {
	return filepath.Join(s.Dir, "attachments", digest)
}

func InsertPromptAttachmentsTx(s *Store, tx *sql.Tx, sessionID int64, seq int, files []PromptFile, now time.Time) ([]PromptAttachment, error) {
	if err := os.MkdirAll(filepath.Join(s.Dir, "attachments"), 0o700); err != nil {
		return nil, err
	}
	out := make([]PromptAttachment, 0, len(files))
	for _, f := range files {
		sum := sha256.Sum256(f.Body)
		digest := hex.EncodeToString(sum[:])
		mime := AttachmentMIME(f.Name, f.Body)
		rel := digest
		abs := s.AttachmentFile(digest)
		if err := os.WriteFile(abs, f.Body, 0o600); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(`INSERT INTO prompt_attachments (session_id, seq, revision, name, digest, mime, body, path, created_at) VALUES (?,?,0,?,?,?,?,?,?)`,
			sessionID, seq, f.Name, digest, mime, []byte{}, rel, now.UTC().Format(time.RFC3339Nano)); err != nil {
			return nil, err
		}
		out = append(out, PromptAttachment{SessionID: sessionID, Seq: seq, Name: f.Name, Digest: digest, MIME: mime, Path: abs, Size: len(f.Body)})
	}
	return out, nil
}

func BindPromptAttachmentsTx(tx *sql.Tx, sessionID int64, seq, revision int) error {
	_, err := tx.Exec(`UPDATE prompt_attachments SET revision=? WHERE session_id=? AND seq=?`, revision, sessionID, seq)
	return err
}

func ListPromptAttachments(s *Store, sessionID int64, revision int) ([]PromptAttachment, error) {
	rows, err := s.DB.Query(`SELECT id, session_id, seq, revision, name, digest, mime, COALESCE(path, ''), length(body) FROM prompt_attachments WHERE session_id=? AND revision=? ORDER BY id`, sessionID, revision)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []PromptAttachment
	for rows.Next() {
		var a PromptAttachment
		var bodyLen int
		if err := rows.Scan(&a.ID, &a.SessionID, &a.Seq, &a.Revision, &a.Name, &a.Digest, &a.MIME, &a.Path, &bodyLen); err != nil {
			return nil, err
		}
		if a.Path != "" {
			a.Path = s.AttachmentFile(a.Digest)
		}
		a.Size = bodyLen
		if a.Path != "" {
			if st, err := os.Stat(a.Path); err == nil {
				a.Size = int(st.Size())
			}
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// extractPromptAttachmentBodies copies leftover SQLite BLOBs onto disk and
// clears the column so upgraded rows match the v43 layout.
func extractPromptAttachmentBodies(s *Store) error {
	if err := os.MkdirAll(filepath.Join(s.Dir, "attachments"), 0o700); err != nil {
		return err
	}
	rows, err := s.DB.Query(`SELECT id, digest, body FROM prompt_attachments WHERE length(body) > 0`)
	if err != nil {
		return err
	}
	type leftover struct {
		id     int64
		digest string
		body   []byte
	}
	var todo []leftover
	for rows.Next() {
		var r leftover
		if err := rows.Scan(&r.id, &r.digest, &r.body); err != nil {
			_ = rows.Close()
			return err
		}
		todo = append(todo, r)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return err
	}
	for _, r := range todo {
		if err := os.WriteFile(s.AttachmentFile(r.digest), r.body, 0o600); err != nil {
			return err
		}
		if _, err := s.DB.Exec(`UPDATE prompt_attachments SET path=?, body=? WHERE id=?`, r.digest, []byte{}, r.id); err != nil {
			return err
		}
	}
	return nil
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
	case ".txt":
		return "text/plain"
	case ".md", ".markdown":
		return "text/markdown"
	case ".wav":
		return "audio/wav"
	}
	return CanonicalMIME(http.DetectContentType(body))
}

// CanonicalMIME lowercases a media type and drops parameters such as charset.
func CanonicalMIME(mime string) string {
	mime = strings.ToLower(strings.TrimSpace(mime))
	if i := strings.IndexByte(mime, ';'); i >= 0 {
		mime = strings.TrimSpace(mime[:i])
	}
	return mime
}
