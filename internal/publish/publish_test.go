package publish

import "testing"

func valid() Attempt {
	return Attempt{
		Source: Source{Repo: "example/test-repo", Item: 1, SnapshotHash: "snap-a", MainSHA: "main-a"},
		Candidate: Candidate{
			SessionID: 7, CommitSHA: "abc123abc123", TreeSHA: "tree-a",
			Ref: "refs/heads/rusui/7/work",
		},
		Proof: Proof{
			Command: "go test ./...", ExitCode: 0, LogDigest: "log-a",
			HeadSHA: "abc123abc123", BaseSHA: "main-a", Isolated: true,
			AuthorSessionID: 7, ReviewerSessionID: 8,
			SourceSnapshotHash: "snap-a", ChecksAvailable: true,
		},
		Action: ActionOpenPR,
	}
}

func TestEvaluateProven(t *testing.T) {
	if got := Evaluate(valid()); got != Proven {
		t.Fatalf("got %s", got)
	}
}

func TestEvaluateFailClosed(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*Attempt)
		want Outcome
	}{
		{"cancelled", func(a *Attempt) { a.Cancelled = true }, Cancelled},
		{"unnamed action", func(a *Attempt) { a.Action = "merge" }, Denied},
		{"write creds", func(a *Attempt) { a.Proof.HadWriteCreds = true }, Denied},
		{"self verified", func(a *Attempt) { a.Proof.AgentDeclaredVerified = true }, Denied},
		{"policy mutated", func(a *Attempt) { a.Proof.PolicyMutated = true }, Denied},
		{"checks disabled", func(a *Attempt) { a.Proof.RequiredChecksDisabled = true }, Denied},
		{"not isolated", func(a *Attempt) { a.Proof.Isolated = false }, Denied},
		{"expired", func(a *Attempt) { a.Proof.Expired = true }, Denied},
		{"too many repairs", func(a *Attempt) { a.Proof.RepairCount = MaxRepair + 1 }, Denied},
		{"bad ref", func(a *Attempt) { a.Candidate.Ref = "refs/heads/main" }, Denied},
		{"source drift", func(a *Attempt) { a.Proof.SourceSnapshotHash = "snap-b" }, SourceDrift},
		{"head mismatch", func(a *Attempt) { a.Proof.HeadSHA = "other" }, Ambiguous},
		{"tests failed", func(a *Attempt) { a.Proof.ExitCode = 1 }, Denied},
		{"checks unavailable", func(a *Attempt) { a.Proof.ChecksAvailable = false }, ChecksUnavailable},
		{"self review", func(a *Attempt) { a.Proof.ReviewerSessionID = a.Proof.AuthorSessionID }, Denied},
		{"missing review", func(a *Attempt) { a.Proof.ReviewerSessionID = 0 }, Denied},
		{"update permitted", func(a *Attempt) { a.Action = ActionUpdatePR }, Proven},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := valid()
			tc.mut(&a)
			if got := Evaluate(a); got != tc.want {
				t.Fatalf("got %s want %s", got, tc.want)
			}
		})
	}
}

func TestPermittedActions(t *testing.T) {
	if !Permitted(ActionOpenPR) || !Permitted(ActionUpdatePR) {
		t.Fatal("open/update must be permitted")
	}
	for _, a := range []Action{"merge", "close", "label", "protect", ""} {
		if Permitted(a) {
			t.Fatalf("permitted unnamed %q", a)
		}
	}
}
