package engine

import (
	"os"
	"path/filepath"
	"strings"
)

// ReadPlaneHooks loads pre-clone and pre-setup scripts from
// dir/<project-slug>/pre-clone and dir/<project-slug>/pre-setup.
func ReadPlaneHooks(dir string) (preClone, preSetup map[string]string, err error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	preClone, preSetup = map[string]string{}, map[string]string{}
	for _, ent := range entries {
		if !ent.IsDir() {
			continue
		}
		slug := ent.Name()
		if body, ok, err := readHookFile(filepath.Join(dir, slug, "pre-clone")); err != nil {
			return nil, nil, err
		} else if ok {
			preClone[slug] = body
		}
		if body, ok, err := readHookFile(filepath.Join(dir, slug, "pre-setup")); err != nil {
			return nil, nil, err
		} else if ok {
			preSetup[slug] = body
		}
	}
	if len(preClone) == 0 {
		preClone = nil
	}
	if len(preSetup) == 0 {
		preSetup = nil
	}
	return preClone, preSetup, nil
}

func readHookFile(path string) (string, bool, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, err
	}
	return strings.ReplaceAll(string(b), "\r\n", "\n"), true, nil
}

// ReadAdmitHooks finds dir/<project-slug>/admit for every project. The
// plane executes the file itself, so it must be executable; the map holds
// paths, not bodies.
func ReadAdmitHooks(dir string) (map[string]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out map[string]string
	for _, ent := range entries {
		if !ent.IsDir() {
			continue
		}
		path := filepath.Join(dir, ent.Name(), "admit")
		// Lstat: any entry named admit counts, so a dangling symlink makes
		// every start refuse instead of skipping admission.
		if _, err := os.Lstat(path); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		if out == nil {
			out = map[string]string{}
		}
		out[ent.Name()] = path
	}
	return out, nil
}
