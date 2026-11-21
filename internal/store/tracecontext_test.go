package store

import (
	"strings"
	"testing"
)

func TestParseTraceparentValidAndMalformed(t *testing.T) {
	const parent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	got := ParseTraceHeaders(parent, "vendor=one")
	if got.TraceID != "4bf92f3577b34da6a3ce929d0e0e4736" || got.ParentID != "00f067aa0ba902b7" || got.Flags != "01" || got.State != "vendor=one" {
		t.Fatalf("valid %+v", got)
	}
	upper := ParseTraceHeaders("00-4BF92F3577B34DA6A3CE929D0E0E4736-00F067AA0BA902B7-01", "")
	if upper.TraceID != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Fatalf("uppercase %+v", upper)
	}
	for _, h := range []string{"", "nope", "ff-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01", "00-00000000000000000000000000000000-00f067aa0ba902b7-01", "00-4bf92f3577b34da6a3ce929d0e0e4736-0000000000000000-01"} {
		if tc := ParseTraceHeaders(h, ""); tc.TraceID != "" {
			t.Fatalf("malformed %q %+v", h, tc)
		}
	}
	dropped := ParseTraceHeaders(parent, strings.Repeat("x", 1025))
	if dropped.State != "" {
		t.Fatalf("long tracestate %q", dropped.State)
	}
}

func TestMintTraceKeepsInboundParent(t *testing.T) {
	in := ParseTraceHeaders("00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01", "")
	got := MintTrace(in)
	if got.TraceID != in.TraceID || got.ParentID != in.ParentID || got.SpanID == "" || got.SpanID == in.ParentID {
		t.Fatalf("join %+v", got)
	}
	fresh := MintTrace(TraceContext{})
	if fresh.TraceID == "" || fresh.ParentID != "" || fresh.SpanID == "" {
		t.Fatalf("fresh %+v", fresh)
	}
	if h := got.GuestHeader(); h != "00-"+got.TraceID+"-"+got.SpanID+"-01" {
		t.Fatalf("guest %s", h)
	}
}

func TestAdmittedTraceContextSurvivesReopen(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/rusui.db"
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	in := ParseTraceHeaders("00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01", "k=v")
	var sid int64
	if err := st.Tx(func(tx *Tx) error {
		var inner error
		sid, _, inner = InsertRunSessionInboundTx(tx, "test", "o/r", "hello", in)
		return inner
	}); err != nil {
		t.Fatal(err)
	}
	turns, err := ListTurnsForSession(st, sid)
	if err != nil || len(turns) != 1 {
		t.Fatalf("turns %+v %v", turns, err)
	}
	first, err := GetTurn(st, turns[0].ID)
	if err != nil || first.Trace.TraceID != in.TraceID || first.Trace.ParentID != in.ParentID || first.Trace.State != "k=v" {
		t.Fatalf("admit %+v %v", first, err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st2.Close() })
	again, err := GetTurn(st2, first.ID)
	if err != nil || again.Trace != first.Trace {
		t.Fatalf("reopen %+v want %+v %v", again, first.Trace, err)
	}
}
