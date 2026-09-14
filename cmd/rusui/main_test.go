package main

import (
	"strings"
	"testing"
)

func TestMissingRequiredSecrets(t *testing.T) {
	got := missingRequiredSecrets(func(string) string { return "" })
	if len(got) != 3 {
		t.Fatalf("%v", got)
	}
	want := []string{"RUSUI_WEBHOOK_SECRET", "RUSUI_WORKER_SECRET", "RUSUI_SLACK_SECRET"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("%v", got)
	}
	got = missingRequiredSecrets(func(k string) string {
		if k == "RUSUI_WORKER_SECRET" {
			return ""
		}
		return "x"
	})
	if len(got) != 1 || got[0] != "RUSUI_WORKER_SECRET" {
		t.Fatalf("%v", got)
	}
	if n := missingRequiredSecrets(func(string) string { return "x" }); len(n) != 0 {
		t.Fatalf("%v", n)
	}
}
