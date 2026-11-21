package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDispositionCLIPostsSeconds(t *testing.T) {
	var body map[string]any
	var saw string
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		saw = r.Method + " " + r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":1,"result_id":42,"disposition":"useful","seconds":90}`))
	}))
	t.Cleanup(hs.Close)
	var buf bytes.Buffer
	if code := dispositionMain([]string{"-url", hs.URL, "-token", "op", "-value", "useful", "-seconds", "90", "42"}, &buf); code != 0 {
		t.Fatalf("code %d %s", code, buf.String())
	}
	if saw != "POST /results/42/dispositions" {
		t.Fatalf("request %q", saw)
	}
	if body["disposition"] != "useful" || body["seconds"] != float64(90) {
		t.Fatalf("body %+v", body)
	}
}

func TestDispositionCLIOmitsSecondsWhenUnset(t *testing.T) {
	var body map[string]any
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":1,"result_id":42,"disposition":"useful"}`))
	}))
	t.Cleanup(hs.Close)
	var buf bytes.Buffer
	if code := dispositionMain([]string{"-url", hs.URL, "-token", "op", "-value", "useful", "42"}, &buf); code != 0 {
		t.Fatalf("code %d %s", code, buf.String())
	}
	if _, ok := body["seconds"]; ok {
		t.Fatalf("omitted flag still sent %+v", body)
	}
}

func TestDispositionCLIRejectsNegativeSeconds(t *testing.T) {
	var buf bytes.Buffer
	if code := dispositionMain([]string{"-url", "http://127.0.0.1:1", "-token", "op", "-value", "useful", "-seconds", "-1", "42"}, &buf); code != 2 {
		t.Fatalf("code %d", code)
	}
}
