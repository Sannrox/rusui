package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sannrox/rusui/internal/flight"
	"github.com/sannrox/rusui/internal/store"
)

func TestFlightGetAndReplay(t *testing.T) {
	_, hs, e := consoleEnv(t)
	sid, err := e.StartRun("test", "summarize obj", "")
	if err != nil {
		t.Fatal(err)
	}
	turns, err := store.ListTurnsForSession(e.Store, sid)
	if err != nil || len(turns) == 0 {
		t.Fatalf("turns %v %v", turns, err)
	}
	tid := turns[0].ID
	rec := flight.Normalize(flight.Record{
		SessionID: sid, TurnID: tid, ReceiptID: "r1", Prompt: "summarize obj",
		Tools: []flight.Tool{
			{ID: "t1", Name: "read_file", Input: map[string]any{"path": "a.txt"}},
			{ID: "t2", Name: "bash", Input: map[string]any{"command": "echo hi"}},
		},
		Snapshot: flight.Snapshot{Objects: []flight.Object{
			{ID: "obj-a", Revision: "rev-1", Readers: []string{"default"}},
			{ID: "obj-b", Revision: "rev-7", Readers: []string{"other"}},
		}},
	})
	if err := store.InsertFlightRecord(e.Store, rec, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	path := "/sessions/" + strconv.FormatInt(sid, 10) + "/turns/" + strconv.FormatInt(tid, 10)
	got := authedGet(t, hs, path+"/flight", "op-tok")
	if got.code != http.StatusOK {
		t.Fatalf("get %d %s", got.code, got.body)
	}
	var view flight.View
	if err := json.Unmarshal([]byte(got.body), &view); err != nil {
		t.Fatal(err)
	}
	if _, err := view.Object("obj-b"); !errors.Is(err, flight.ErrNoRead) {
		t.Fatalf("obj-b %v view %+v", err, view)
	}
	if _, err := view.Object("obj-a"); err != nil {
		t.Fatalf("obj-a %v", err)
	}
	anon := authedGet(t, hs, path+"/flight", "")
	if anon.code != http.StatusUnauthorized {
		t.Fatalf("anon %d", anon.code)
	}

	body, err := json.Marshal(map[string]any{
		"snapshot": map[string]any{
			"objects": []map[string]string{{"id": "obj-a", "revision": "rev-changed"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, hs.URL+path+"/replay", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer op-tok")
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(res.Body)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("replay %d %s", res.StatusCode, buf.String())
	}
	var rep flight.Report
	if err := json.Unmarshal(buf.Bytes(), &rep); err != nil {
		t.Fatal(err)
	}
	if rep.Divergence != "object_revision" || rep.ObjectID != "obj-a" {
		t.Fatalf("report %+v", rep)
	}
	if !strings.Contains(buf.String(), `"denied_effects"`) {
		t.Fatalf("missing denials %s", buf.String())
	}
}
