package core

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTTPCreateBranchAndPermissionDenied(t *testing.T) {
	var sawAuth, sawPath string
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawPath = r.URL.Path
		sawAuth = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		var req map[string]string
		_ = json.Unmarshal(body, &req)
		if req["branchId"] != "interview-7" || req["namespace"] != "sales" {
			t.Errorf("wire %+v", req)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"branch":{"namespace":"sales","branchId":"interview-7","headRevisionDigest":"sha256:h"}}`))
	}))
	t.Cleanup(hs.Close)
	cli := NewHTTP(hs.URL, "plane-tok")
	b, err := cli.CreateDefinitionBranch(CreateBranchRequest{Namespace: "sales", BranchID: "interview-7", ParentRevisionDigest: "sha256:p", IdempotencyKey: "k"})
	if err != nil {
		t.Fatal(err)
	}
	if b.HeadRevisionDigest != "sha256:h" || sawPath != "/sekai.SekaiService/CreateDefinitionBranch" || sawAuth != "Bearer plane-tok" {
		t.Fatalf("branch %+v path %s auth %s", b, sawPath, sawAuth)
	}

	deny := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"code":"permission_denied","message":"namespace write required"}`))
	}))
	t.Cleanup(deny.Close)
	_, err = NewHTTP(deny.URL, "plane-tok").CreateDefinitionBranch(CreateBranchRequest{Namespace: "sales", BranchID: "x"})
	if !IsPermissionDenied(err) {
		t.Fatalf("err %v", err)
	}
}

func TestHTTPGetBranchNotFoundAndLintCamelCase(t *testing.T) {
	missing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"code":"not_found","message":"definition branch unavailable"}`))
	}))
	t.Cleanup(missing.Close)
	_, err := NewHTTP(missing.URL, "plane-tok").GetDefinitionBranch("sales", "interview-7")
	if !IsNotFound(err) {
		t.Fatalf("err %v", err)
	}

	lint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"revisionDigest":"sha256:h","complete":true,"findings":[{"rule":"unowned_object_type","subjectKind":"object_type","subjectId":"Customer","severity":"warning"}]}`))
	}))
	t.Cleanup(lint.Close)
	rep, err := NewHTTP(lint.URL, "plane-tok").GetOntologyLintReport("sales", "interview-7")
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Findings) != 1 || rep.Findings[0].SubjectKind != "object_type" || rep.Findings[0].SubjectID != "Customer" {
		t.Fatalf("lint %+v", rep)
	}
}
