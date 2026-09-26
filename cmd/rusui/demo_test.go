package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDemoMainWritesSampleReceipt(t *testing.T) {
	dir := t.TempDir()
	var out, errw bytes.Buffer
	if code := demoMain([]string{"-dir", dir, "-keep"}, &out, &errw); code != 0 {
		t.Fatalf("demoMain=%d stderr=%s stdout=%s", code, errw.String(), out.String())
	}
	got := out.String()
	if !strings.Contains(got, "SAMPLE rusui demo") {
		t.Fatalf("missing sample banner: %s", got)
	}
	if !strings.Contains(got, "sample/demo#1") || !strings.Contains(got, `"verdict":"keep"`) {
		t.Fatalf("missing sample receipt: %s", got)
	}
	owned, err := demoOwnedDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(owned, "SAMPLE")); err != nil {
		t.Fatalf("sample marker: %v", err)
	}
}

func TestDemoMainRemovesOwnedStateAndLeavesParent(t *testing.T) {
	dir := t.TempDir()
	keep := filepath.Join(dir, "keep.txt")
	if err := os.WriteFile(keep, []byte("stay\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errw bytes.Buffer
	if code := demoMain([]string{"-dir", dir}, &out, &errw); code != 0 {
		t.Fatalf("demoMain=%d stderr=%s", code, errw.String())
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("parent file removed: %v", err)
	}
	matches, err := filepath.Glob(filepath.Join(dir, "rusui-sample-demo-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("owned sample dir remained: %v", matches)
	}
	if !strings.Contains(out.String(), "removed sample state") {
		t.Fatalf("missing cleanup line: %s", out.String())
	}
}

func demoOwnedDir(parent string) (string, error) {
	matches, err := filepath.Glob(filepath.Join(parent, "rusui-sample-demo-*"))
	if err != nil {
		return "", err
	}
	if len(matches) != 1 {
		return "", fmt.Errorf("owned sample dirs %v", matches)
	}
	return matches[0], nil
}

func TestDemoMainUsage(t *testing.T) {
	var out, errw bytes.Buffer
	if code := demoMain([]string{"extra"}, &out, &errw); code != 2 {
		t.Fatalf("code=%d stderr=%s", code, errw.String())
	}
}

func TestDemoMainRejectsFileDir(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errw bytes.Buffer
	if code := demoMain([]string{"-dir", path}, &out, &errw); code != 1 {
		t.Fatalf("code=%d stderr=%s", code, errw.String())
	}
	if !strings.Contains(errw.String(), "not a directory") {
		t.Fatalf("stderr=%s", errw.String())
	}
}
