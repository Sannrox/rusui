package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"testing"

	"github.com/sannrox/rusui/internal/core"
	"github.com/sannrox/rusui/internal/engine"
)

func TestApplySessionDraftRequiresOperatorAndProposal(t *testing.T) {
	_, hs, e := consoleEnv(t)
	sid, err := e.StartRun("test", "ordinary", "")
	if err != nil {
		t.Fatal(err)
	}
	path := hs.URL + "/sessions/" + strconv.FormatInt(sid, 10) + "/draft-actions"
	if code := postJSONStatus(t, path, "wsec", `{}`); code != http.StatusUnauthorized {
		t.Fatalf("worker %d", code)
	}
	if code := postJSONStatus(t, path, "op-tok", `{}`); code != http.StatusConflict {
		t.Fatalf("no proposal %d", code)
	}
}

func TestSessionGetReportsDraftPermissionDenied(t *testing.T) {
	_, hs, e := consoleEnv(t)
	e.Core = &core.Fake{Denied: core.DeniedError{Message: "namespace write required"}}
	sid, err := e.StartRun("test", "describe the sales domain", "")
	if err != nil {
		t.Fatal(err)
	}
	c, err := e.Claim("example/test-repo")
	if err != nil || c == nil {
		t.Fatal(err)
	}
	draft := &engine.DraftRequest{
		Namespace: "sales", BranchID: fmt.Sprintf("interview-%d", sid), ParentRevisionDigest: "sha256:published",
		Members: []engine.DraftMember{
			{MemberKind: "object_type", MemberID: "Customer", DefinitionJSON: `{"name":"Customer","source_turn":"interview-1#turn-1"}`},
			{MemberKind: "link_type", MemberID: "PlacedBy", DefinitionJSON: `{"name":"PlacedBy","source_turn":"interview-1#turn-2"}`},
		},
	}
	a := engine.Artifact{
		SchemaVersion: 1, Repo: c.Job.Repo, Item: c.Job.Item, ItemKind: c.Job.ItemKind,
		ClaimedRevision: c.Job.ClaimedRevision, SnapshotHash: c.ItemHash, MainSHA: c.Snapshot.MainSHA,
		Verdict: "keep", Result: &engine.TaskResult{SchemaVersion: engine.ResultSchema, SourceHash: c.ItemHash, Draft: draft},
	}
	if _, err := e.Complete(c.Job.ID, c.Job.LeaseGeneration, c.Job.ClaimedRevision, a); err != nil {
		t.Fatal(err)
	}
	path := hs.URL + "/sessions/" + strconv.FormatInt(sid, 10) + "/draft-actions"
	req, err := http.NewRequest(http.MethodPost, path, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer op-tok")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("apply %d %s", res.StatusCode, body)
	}
	var rec engine.DraftApplyReceipt
	if err := json.Unmarshal(body, &rec); err != nil || !rec.Refused || rec.Code != "permission_denied" {
		t.Fatalf("receipt %s", body)
	}
	get, err := http.NewRequest(http.MethodGet, hs.URL+"/sessions/"+strconv.FormatInt(sid, 10), nil)
	if err != nil {
		t.Fatal(err)
	}
	get.Header.Set("Authorization", "Bearer op-tok")
	gres, err := http.DefaultClient.Do(get)
	if err != nil {
		t.Fatal(err)
	}
	gb, _ := io.ReadAll(gres.Body)
	_ = gres.Body.Close()
	if gres.StatusCode != http.StatusOK {
		t.Fatalf("get %d %s", gres.StatusCode, gb)
	}
	var page map[string]any
	if err := json.Unmarshal(gb, &page); err != nil {
		t.Fatal(err)
	}
	draftPage, _ := page["draft"].(map[string]any)
	if draftPage == nil || draftPage["refused"] != true || draftPage["code"] != "permission_denied" {
		t.Fatalf("session %s", gb)
	}
}
