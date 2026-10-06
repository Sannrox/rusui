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
