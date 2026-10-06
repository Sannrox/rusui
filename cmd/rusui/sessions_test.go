package main

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestNewPromptRequestCanSteer(t *testing.T) {
	req, err := newPromptRequest("http://127.0.0.1:8080/", "secret", "42", "use the narrow fix", true, false)
	if err != nil {
		t.Fatal(err)
	}
	if req.Method != http.MethodPost || req.URL.Path != "/sessions/42/turns" || req.Header.Get("Authorization") != "Bearer secret" {
		t.Fatalf("request %s %s headers=%v", req.Method, req.URL, req.Header)
	}
	var body struct {
		Prompt string `json:"prompt"`
		Steer  bool   `json:"steer"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Prompt != "use the narrow fix" || !body.Steer {
		t.Fatalf("body %+v", body)
	}
}

func TestNewPromptRequestCanQueue(t *testing.T) {
	req, err := newPromptRequest("http://127.0.0.1:8080", "secret", "42", "after you finish", false, true)
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Prompt string `json:"prompt"`
		Queued bool   `json:"queued"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if req.URL.Path != "/sessions/42/turns" || body.Prompt != "after you finish" || !body.Queued {
		t.Fatalf("request %s body %+v", req.URL, body)
	}
}

func TestNewDropQueueRequest(t *testing.T) {
	req, err := newDropQueueRequest("http://127.0.0.1:8080/", "secret", "42")
	if err != nil {
		t.Fatal(err)
	}
	if req.Method != http.MethodDelete || req.URL.Path != "/sessions/42/queued" || req.Header.Get("Authorization") != "Bearer secret" {
		t.Fatalf("request %s %s headers=%v", req.Method, req.URL, req.Header)
	}
}
