package store

import "testing"

// LatestTerminalAccessActor must order by the portable id column. SQLite
// aliases INTEGER PRIMARY KEY as rowid, so ORDER BY rowid passed unit tests
// and failed on PostgreSQL (no rowid on terminal_access), which made
// terminalWriteHolder false and attach input 409.
func TestLatestTerminalAccessActorReturnsNewest(t *testing.T) {
	st := openTestStore(t)
	if err := InsertTerminalAccess(st, 7, 11, "acquire", "first", "actor-a"); err != nil {
		t.Fatal(err)
	}
	if err := InsertTerminalAccess(st, 7, 11, "acquire", "second", "actor-b"); err != nil {
		t.Fatal(err)
	}
	actor, ok, err := LatestTerminalAccessActor(st, 7, "acquire")
	if err != nil || !ok || actor != "actor-b" {
		t.Fatalf("got actor=%q ok=%v err=%v", actor, ok, err)
	}
	if _, ok, err := LatestTerminalAccessActor(st, 7, "input"); err != nil || ok {
		t.Fatalf("missing action ok=%v err=%v", ok, err)
	}
}
