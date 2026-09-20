package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/sannrox/rusui/internal/env"
	"github.com/sannrox/rusui/internal/publish"
	"github.com/sannrox/rusui/internal/store"
)

type VerifySpec struct {
	Source        publish.Source    `json:"source"`
	Candidate     publish.Candidate `json:"candidate"`
	Author        int64             `json:"author_session_id"`
	Reviewer      int64             `json:"reviewer_session_id"`
	Command       []string          `json:"command"`
	ChecksOn      bool              `json:"checks_available"`
	CandidateBlob string            `json:"candidate_blob"`
}

type VerifyReport struct {
	ID        int64           `json:"id"`
	Outcome   publish.Outcome `json:"outcome"`
	LogDigest string          `json:"log_digest"`
	ExitCode  int             `json:"exit_code"`
}

// Verify runs the check command on the configured container driver with no
// publication credentials, records an immutable proof, and evaluates the
// publication gate. A blob whose hash is not the candidate SHA is not proven.
func (e *Engine) Verify(spec VerifySpec) (*VerifyReport, error) {
	if e.Container == nil {
		return nil, fmt.Errorf("verifier isolation required")
	}
	if spec.CandidateBlob == "" || spec.Candidate.CommitSHA == "" {
		return nil, fmt.Errorf("invalid artifact")
	}
	blobHash := sha256hex(spec.CandidateBlob)
	cmd := spec.Command
	if len(cmd) == 0 {
		cmd = []string{"true"}
	}
	handle, err := e.Container.Create("verify")
	if err != nil {
		return nil, err
	}
	defer func() { _ = e.Container.Destroy(handle) }()
	exit := 0
	if blobHash != spec.Candidate.CommitSHA {
		exit = 1
	} else if err := containerExec(e.Container, handle, cmd); err != nil {
		exit = 1
	}
	logDigest := sha256hex(spec.CandidateBlob + fmt.Sprint(exit))
	attempt := publish.Attempt{
		Source:    spec.Source,
		Candidate: spec.Candidate,
		Action:    publish.ActionOpenPR,
		Proof: publish.Proof{
			Command: cmd[0], ExitCode: exit, LogDigest: logDigest,
			HeadSHA: spec.Candidate.CommitSHA, BaseSHA: spec.Source.MainSHA,
			Isolated: true, HadWriteCreds: false,
			AuthorSessionID: spec.Author, ReviewerSessionID: spec.Reviewer,
			SourceSnapshotHash: spec.Source.SnapshotHash, ChecksAvailable: spec.ChecksOn && exit == 0,
		},
	}
	out := publish.Evaluate(attempt)
	id, err := store.InsertProof(e.Store, store.ProofRow{
		CandidateSHA: spec.Candidate.CommitSHA, SourceHash: spec.Source.SnapshotHash,
		LogDigest: logDigest, Command: cmd[0], ExitCode: exit, Outcome: string(out),
	})
	if err != nil {
		return nil, err
	}
	return &VerifyReport{ID: id, Outcome: out, LogDigest: logDigest, ExitCode: exit}, nil
}

func containerExec(d env.Driver, handle string, cmd []string) error {
	c, ok := d.(env.Container)
	if !ok || c.RT == nil {
		return fmt.Errorf("no exec")
	}
	return c.RT.Exec(handle, cmd)
}

func sha256hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
