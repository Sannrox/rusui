package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/sannrox/rusui/internal/store"
)

func TestSessionDetailReceiptHistoryIsExplicitAndPaged(t *testing.T) {
	_, hs, e := consoleEnv(t)
	sessionID, err := e.StartRun("test", "receipt history", "")
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.GetSession(e.Store, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	for i := range 101 {
		now := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC).Add(time.Duration(i) * time.Second)
		if err := store.InsertEnvironmentReceipt(e.Store, sess.EnvironmentID, "sleep", "succeeded", fmt.Sprintf("receipt %03d", i), now); err != nil {
			t.Fatal(err)
		}
	}

	get := func(query string) (int, []byte) {
		t.Helper()
		path := "/sessions/" + strconv.FormatInt(sessionID, 10)
		if query != "" {
			path += "?" + query
		}
		req, err := http.NewRequest(http.MethodGet, hs.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer op-tok")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = res.Body.Close() }()
		body, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatal(err)
		}
		return res.StatusCode, body
	}

	status, body := get("")
	if status != http.StatusOK {
		t.Fatalf("default session detail %d: %s", status, body)
	}
	var detail map[string]json.RawMessage
	if err := json.Unmarshal(body, &detail); err != nil {
		t.Fatal(err)
	}
	if _, ok := detail["environment_receipts"]; ok {
		t.Fatal("default session detail included receipt history")
	}

	type receiptPage struct {
		Receipts  []store.EnvironmentReceipt `json:"environment_receipts"`
		NextAfter int64                      `json:"environment_receipts_next_after_id"`
	}
	status, body = get("include=receipts")
	if status != http.StatusOK {
		t.Fatalf("first receipt page %d: %s", status, body)
	}
	var first receiptPage
	if err := json.Unmarshal(body, &first); err != nil {
		t.Fatal(err)
	}
	if len(first.Receipts) != 100 {
		t.Fatalf("first receipt page has %d items and cursor %d", len(first.Receipts), first.NextAfter)
	}
	if first.Receipts[0].Detail != "receipt 000" || first.Receipts[99].Detail != "receipt 099" {
		t.Fatalf("first receipt page is out of order: first=%+v last=%+v", first.Receipts[0], first.Receipts[99])
	}
	if first.NextAfter != first.Receipts[99].ID {
		t.Fatalf("first receipt page cursor %d does not match its last row %d", first.NextAfter, first.Receipts[99].ID)
	}

	query := url.Values{"include": {"receipts"}, "receipt_after_id": {strconv.FormatInt(first.NextAfter, 10)}}
	status, body = get(query.Encode())
	if status != http.StatusOK {
		t.Fatalf("second receipt page %d: %s", status, body)
	}
	var second receiptPage
	if err := json.Unmarshal(body, &second); err != nil {
		t.Fatal(err)
	}
	if len(second.Receipts) != 1 || second.Receipts[0].Detail != "receipt 100" || second.NextAfter != 0 {
		t.Fatalf("second receipt page %+v", second)
	}
	var secondPayload map[string]json.RawMessage
	if err := json.Unmarshal(body, &secondPayload); err != nil {
		t.Fatal(err)
	}
	if _, ok := secondPayload["environment_receipts_next_after_id"]; ok {
		t.Fatal("final receipt page included a continuation cursor")
	}
	if second.Receipts[0].ID <= first.NextAfter {
		t.Fatalf("receipt cursor repeated an earlier row: first=%d second=%d", first.NextAfter, second.Receipts[0].ID)
	}

	for _, query := range []string{"include=receipts&receipt_after_id=-1", "include=receipts&receipt_after_id="} {
		status, body = get(query)
		if status != http.StatusBadRequest {
			t.Fatalf("invalid receipt cursor %q status %d: %s", query, status, body)
		}
	}
}
