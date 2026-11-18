// Command taskrun runs a corpus of historical tasks through a running
// plane and scores each published head with checks the agent never sees.
// It is a development tool, not part of the shipped binary:
//
//	go run ./eval/taskrun -corpus FILE -policy FILE -revision REV [-url URL] [-out DIR]
//
// See eval/set.md for the corpus format, the measures, and evidence
// identity.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// Exit code for a prior report from different inputs; 2 is usage.
const exitStale = 3

func main() {
	// An interrupt cancels the run, which kills any running check's
	// process group and removes the checkouts.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

func run(ctx context.Context, args []string, out, errw io.Writer) int {
	fs := flag.NewFlagSet("taskrun", flag.ContinueOnError)
	fs.SetOutput(errw)
	corpusPath := fs.String("corpus", "", "task corpus file")
	policyPath := fs.String("policy", "", "policy file the plane runs (digested into the report)")
	revision := fs.String("revision", "", "candidate plane revision under evaluation")
	guest := fs.String("guest", "", "guest to admit tasks with (default: the project's)")
	url := fs.String("url", "http://127.0.0.1:8080", "plane URL")
	worker := fs.String("worker-token", os.Getenv("RUSUI_WORKER_SECRET"), "worker secret (admits pinned runs)")
	operator := fs.String("token", os.Getenv("RUSUI_OPERATOR_TOKEN"), "operator token (reads turns and publications)")
	remote := fs.String("remote", "https://github.com/%s.git", "clone source for checks; %s is the repository")
	outDir := fs.String("out", "", "also write the JSON report into this directory; never overwrites")
	prior := fs.String("check", "", "prior JSON report; exit 3 before running if it came from different inputs")
	poll := fs.Duration("poll", 10*time.Second, "how often to read turn state")
	taskTimeout := fs.Duration("task-timeout", 2*time.Hour, "longest wait for one task's terminal turn")
	checkTimeout := fs.Duration("check-timeout", 30*time.Minute, "longest run of one check command")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 || *corpusPath == "" || *policyPath == "" || *revision == "" {
		_, _ = fmt.Fprintln(errw, "usage: taskrun -corpus FILE -policy FILE -revision REV [-guest NAME] [-url URL] [-worker-token SECRET] [-token TOKEN] [-remote FMT] [-out DIR] [-check PRIOR]")
		return 2
	}
	fail := func(err error) int {
		_, _ = fmt.Fprintln(errw, "taskrun:", err)
		return 1
	}
	c, err := loadCorpus(*corpusPath)
	if err != nil {
		return fail(err)
	}
	pol, err := os.ReadFile(*policyPath)
	if err != nil {
		return fail(err)
	}
	inputs := Report{
		Version: scorerVersion, Revision: *revision, Guest: *guest,
		PolicyDigest: digest(pol), CorpusDigest: c.digest, Limit: historyLimit,
	}
	// Evidence identity is known before any task runs, so a stale prior
	// report is refused before anything is admitted.
	if *prior != "" {
		var old Report
		b, err := os.ReadFile(*prior)
		if err != nil {
			return fail(err)
		}
		if err := json.Unmarshal(b, &old); err != nil {
			return fail(fmt.Errorf("prior report %s: %w", *prior, err))
		}
		if err := sameEvidence(old, inputs); err != nil {
			_, _ = fmt.Fprintln(errw, "taskrun: stale evidence:", err)
			return exitStale
		}
	}
	work, err := os.MkdirTemp("", "taskrun-checks-")
	if err != nil {
		return fail(err)
	}
	defer removeTree(work)
	r := runner{
		plane:   &plane{url: *url, worker: *worker, operator: *operator, http: &http.Client{Timeout: time.Minute}, poll: *poll},
		checks:  checker{remote: *remote, workDir: work, timeout: *checkTimeout},
		timeout: *taskTimeout,
	}
	report, err := r.run(ctx, c, inputs)
	if err != nil {
		return fail(err)
	}
	body, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fail(err)
	}
	body = append(body, '\n')
	_, _ = out.Write(body)
	if *outDir != "" {
		path, err := preserve(*outDir, report, body)
		if err != nil {
			return fail(err)
		}
		_, _ = fmt.Fprintln(errw, "taskrun: wrote", path)
	}
	return 0
}

type runner struct {
	plane   *plane
	checks  checker
	timeout time.Duration
}

// run admits and scores tasks one at a time, in corpus order.
func (r runner) run(ctx context.Context, c *Corpus, rep Report) (Report, error) {
	for _, t := range c.Tasks {
		res, err := r.task(ctx, c, t, rep)
		if err != nil {
			return rep, fmt.Errorf("task %s: %w", t.ID, err)
		}
		rep.Tasks = append(rep.Tasks, res)
	}
	rep.Totals = totals(rep.Tasks)
	return rep, nil
}

func (r runner) task(ctx context.Context, c *Corpus, t Task, rep Report) (TaskResult, error) {
	res := TaskResult{ID: t.ID}
	// Every evidence input is in the key, so a rerun resumes only sessions
	// that ran under the same inputs.
	effort := "taskrun-" + digest(fmt.Appendf(nil, "%d\x00%s\x00%s\x00%s\x00%s\x00%s",
		rep.Version, rep.Revision, rep.Guest, rep.PolicyDigest, rep.CorpusDigest, t.ID))[:24]
	start := time.Now()
	session, err := r.plane.admit(ctx, c, t, rep.Guest, effort)
	if err != nil {
		return res, err
	}
	wctx, cancel := context.WithTimeout(ctx, r.timeout)
	turns, err := r.plane.wait(wctx, c.Project, session)
	timedOut := errors.Is(wctx.Err(), context.DeadlineExceeded) && ctx.Err() == nil
	cancel()
	res.WallClockMS = time.Since(start).Milliseconds()
	if err != nil && timedOut {
		res.Status, res.Reason = StatusNotVerified, "timeout"
		return res, nil
	}
	if err != nil {
		return res, err
	}
	res.Interventions, res.Tokens = measures(turns)
	if err := r.plane.applyWrite(ctx, session); err != nil {
		return res, err
	}
	pub, err := r.plane.published(ctx, session)
	if err != nil {
		return res, err
	}
	if pub == nil || pub.SHA == "" {
		last := turns[len(turns)-1]
		res.Status, res.Reason = StatusNotVerified, "no published head ("+last.TerminalState+")"
		return res, nil
	}
	res.Head = pub.SHA
	v, err := r.checks.score(ctx, c, t, pub.SHA)
	if err != nil {
		return res, err
	}
	res.Checks = "fail"
	if v.headPass {
		res.Checks = "pass"
	}
	res.Regression = v.headPass && !v.gatePass
	res.Failure = v.failure
	switch {
	case !v.headPass || res.Regression:
		res.Status = StatusFailed
	case v.basePass:
		res.Status = StatusInvalid
	default:
		res.Status = StatusVerified
	}
	return res, nil
}

// measures counts interventions (permission decisions an operator made,
// and turns after the first) and sums tokens; tokens are unknown when any
// turn did not report them.
func measures(turns []measurement) (int, *int64) {
	interventions := len(turns) - 1
	var tokens int64
	known := true
	for _, m := range turns {
		if m.PermissionDecision != "" {
			interventions++
		}
		if m.TokensIn == nil || m.TokensOut == nil {
			known = false
			continue
		}
		tokens += *m.TokensIn + *m.TokensOut
	}
	if !known {
		return interventions, nil
	}
	return interventions, &tokens
}
