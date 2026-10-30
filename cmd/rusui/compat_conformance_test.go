package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/sannrox/rusui/internal/compat"
	"github.com/sannrox/rusui/internal/store"
)

func TestCompatConformanceMainPrintsIdentity(t *testing.T) {
	old := GitCommit
	t.Cleanup(func() { GitCommit = old })
	GitCommit = "testcommit"
	var out, errOut bytes.Buffer
	if code := compatConformanceMain([]string{"-dir", t.TempDir()}, &out, &errOut); code != 0 {
		t.Fatalf("code %d err %s out %s", code, errOut.String(), out.String())
	}
	var rep compat.Report
	if err := json.Unmarshal(out.Bytes(), &rep); err != nil {
		t.Fatal(err)
	}
	if rep.Commit != "testcommit" || rep.Schema != store.CurrentSchema || rep.Policy != compat.PolicyVersion {
		t.Fatalf("%+v", rep)
	}
	if !strings.Contains(out.String(), `"sqlite-schema"`) {
		t.Fatalf("missing catalog: %s", out.String())
	}
}
