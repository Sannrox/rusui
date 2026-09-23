package server

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sannrox/rusui/internal/store"
)

func TestSessionDetailReturnsSeparateProcessAndAttachIdentities(t *testing.T) {
	_, hs, e := consoleEnv(t)
	now := time.Date(2026, 9, 23, 18, 0, 0, 0, time.UTC)
	res, err := e.Store.DB.Exec(`INSERT INTO sessions (environment_id, kind, repo, item, item_kind, state, created_at)
		VALUES (?, ?, '', -1, 'local', 'open', ?)`, store.DefaultEnvironmentID, store.SessionKindLocal, now.Format(time.RFC3339Nano))
	if err != nil {
		t.Fatal(err)
	}
	sessionID, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	process, err := store.StartSumikaProcess(e.Store, sessionID, now)
	if err != nil {
		t.Fatal(err)
	}
	process, err = store.ObserveSumikaProcess(e.Store, process.ID, process.Generation, process.Revision, store.ProcessIdle, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	attach, err := store.BeginSumikaAttach(e.Store, process.ID, process.Generation, now.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}

	req, err := http.NewRequest("GET", hs.URL+"/sessions/"+strconv.FormatInt(sessionID, 10), nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer op-tok")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("session detail %d: %s", resp.StatusCode, body)
	}
	var detail struct {
		Session          store.Session   `json:"session"`
		Turns            []store.Turn    `json:"turns"`
		Processes        []store.Process `json:"processes"`
		EnvironmentState string          `json:"environment_state"`
	}
	if err := json.Unmarshal(body, &detail); err != nil {
		t.Fatal(err)
	}
	if detail.Session.ID != sessionID || detail.Session.EnvironmentID != store.DefaultEnvironmentID || detail.EnvironmentState != store.EnvReady {
		t.Fatalf("session/environment identity %+v state=%s", detail.Session, detail.EnvironmentState)
	}
	if len(detail.Turns) != 0 || len(detail.Processes) != 1 {
		t.Fatalf("turn/process collections turns=%+v processes=%+v", detail.Turns, detail.Processes)
	}
	got := detail.Processes[0]
	if got.ID != process.ID || got.SessionID != sessionID || got.Generation != process.Generation || got.Name != process.Name || got.State != store.ProcessIdle {
		t.Fatalf("process identity %+v", got)
	}
	if len(got.Attaches) != 1 || got.Attaches[0].ID != attach.ID || got.Attaches[0].ProcessID != process.ID || got.Attaches[0].Generation != attach.Generation || got.Attaches[0].State != store.AttachAttached {
		t.Fatalf("attach identity %+v", got.Attaches)
	}
	encoded := strings.ToLower(string(body))
	if strings.Contains(encoded, "scrollback") || strings.Contains(encoded, "pty_bytes") || strings.Contains(encoded, "terminal_bytes") || strings.Contains(encoded, `"argv"`) || strings.Contains(encoded, `"cwd"`) {
		t.Fatalf("runtime detail exposed terminal content: %s", body)
	}
}
