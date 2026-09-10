package slack

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func TestVerifyAndReplay(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	body := []byte("user_id=U1&text=status")
	ts := "1000000"
	sig := Sign("sec", ts, body)
	if err := Verify("sec", ts, sig, body, now); err != nil {
		t.Fatal(err)
	}
	if err := Verify("sec", ts, "v0=dead", body, now); err == nil {
		t.Fatal("bad sig")
	}
	if err := Verify("sec", "1", sig, body, now); err == nil {
		t.Fatal("skew")
	}
	if err := Verify("", ts, sig, body, now); err == nil {
		t.Fatal("empty secret")
	}
}

func TestParseFormAndSplit(t *testing.T) {
	body := []byte("user_id=U1&command=%2Frusui&text=retry+example%2Frepo%2312")
	c := ParseForm(body)
	if c.UserID != "U1" || c.Command != "retry" || c.Arg != "example/repo#12" {
		t.Fatalf("%+v", c)
	}
	repo, item := SplitItem(c.Arg)
	if repo != "example/repo" || item != 12 {
		t.Fatalf("%s %d", repo, item)
	}
}

func TestChallenge(t *testing.T) {
	ch, ok := Challenge([]byte(`{"type":"url_verification","challenge":"abc"}`))
	if !ok || ch != "abc" {
		t.Fatalf("%v %v", ch, ok)
	}
}

func TestPoster(t *testing.T) {
	var got url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got, _ = url.ParseQuery(string(b))
		if r.Header.Get("Authorization") != "Bearer bot" {
			t.Errorf("auth %s", r.Header.Get("Authorization"))
		}
		w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(srv.Close)
	p := &Poster{Token: "bot", Channel: "C1", API: srv.URL, HTTP: srv.Client()}
	if err := p.Exception("retry_limit exhausted"); err != nil {
		t.Fatal(err)
	}
	if got.Get("channel") != "C1" || got.Get("text") != "retry_limit exhausted" {
		t.Fatalf("%v", got)
	}
}
