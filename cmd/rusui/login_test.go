package main

import (
	"bytes"
	"context"
	"flag"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sannrox/rusui/internal/oidc"
)

func TestOperatorTokenPrecedence(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	base := "https://plane.example"
	if err := oidc.SaveCredentials(oidc.Credentials{Server: base, Issuer: "https://issuer.example", TokenEndpoint: "https://issuer.example/token", ClientID: "cli", AccessToken: "saved", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, env, worker string
		args              []string
		want              string
	}{
		{name: "saved", want: "saved"},
		{name: "environment", env: "environment", want: "environment"},
		{name: "flag", env: "environment", args: []string{"-token", "explicit"}, want: "explicit"},
		{name: "explicit empty", env: "environment", args: []string{"-token", ""}, want: ""},
		{name: "worker default", worker: "worker", want: "saved"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("RUSUI_OPERATOR_TOKEN", tc.env)
			fs := flag.NewFlagSet("test", flag.ContinueOnError)
			token := fs.String("token", tc.worker, "")
			if err := fs.Parse(tc.args); err != nil {
				t.Fatal(err)
			}
			if err := resolveOperatorToken(fs, base, token); err != nil {
				t.Fatal(err)
			}
			if *token != tc.want {
				t.Fatalf("token=%q want=%q", *token, tc.want)
			}
		})
	}
}

func TestReadUsesSavedOperatorCredential(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("RUSUI_OPERATOR_TOKEN", "")
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer saved" {
			t.Error("saved credential not sent")
			http.Error(w, "auth", http.StatusUnauthorized)
			return
		}
		_, _ = io.WriteString(w, `{"session_id":1,"kind":"run","prompt":"hello"}`)
	}))
	defer hs.Close()
	if err := oidc.SaveCredentials(oidc.Credentials{Server: hs.URL, Issuer: "https://issuer.example", TokenEndpoint: "https://issuer.example/token", ClientID: "cli", AccessToken: "saved", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	if code := readMain([]string{"-url", hs.URL, "1"}, &out, &stderr); code != 0 {
		t.Fatalf("code=%d %s", code, stderr.String())
	}
	if out.Len() == 0 {
		t.Fatal("read did not print transcript")
	}
	if err := oidc.Logout(hs.URL); err != nil {
		t.Fatal(err)
	}
	if token, err := oidc.SavedToken(context.Background(), hs.URL); token != "" || err != nil {
		t.Fatalf("logout token=%q err=%v", token, err)
	}
}
