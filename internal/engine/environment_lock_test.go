package engine_test

import (
	"errors"
	"testing"
	"time"

	"github.com/sannrox/rusui/internal/engine"
	"github.com/sannrox/rusui/internal/env"
	"github.com/sannrox/rusui/internal/store"
)

func TestEnvironmentLifecycleDoesNotWaitOnRuntimeIO(t *testing.T) {
	h := setup(t)
	rt := &env.FakeRuntime{}
	h.e.Container = env.Container{RT: rt, Image: "rusui-guest:test"}
	created, err := h.e.ProvisionEnvironment(engine.EnvSpec{Name: "lock-check", Kind: env.KindContainer})
	if err != nil {
		t.Fatal(err)
	}

	stopEntered := make(chan struct{})
	stopRelease := make(chan struct{})
	rt.StopHook = func(string) error {
		close(stopEntered)
		<-stopRelease
		return nil
	}
	h.e.EnvIdleSleep = 5 * time.Minute
	h.clk.Advance(6 * time.Minute)
	sleepDone := make(chan error, 1)
	go func() { sleepDone <- h.e.SleepIdleEnvironments() }()
	select {
	case <-stopEntered:
	case <-time.After(time.Second):
		close(stopRelease)
		t.Fatal("idle sleep did not reach the container runtime")
	}

	wakeDone := make(chan error, 1)
	go func() {
		_, err := h.e.WakeEnvironment(created.ID)
		wakeDone <- err
	}()
	select {
	case err := <-wakeDone:
		if !errors.Is(err, store.ErrEnvironmentBusy) {
			close(stopRelease)
			<-sleepDone
			t.Fatalf("wake during sleep I/O error = %v, want environment busy", err)
		}
	case <-time.After(250 * time.Millisecond):
		close(stopRelease)
		<-sleepDone
		<-wakeDone
		t.Fatal("wake waited for idle sleep runtime I/O")
	}
	close(stopRelease)
	if err := <-sleepDone; err != nil {
		t.Fatalf("idle sleep: %v", err)
	}

	startEntered := make(chan struct{})
	startRelease := make(chan struct{})
	rt.StartHook = func(string) error {
		close(startEntered)
		<-startRelease
		return nil
	}
	wakeDone = make(chan error, 1)
	go func() {
		_, err := h.e.WakeEnvironment(created.ID)
		wakeDone <- err
	}()
	select {
	case <-startEntered:
	case <-time.After(time.Second):
		close(startRelease)
		t.Fatal("wake did not reach the container runtime")
	}

	sleepDone = make(chan error, 1)
	go func() {
		_, err := h.e.SleepEnvironment(created.ID)
		sleepDone <- err
	}()
	select {
	case err := <-sleepDone:
		if !errors.Is(err, store.ErrEnvironmentBusy) {
			close(startRelease)
			<-wakeDone
			t.Fatalf("sleep during wake I/O error = %v, want environment busy", err)
		}
	case <-time.After(250 * time.Millisecond):
		close(startRelease)
		<-wakeDone
		<-sleepDone
		t.Fatal("sleep waited for wake runtime I/O")
	}
	close(startRelease)
	if err := <-wakeDone; err != nil {
		t.Fatalf("wake: %v", err)
	}
}

func TestClaimSkipsBusyEnvironmentAndClaimsIndependentQueuedTurn(t *testing.T) {
	h := setup(t)
	rt := &env.FakeRuntime{DefaultFiles: map[string]bool{env.ResumePath: true}}
	h.e.Container = env.Container{RT: rt, Image: "rusui-guest:test"}
	h.putRefresh(issue(1))
	first := h.claim()
	firstTurn, err := store.GetTurn(h.st, first.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	firstSession, err := store.GetSession(h.st, firstTurn.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.e.Complete(first.Job.ID, first.Job.LeaseGeneration, first.Job.ClaimedRevision, art(first, "keep", "", "")); err != nil {
		t.Fatal(err)
	}
	h.putRefresh(issue(2))
	second := h.claim()
	secondTurn, err := store.GetTurn(h.st, second.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	secondSession, err := store.GetSession(h.st, secondTurn.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if secondSession.EnvironmentID == firstSession.EnvironmentID {
		t.Fatalf("sessions share environment %d; want independent environments", firstSession.EnvironmentID)
	}
	if _, err := h.e.Complete(second.Job.ID, second.Job.LeaseGeneration, second.Job.ClaimedRevision, art(second, "keep", "", "")); err != nil {
		t.Fatal(err)
	}
	if _, err := h.e.SleepEnvironment(firstSession.EnvironmentID); err != nil {
		t.Fatal(err)
	}
	busyFollowUpID, _, err := h.e.PromptFollowUp(firstSession.ID, "continue after wake")
	if err != nil {
		t.Fatal(err)
	}
	readyFollowUpID, _, err := h.e.PromptFollowUp(secondSession.ID, "continue independently")
	if err != nil {
		t.Fatal(err)
	}

	wakeEntered := make(chan struct{})
	wakeRelease := make(chan struct{})
	rt.StartHook = func(string) error {
		close(wakeEntered)
		<-wakeRelease
		return nil
	}
	wakeDone := make(chan error, 1)
	go func() {
		_, err := h.e.WakeEnvironment(firstSession.EnvironmentID)
		wakeDone <- err
	}()
	select {
	case <-wakeEntered:
	case <-time.After(time.Second):
		close(wakeRelease)
		t.Fatal("wake did not reach the container runtime")
	}

	type claimResult struct {
		claim *engine.Claim
		err   error
	}
	claimDone := make(chan claimResult, 1)
	go func() {
		claim, err := h.e.Claim("example/test-repo")
		claimDone <- claimResult{claim: claim, err: err}
	}()
	select {
	case got := <-claimDone:
		if got.err != nil || got.claim == nil || got.claim.Job.ID != readyFollowUpID {
			close(wakeRelease)
			<-wakeDone
			t.Fatalf("claim during wake returned claim=%+v err=%v, want independent turn %d", got.claim, got.err, readyFollowUpID)
		}
		if _, err := h.e.Complete(got.claim.Job.ID, got.claim.Job.LeaseGeneration, got.claim.Job.ClaimedRevision, art(got.claim, "keep", "", "")); err != nil {
			close(wakeRelease)
			<-wakeDone
			t.Fatalf("complete independent turn during wake: %v", err)
		}
	case <-time.After(250 * time.Millisecond):
		close(wakeRelease)
		<-wakeDone
		<-claimDone
		t.Fatal("claim waited for environment wake runtime I/O")
	}
	close(wakeRelease)
	if err := <-wakeDone; err != nil {
		t.Fatalf("wake: %v", err)
	}
	busyFollowUp, err := store.GetTurn(h.st, busyFollowUpID)
	if err != nil || busyFollowUp.State != "queued" || busyFollowUp.RetryCount != 0 {
		t.Fatalf("busy environment turn changed unexpectedly: turn=%+v err=%v", busyFollowUp, err)
	}

	claim, err := h.e.Claim("example/test-repo")
	if err != nil || claim == nil || claim.Job.ID != busyFollowUpID {
		t.Fatalf("claim after wake: claim=%+v err=%v", claim, err)
	}
}
