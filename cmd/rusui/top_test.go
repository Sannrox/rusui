package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTopMainPrintsLiveSample(t *testing.T) {
	body := `[{"ID":7,"environment_state":"ready","size":"small","usage":{"cpu_millis":250,"memory_bytes":33554432}},{"ID":8,"environment_state":"sleeping","size":"medium"},{"ID":9,"environment_state":"sleeping","archived":true}]`
	var saw string
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		saw = r.Method + " " + r.URL.RequestURI()
		if r.Header.Get("Authorization") != "Bearer op" {
			t.Error("operator token not sent")
			http.Error(w, "auth", http.StatusUnauthorized)
			return
		}
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(hs.Close)

	var out, errb bytes.Buffer
	if code := topMain([]string{"-url", hs.URL, "-token", "op"}, &out, &errb); code != 0 {
		t.Fatalf("code %d %s", code, errb.String())
	}
	if saw != "GET /sessions?include=stats" {
		t.Fatalf("request %q", saw)
	}
	var rows []struct {
		ID               int64  `json:"ID"`
		EnvironmentState string `json:"environment_state"`
		Size             string `json:"size"`
		Archived         bool   `json:"archived"`
		Usage            *struct {
			CPUMillis   int   `json:"cpu_millis"`
			MemoryBytes int64 `json:"memory_bytes"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 || rows[0].ID != 7 || rows[0].Usage == nil || rows[0].Usage.CPUMillis != 250 {
		t.Fatalf("rows %s", out.Bytes())
	}
	if rows[1].EnvironmentState != "sleeping" || rows[1].Usage != nil || !rows[2].Archived || rows[2].Usage != nil {
		t.Fatalf("idle rows %s", out.Bytes())
	}
}

func TestTopMainRefusesUnauthorized(t *testing.T) {
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "auth", http.StatusUnauthorized)
	}))
	t.Cleanup(hs.Close)
	var errb bytes.Buffer
	if code := topMain([]string{"-url", hs.URL, "-token", "wsec"}, io.Discard, &errb); code != 1 || !strings.Contains(errb.String(), "401") {
		t.Fatalf("code %d %s", code, errb.String())
	}
}
