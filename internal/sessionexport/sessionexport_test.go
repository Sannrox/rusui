package sessionexport

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func testKey(t *testing.T) []byte {
	t.Helper()
	key, err := ParseKey(strings.Repeat("ab", 32))
	if err != nil || len(key) < 32 {
		t.Fatalf("key %v %d", err, len(key))
	}
	return key
}

func samplePayload(now time.Time) Payload {
	return Payload{
		Schema:          Schema,
		Kind:            Kind,
		SourcePlaneID:   "plane-src",
		SourceSessionID: 7,
		SourceRepo:      "example/test-repo",
		ExportedAt:      now,
		ExpiresAt:       now.Add(DefaultTTL),
		SourceGitPin:    "abcdef1",
		GuestName:       "shikigami",
		GuestPin:        "acp",
		CreatedAt:       now.Add(-time.Hour),
		Turns:           []TurnMeta{{ID: 3, SeqLo: 1, SeqHi: 4, State: "completed"}},
	}
}

func TestSessionExportHMACRoundTrip(t *testing.T) {
	t.Parallel()
	key := testKey(t)
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	raw, err := Sign(key, samplePayload(now))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, key) || strings.Contains(string(raw), strings.Repeat("ab", 32)) {
		t.Fatal("key leaked into bundle")
	}
	got, err := Verify(key, raw, now)
	if err != nil {
		t.Fatal(err)
	}
	if got.SourceSessionID != 7 || got.SourceGitPin != "abcdef1" || got.GuestName != "shikigami" {
		t.Fatalf("%+v", got)
	}
	if got.GuestPin != "acp" || len(got.Turns) != 1 || got.Turns[0].SeqHi != 4 {
		t.Fatalf("turns %+v", got.Turns)
	}
	if strings.Contains(string(raw), "prompt") && strings.Contains(string(raw), "do not export") {
		t.Fatal("prompt body present")
	}
}

func TestSessionExportRefusesBadMAC(t *testing.T) {
	t.Parallel()
	key := testKey(t)
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	raw, err := Sign(key, samplePayload(now))
	if err != nil {
		t.Fatal(err)
	}
	tampered := bytes.Replace(raw, []byte("abcdef1"), []byte("deadbeef"), 1)
	if _, err := Verify(key, tampered, now); !errors.Is(err, ErrBadMAC) {
		t.Fatalf("got %v", err)
	}
	other, err := ParseKey(strings.Repeat("cd", 32))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(other, raw, now); !errors.Is(err, ErrBadMAC) {
		t.Fatalf("other key %v", err)
	}
}

func TestSessionExportRefusesMissingKey(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	p := samplePayload(now)
	if _, err := Sign(nil, p); !errors.Is(err, ErrNoKey) {
		t.Fatalf("sign %v", err)
	}
	key := testKey(t)
	raw, err := Sign(key, p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(nil, raw, now); !errors.Is(err, ErrNoKey) {
		t.Fatalf("verify %v", err)
	}
}

func TestSessionExportRefusesStrippedExpiry(t *testing.T) {
	t.Parallel()
	key := testKey(t)
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	p := samplePayload(now)
	p.ExpiresAt = time.Time{}
	if _, err := Sign(key, p); !errors.Is(err, ErrNoExpiry) {
		t.Fatalf("sign without expiry %v", err)
	}
	p.ExpiresAt = now.Add(DefaultTTL)
	raw, err := Sign(key, p)
	if err != nil {
		t.Fatal(err)
	}
	stripped := bytes.Replace(raw, []byte(`"expires_at":"`+p.ExpiresAt.Format(time.RFC3339)+`"`), []byte(`"expires_at":"0001-01-01T00:00:00Z"`), 1)
	if _, err := Verify(key, stripped, now); err == nil || (!errors.Is(err, ErrNoExpiry) && !errors.Is(err, ErrBadMAC)) {
		t.Fatalf("stripped %v", err)
	}
}

func TestSessionExportRefusesExpired(t *testing.T) {
	t.Parallel()
	key := testKey(t)
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	p := samplePayload(now)
	raw, err := Sign(key, p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(key, raw, p.ExpiresAt); !errors.Is(err, ErrExpired) {
		t.Fatalf("at expiry %v", err)
	}
	if _, err := Verify(key, raw, p.ExpiresAt.Add(time.Second)); !errors.Is(err, ErrExpired) {
		t.Fatalf("past expiry %v", err)
	}
}

func TestParseKeyShape(t *testing.T) {
	t.Parallel()
	if key, err := ParseKey(""); err != nil || key != nil {
		t.Fatalf("empty %v %v", key, err)
	}
	if _, err := ParseKey("zz"); err == nil {
		t.Fatal("non-hex accepted")
	}
	if _, err := ParseKey(strings.Repeat("ab", 16)); err == nil {
		t.Fatal("short key accepted")
	}
}

func TestVerifyUnknownFieldFailsClosed(t *testing.T) {
	t.Parallel()
	key := testKey(t)
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	raw, err := Sign(key, samplePayload(now))
	if err != nil {
		t.Fatal(err)
	}
	var env Envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(env.Payload, &body); err != nil {
		t.Fatal(err)
	}
	body["extra"] = true
	env.Payload, err = json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	env.MAC = mac(key, env.Payload)
	forged, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(key, forged, now); !errors.Is(err, ErrMalformed) {
		t.Fatalf("unknown field %v", err)
	}
}
