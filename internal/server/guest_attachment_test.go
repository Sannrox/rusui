package server

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/sannrox/rusui/internal/acp"
	"github.com/sannrox/rusui/internal/store"
)

func TestShikigamiAttachmentCapabilityAdmission(t *testing.T) {
	for _, tc := range []struct {
		name     string
		receipts []string
		want     int
	}{
		{"native Codex", nil, 400},
		{"unknown", nil, 400},
		{"malformed", []string{`{"guest":`}, 400},
		{"old guest", []string{`{"guest":"shikigami","result":{"agentCapabilities":{"promptCapabilities":{}}}}`}, 400},
		{"image only", []string{`{"guest":"shikigami","result":{"agentCapabilities":{"promptCapabilities":{"image":true}}}}`}, 400},
		{"document only", []string{`{"guest":"shikigami","result":{"agentCapabilities":{"promptCapabilities":{"embeddedContext":true}}}}`}, 400},
		{"different guest", []string{`{"guest":"grok","result":{"agentCapabilities":{"promptCapabilities":{"image":true,"embeddedContext":true}}}}`}, 400},
		{"matching HTTP guest", []string{`{"guest":"shikigami","result":{"agentCapabilities":{"promptCapabilities":{"image":true,"embeddedContext":true}}}}`}, 200},
		{"downgraded guest", []string{`{"guest":"shikigami","result":{"agentCapabilities":{"promptCapabilities":{"image":true,"embeddedContext":true}}}}`, `{"guest":"shikigami","result":{}}`}, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, hs, e := consoleEnv(t)
			s.Guest = acp.GuestShikigami
			if tc.name == "native Codex" {
				s.Guest = "codex"
			}
			e.DefaultGuest = s.Guest
			sid := createRunSession(t, hs, "first")
			for i, body := range tc.receipts {
				if err := store.InsertAction(e.Store, store.Action{ID: fmt.Sprintf("init-%d", i), SessionID: &sid, Type: "acp.initialize", Body: body}); err != nil {
					t.Fatal(err)
				}
			}
			var before int
			if err := e.Store.DB.QueryRow(`SELECT COUNT(*) FROM followup_queue WHERE session_id=?`, sid).Scan(&before); err != nil {
				t.Fatal(err)
			}
			body, err := json.Marshal(map[string]any{"prompt": "inspect", "attachments": []map[string]string{
				{"name": "image.png", "content": base64.StdEncoding.EncodeToString([]byte("\x89PNG\r\n\x1a\n"))},
				{"name": "document.pdf", "content": base64.StdEncoding.EncodeToString([]byte("%PDF-1.1"))},
			}})
			if err != nil {
				t.Fatal(err)
			}
			req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/sessions/%d/turns", hs.URL, sid), strings.NewReader(string(body)))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Authorization", "Bearer op-tok")
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			_ = res.Body.Close()
			if res.StatusCode != tc.want {
				t.Fatalf("status %d, want %d", res.StatusCode, tc.want)
			}
			if tc.want == 400 {
				var after, attachments int
				if err := e.Store.DB.QueryRow(`SELECT COUNT(*) FROM followup_queue WHERE session_id=?`, sid).Scan(&after); err != nil {
					t.Fatal(err)
				}
				if err := e.Store.DB.QueryRow(`SELECT COUNT(*) FROM prompt_attachments WHERE session_id=?`, sid).Scan(&attachments); err != nil {
					t.Fatal(err)
				}
				if after != before || attachments != 0 {
					t.Fatalf("refusal stored prompt: before=%d after=%d attachments=%d", before, after, attachments)
				}
			}
		})
	}
}
