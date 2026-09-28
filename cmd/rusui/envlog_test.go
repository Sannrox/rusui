package main

import (
	"strings"
	"testing"
)

func TestPrintCaptures(t *testing.T) {
	var out strings.Builder
	if err := printCaptures(&out, []byte(`{"captures":[]}`)); err != nil {
		t.Fatal(err)
	}
	if out.String() != "no setup, resume, or service output\n" {
		t.Fatalf("empty %q", out.String())
	}
	out.Reset()
	body := `{"captures":[
{"kind":"setup","output":"ok\n","failed":true,"recorded_at":"2026-09-28T10:00:00Z"},
{"kind":"service","name":"web","output":"tail","truncated":true,"recorded_at":"2026-09-28T10:00:01Z"}]}`
	if err := printCaptures(&out, []byte(body)); err != nil {
		t.Fatal(err)
	}
	want := "== setup 2026-09-28T10:00:00Z failed\nok\n== service web 2026-09-28T10:00:01Z truncated to the last 1 MiB\ntail\n"
	if out.String() != want {
		t.Fatalf("got %q", out.String())
	}
}
