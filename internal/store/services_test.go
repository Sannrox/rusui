package store

import (
	"testing"
	"time"
)

func TestPreviewGrantServiceNameAndNull(t *testing.T) {
	s := openTestStore(t)
	now := time.Now().UTC()
	expires := now.Add(time.Hour)
	envID, err := InsertEnvironment(s, Environment{
		Name: "svc-grant", Driver: "process", State: EnvReady,
		Handle: "ws", ExpiresAt: &expires, CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := PutPreviewGrant(s, PreviewGrant{
		TokenHash: "named", SessionID: 1, EnvironmentID: envID,
		Handle: "ws", Port: 3000, ServiceName: "web", ExpiresAt: now.Add(time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	got, ok, err := GetPreviewGrant(s, "named")
	if err != nil || !ok || got.ServiceName != "web" || got.Port != 3000 {
		t.Fatalf("named %+v ok=%v err=%v", got, ok, err)
	}
	if err := PutPreviewGrant(s, PreviewGrant{
		TokenHash: "raw", SessionID: 1, EnvironmentID: envID,
		Handle: "ws", Port: 4000, ExpiresAt: now.Add(time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	got, ok, err = GetPreviewGrant(s, "raw")
	if err != nil || !ok || got.ServiceName != "" || got.Port != 4000 {
		t.Fatalf("raw %+v ok=%v err=%v", got, ok, err)
	}
	if _, err := s.DB.Exec(`INSERT INTO preview_grants (token_hash, session_id, environment_id, handle, port, expires_at, revoked)
VALUES ('legacy', 1, ?, 'ws', 5000, ?, 0)`, envID, now.Add(time.Minute).Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	got, ok, err = GetPreviewGrant(s, "legacy")
	if err != nil || !ok || got.ServiceName != "" || got.Port != 5000 {
		t.Fatalf("legacy null %+v ok=%v err=%v", got, ok, err)
	}
}

func TestReplaceEnvironmentServicesOnEachStart(t *testing.T) {
	s := openTestStore(t)
	now := time.Now().UTC()
	envID, err := InsertEnvironment(s, Environment{
		Name: "svc-state", Driver: "process", State: EnvReady, Handle: "ws", CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := ReplaceEnvironmentServices(s, envID, EnvironmentServiceSet{
		Services: []EnvironmentService{{Name: "web", Port: 3000, State: "starting"}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := ReplaceEnvironmentServices(s, envID, EnvironmentServiceSet{
		Services: []EnvironmentService{{Name: "web", Port: 3000, State: "healthy"}, {Name: "worker", State: "exited"}},
	}); err != nil {
		t.Fatal(err)
	}
	got, err := ListEnvironmentServices(s, envID)
	if err != nil || got.ParseError != "" || len(got.Services) != 2 {
		t.Fatalf("%+v %v", got, err)
	}
	if got.Services[0].Name != "web" || got.Services[0].State != "healthy" || got.Services[1].Name != "worker" {
		t.Fatalf("%+v", got.Services)
	}
	if err := ReplaceEnvironmentServices(s, envID, EnvironmentServiceSet{ParseError: "env: services.yaml: field portal not found"}); err != nil {
		t.Fatal(err)
	}
	got, err = ListEnvironmentServices(s, envID)
	if err != nil || got.ParseError == "" || len(got.Services) != 0 {
		t.Fatalf("parse error should start none: %+v %v", got, err)
	}
}
