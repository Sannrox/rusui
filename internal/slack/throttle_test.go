package slack

import (
	"testing"
	"time"
)

func TestThrottleMutesRepeatsWithinWindow(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	th := &Throttle{Window: time.Hour, Now: func() time.Time { return now }}
	if !th.Allow("reconcile: boom") {
		t.Fatal("first post muted")
	}
	now = now.Add(time.Minute)
	if th.Allow("reconcile: boom") {
		t.Fatal("repeat on the next tick posted")
	}
	if !th.Allow("reconcile: other") {
		t.Fatal("changed message muted")
	}
	now = now.Add(time.Hour)
	if !th.Allow("reconcile: boom") {
		t.Fatal("repeat after the window muted")
	}
}

func TestThrottleForgetLetsAFailedPostRetry(t *testing.T) {
	th := &Throttle{Window: time.Hour}
	th.Allow("apply retry: 502")
	th.Forget("apply retry: 502")
	if !th.Allow("apply retry: 502") {
		t.Fatal("forgotten message still muted")
	}
}
