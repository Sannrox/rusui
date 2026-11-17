package engine

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/sannrox/rusui/internal/store"
)

// AdmitTimeout bounds one admit hook run; a slower hook refuses.
const AdmitTimeout = 10 * time.Second

// admitRefLimit caps the stdout line recorded as a decision reference or
// refusal reason.
const admitRefLimit = 512

// AdmitRequest is the JSON object an admit hook reads on stdin. It carries
// a digest of the prompt, never the prompt text.
type AdmitRequest struct {
	Project      string `json:"project"`
	Kind         string `json:"kind"`
	Repo         string `json:"repo,omitempty"`
	BaseSHA      string `json:"base_sha,omitempty"`
	Actor        string `json:"actor,omitempty"`
	PromptSHA256 string `json:"prompt_sha256,omitempty"`
}

// AdmitRefusedError is a session start the project's admit hook refused.
// Reason is the hook's first stdout line, or why the hook failed.
type AdmitRefusedError struct{ Reason string }

func (e *AdmitRefusedError) Error() string { return "admit refused: " + e.Reason }

// errNeedsAdmit rolls back a start transaction that reached its insert
// before the admit hook ran; admitStart runs the hook and retries.
var errNeedsAdmit = errors.New("engine: admit hook has not run")

// admitGate carries one session start's admission through the start
// transaction. A project without an admit hook gets a gate that always
// passes and records nothing.
type admitGate struct {
	hook    string
	req     AdmitRequest
	decided bool
	ref     string
}

// ready is called inside the start transaction just before the session
// row is inserted. Starts that return an existing session never reach it,
// so a retried start does not run the hook again.
func (g *admitGate) ready() error {
	if g.hook == "" || g.decided {
		return nil
	}
	return errNeedsAdmit
}

// recordTx records admit.allow on the new session, in the transaction that
// created it.
func (g *admitGate) recordTx(tx *store.Tx, sessionID int64) error {
	if g.hook == "" {
		return nil
	}
	body, err := json.Marshal(struct {
		AdmitRequest
		Ref string `json:"ref"`
	}{g.req, g.ref})
	if err != nil {
		return err
	}
	return store.InsertActionTx(tx, store.Action{
		ID: fmt.Sprintf("admit-allow-%d", sessionID), SessionID: &sessionID,
		Repo: g.req.Repo, Type: "admit.allow", ReasonCode: "recorded", EvidenceClass: "plane_observed",
		LimitSentence: "The project's admit hook admitted this session before it was created; the reference is what the hook printed.",
		Body:          string(body),
	})
}

// admitStart runs start once with g. When start stops at the gate, it runs
// the project's admit hook outside any transaction, records a refusal, or
// runs start again with the decision. refusalKey, when set, names a start
// that is retried on a timer (a schedule bucket): a refusal under that key
// is replayed instead of rerunning the hook every tick. Operator starts
// pass none, so a retry asks the hook again.
func (e *Engine) admitStart(req AdmitRequest, refusalKey string, start func(g *admitGate) error) error {
	g := &admitGate{hook: e.AdmitHooks[req.Project], req: req}
	err := start(g)
	if !errors.Is(err, errNeedsAdmit) {
		return err
	}
	refuseID := "admit-refuse-" + randomHex()
	if refusalKey != "" {
		sum := sha256.Sum256([]byte(req.Project + "\x00" + refusalKey))
		refuseID = "admit-refuse-" + hex.EncodeToString(sum[:16])
		if prior, err := store.GetAction(e.Store, refuseID); err == nil {
			var rec struct {
				Reason string `json:"reason"`
			}
			_ = json.Unmarshal([]byte(prior.Body), &rec)
			return &AdmitRefusedError{Reason: rec.Reason}
		} else if !errors.Is(err, store.ErrNoRows) {
			return err
		}
	}
	allowed, line := runAdmitHook(g.hook, req)
	if !allowed {
		if err := e.recordAdmitRefusal(refuseID, req, line); err != nil {
			return err
		}
		return &AdmitRefusedError{Reason: line}
	}
	g.decided, g.ref = true, line
	return start(g)
}

func (e *Engine) recordAdmitRefusal(id string, req AdmitRequest, reason string) error {
	body, err := json.Marshal(struct {
		AdmitRequest
		Reason string `json:"reason"`
	}{req, reason})
	if err != nil {
		return err
	}
	return e.Store.Tx(func(tx *store.Tx) error {
		if exists, err := store.ActionExistsTx(tx, id); err != nil || exists {
			return err
		}
		return store.InsertActionTx(tx, store.Action{
			ID: id, Repo: req.Repo, Type: "admit.refuse", ReasonCode: "refused", EvidenceClass: "plane_observed",
			LimitSentence: "The project's admit hook refused a session start; no session was created.",
			Body:          string(body),
		})
	})
}

// runAdmitHook executes the hook at path with req on stdin and only PATH
// in its environment. Exit 0 admits and exit 2 refuses, each with the
// first stdout line; any other outcome refuses (fail closed).
func runAdmitHook(path string, req AdmitRequest) (allowed bool, line string) {
	in, err := json.Marshal(req)
	if err != nil {
		return false, "admit request could not be encoded"
	}
	ctx, cancel := context.WithTimeout(context.Background(), AdmitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, path)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH")}
	cmd.Stdin = bytes.NewReader(in)
	var out bytes.Buffer
	cmd.Stdout = &limitedWriter{w: &out, n: 64 << 10}
	cmd.WaitDelay = time.Second
	err = cmd.Run()
	first := firstLine(out.Bytes())
	switch {
	case ctx.Err() != nil:
		return false, "admit hook timed out"
	case err == nil:
		return true, first
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 2 {
		return false, first
	}
	if errors.As(err, &exit) {
		return false, fmt.Sprintf("admit hook failed (exit %d)", exit.ExitCode())
	}
	return false, "admit hook could not run"
}

func firstLine(b []byte) string {
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 0, 4096), len(b)+1)
	if !sc.Scan() {
		return ""
	}
	line := sc.Bytes()
	if len(line) > admitRefLimit {
		line = line[:admitRefLimit]
	}
	return string(line)
}

// limitedWriter keeps the first n bytes and discards the rest, so a noisy
// hook cannot grow plane memory.
type limitedWriter struct {
	w *bytes.Buffer
	n int
}

func (l *limitedWriter) Write(p []byte) (int, error) {
	if rest := l.n - l.w.Len(); rest > 0 {
		if len(p) > rest {
			l.w.Write(p[:rest])
		} else {
			l.w.Write(p)
		}
	}
	return len(p), nil
}

func promptDigest(prompt string) string {
	sum := sha256.Sum256([]byte(prompt))
	return hex.EncodeToString(sum[:])
}

func randomHex() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// admitActor is the first non-empty of the acting identity, the creator,
// and the tenant: who the session acts for.
func admitActor(candidates ...string) string {
	for _, c := range candidates {
		if c = strings.TrimSpace(c); c != "" {
			return c
		}
	}
	return ""
}
