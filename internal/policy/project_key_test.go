package policy

import "testing"

func TestProjectKeyRoundTrip(t *testing.T) {
	key := ProjectKey("empty")
	if key != "project:empty" {
		t.Fatalf("%q", key)
	}
	slug, ok := ParseProjectKey(key)
	if !ok || slug != "empty" {
		t.Fatalf("%q %v", slug, ok)
	}
	if !IsProjectKey(key) {
		t.Fatal("IsProjectKey")
	}
	if IsProjectKey("example/test-repo") {
		t.Fatal("bound repo treated as project key")
	}
	if _, ok := ParseProjectKey("project:"); ok {
		t.Fatal("empty slug")
	}
	if _, ok := ParseProjectKey("project:Empty"); ok {
		t.Fatal("invalid slug")
	}
}
