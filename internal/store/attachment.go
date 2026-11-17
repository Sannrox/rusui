package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// PromptAttachment is a file attached to a prompt, stored outside the
// repository until the guest reads it (#508). The store row holds the
// durable bytes, so every plane instance sharing the store can read them
// (#746). A file named by digest under Dir/attachments is this instance's
// cache, which the runner reads by path (#560).
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
			sessionID, seq, f.Name, digest, mime, f.Body, rel, now.UTC().Format(time.RFC3339Nano)); err != nil {
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
		a.Size = bodyLen
		if a.Path != "" {
			a.Path = s.AttachmentFile(a.Digest)
			if st, err := os.Stat(a.Path); err == nil {
				a.Size = int(st.Size())
			}
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		if out[i].Path == "" {
			continue
		}
		// A body that cannot be cached here leaves the path unresolved; the
		// runner's read then fails the turn instead of dropping the file.
		_ = s.cacheAttachment(out[i].ID, out[i].Digest)
		if st, err := os.Stat(out[i].Path); err == nil {
			out[i].Size = int(st.Size())
		}
	}
	return out, nil
}

// cacheAttachment writes the stored body of attachment id to this
// instance's cache file when the file is missing, for example after
// another instance stored it. The digest is checked before the file
// appears; a row stored before #746 without a body and without a local
// file cannot be served here.
func (s *Store) cacheAttachment(id int64, digest string) error {
	path := s.AttachmentFile(digest)
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	var body []byte
	if err := s.DB.QueryRow(`SELECT body FROM prompt_attachments WHERE id=?`, id).Scan(&body); err != nil {
		return err
	}
	if len(body) == 0 {
		return fmt.Errorf("store: attachment %s has no stored body and no file on this instance", digest)
	}
	sum := sha256.Sum256(body)
	if hex.EncodeToString(sum[:]) != digest {
		return fmt.Errorf("store: attachment %s body does not match its digest", digest)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+digest+".*")
	if err != nil {
		return err
	}
	_, werr := tmp.Write(body)
	cerr := tmp.Close()
	if werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Rename(tmp.Name(), path)
	}
	if werr != nil {
		_ = os.Remove(tmp.Name())
	}
	return werr
}

// SyncAttachmentCache reconciles this instance's attachment cache with the
// store. It copies a cached file into a row stored before #746 without a
// body, and removes cache files no row references, so an attachment that
// retention removed on another instance does not linger here. Every
// instance runs it for its own Dir; it is safe to repeat and to run on
// several instances at once.
// attachmentSweepGrace is how old an unreferenced cache file must be
// before SyncAttachmentCache removes it.
const attachmentSweepGrace = time.Hour

func SyncAttachmentCache(s *Store) error {
	if s.Dir == "" {
		return nil
	}
	dir := filepath.Join(s.Dir, "attachments")
	if err := backfillAttachmentBodies(s); err != nil {
		return err
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		// A young file may belong to a write whose row has not committed;
		// a binary from before #746 holds those bytes only on disk.
		if info, err := e.Info(); err != nil || time.Since(info.ModTime()) < attachmentSweepGrace {
			continue
		}
		var n int
		if err := s.DB.QueryRow(`SELECT COUNT(*) FROM prompt_attachments WHERE digest=?`, e.Name()).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
	return nil
}

// backfillAttachmentBodies stores the bytes of rows written before #746,
// whose body is empty, from this instance's cache. A file whose bytes do
// not match the digest is left alone.
func backfillAttachmentBodies(s *Store) error {
	rows, err := s.DB.Query(`SELECT DISTINCT digest FROM prompt_attachments WHERE length(body) = 0 AND path != ''`)
	if err != nil {
		return err
	}
	var digests []string
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			_ = rows.Close()
			return err
		}
		digests = append(digests, d)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return err
	}
	for _, d := range digests {
		body, err := os.ReadFile(s.AttachmentFile(d))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		sum := sha256.Sum256(body)
		if hex.EncodeToString(sum[:]) != d || len(body) == 0 {
			continue
		}
		if _, err := s.DB.Exec(`UPDATE prompt_attachments SET body=? WHERE digest=? AND length(body) = 0`, body, d); err != nil {
			return err
		}
	}
	return nil
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
