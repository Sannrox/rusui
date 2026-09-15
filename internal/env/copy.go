package env

import (
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

func copyDir(src, dest string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." || rel == ".rusui-snapshot" {
			return nil
		}
		out := filepath.Join(dest, rel)
		if d.IsDir() {
			return os.MkdirAll(out, 0o700)
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		if err := os.MkdirAll(filepath.Dir(out), 0o700); err != nil {
			return err
		}
		f, err := os.OpenFile(out, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(f, in)
		return err
	})
}
