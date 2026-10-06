package gh

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// PullSpec is one pull request the plane creates or updates for a session
// branch in the same repository.
type PullSpec struct {
	Number int // update this pull request when > 0
	Head   string
	SHA    string // commit Head must point at; required
	Base   string
	Title  string
	Body   string
}

// Publisher creates or updates pull requests as the GitHub App with a
// token scoped to the one repository (ADR 0020, ADR 0044). PushBase
// fast-forwards the default branch (ADR 0068). It has no merge, close,
// label, or release call.
type Publisher struct {
	Tokens  *InstallationTokens
	BaseURL string
	HTTP    *http.Client
}

// PublishPull updates s.Number, or the open pull request whose head is
// s.Head, or creates one, only while s.Head points at s.SHA. s.Number must name the open pull request for
// s.Head in repo; anything else is refused rather than edited. Looking up
// by head first, and again after a failed create, makes a lost response
// update instead of duplicate.
func (p *Publisher) PublishPull(repo string, s PullSpec) (int, error) {
	owner, name, err := splitRepo(repo)
	if err != nil {
		return 0, err
	}
	tok, err := p.Tokens.RepoToken(repo)
	if err != nil {
		return 0, err
	}
	if err := p.requireHead(tok, "/repos/"+owner+"/"+name, s); err != nil {
		return 0, err
	}
	pulls := "/repos/" + owner + "/" + name + "/pulls"
	n := s.Number
	if n > 0 {
		var cur struct {
			State string `json:"state"`
			Head  struct {
				Ref  string `json:"ref"`
				Repo struct {
					FullName string `json:"full_name"`
				} `json:"repo"`
			} `json:"head"`
		}
		if err := p.do(tok, http.MethodGet, pulls+"/"+strconv.Itoa(n), nil, &cur); err != nil {
			return 0, err
		}
		if cur.State != "open" || cur.Head.Ref != s.Head || !strings.EqualFold(cur.Head.Repo.FullName, repo) {
			return 0, fmt.Errorf("github: pull request #%d is not the open pull request for %s", n, s.Head)
		}
	} else if n, err = p.openFor(tok, pulls, owner, s.Head); err != nil {
		return 0, err
	}
	if n > 0 {
		return p.update(tok, pulls, n, s)
	}
	var out struct {
		Number int `json:"number"`
	}
	err = p.do(tok, http.MethodPost, pulls, map[string]string{"title": s.Title, "body": s.Body, "head": s.Head, "base": s.Base}, &out)
	if err != nil {
		if n, lookErr := p.openFor(tok, pulls, owner, s.Head); lookErr == nil && n > 0 {
			return p.update(tok, pulls, n, s)
		}
		return 0, err
	}
	if out.Number <= 0 {
		return 0, fmt.Errorf("github: pull request number missing")
	}
	return out.Number, nil
}

// PushBase fast-forwards branch to sha with force=false. It does not
// open a pull request, merge one, comment, or close an issue (ADR 0068).
func (p *Publisher) PushBase(repo, branch, sha string) error {
	owner, name, err := splitRepo(repo)
	if err != nil {
		return err
	}
	tok, err := p.Tokens.RepoToken(repo)
	if err != nil {
		return err
	}
	if sha == "" {
		return fmt.Errorf("%w: no commit named", ErrHeadMismatch)
	}
	escaped, err := escapeRefHeads(branch)
	if err != nil {
		return err
	}
	var out struct {
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	return p.do(tok, http.MethodPatch, "/repos/"+owner+"/"+name+"/git/refs/heads/"+escaped, map[string]any{"sha": sha, "force": false}, &out)
}

// ErrHeadMismatch refuses a publication whose branch does not point at
// the commit the plane was asked to publish.
var ErrHeadMismatch = errors.New("github: branch head is not the publication commit")

// requireHead checks, just before the write, that s.Head points at s.SHA.
// A push after this check can still move the branch; the result's
// observation records the head GitHub shows afterwards.
func (p *Publisher) requireHead(tok, repoPath string, s PullSpec) error {
	if s.SHA == "" {
		return fmt.Errorf("%w: no commit named", ErrHeadMismatch)
	}
	var ref struct {
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	escaped, err := escapeRefHeads(s.Head)
	if err != nil {
		return err
	}
	err = p.do(tok, http.MethodGet, repoPath+"/git/ref/heads/"+escaped, nil, &ref)
	var se *statusError
	if errors.As(err, &se) && se.code == http.StatusNotFound {
		return fmt.Errorf("%w: %s does not exist", ErrHeadMismatch, s.Head)
	}
	if err != nil {
		return err
	}
	if ref.Object.SHA != s.SHA {
		return fmt.Errorf("%w: %s is at %s, not %s", ErrHeadMismatch, s.Head, ref.Object.SHA, s.SHA)
	}
	return nil
}

// statusError is a non-2xx GitHub response.
type statusError struct {
	code int
	msg  string
}

func (e *statusError) Error() string { return e.msg }

// openFor returns the open pull request whose head is branch, or 0.
func (p *Publisher) openFor(tok, pulls, owner, branch string) (int, error) {
	var open []struct {
		Number int `json:"number"`
	}
	q := url.Values{"state": {"open"}, "head": {owner + ":" + branch}}
	if err := p.do(tok, http.MethodGet, pulls+"?"+q.Encode(), nil, &open); err != nil {
		return 0, err
	}
	if len(open) == 0 {
		return 0, nil
	}
	return open[0].Number, nil
}

func escapeRefHeads(branch string) (string, error) {
	segs := strings.Split(branch, "/")
	for i, seg := range segs {
		if seg == "" || seg == "." || seg == ".." {
			return "", fmt.Errorf("%w: invalid branch %q", ErrHeadMismatch, branch)
		}
		segs[i] = url.PathEscape(seg)
	}
	return strings.Join(segs, "/"), nil
}

func (p *Publisher) update(tok, pulls string, n int, s PullSpec) (int, error) {
	var out struct {
		Number int `json:"number"`
	}
	if err := p.do(tok, http.MethodPatch, pulls+"/"+strconv.Itoa(n), map[string]string{"title": s.Title, "body": s.Body}, &out); err != nil {
		return 0, err
	}
	return n, nil
}

func (p *Publisher) do(tok, method, path string, in, dst any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	base := p.BaseURL
	if base == "" {
		base = githubAPI
	}
	req, err := http.NewRequest(method, strings.TrimRight(base, "/")+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "rusui")
	req.Header.Set("Authorization", "Bearer "+tok)
	cli := p.HTTP
	if cli == nil {
		cli = http.DefaultClient
	}
	res, err := cli.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		return err
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return &statusError{code: res.StatusCode, msg: fmt.Sprintf("github %s %s: %s %s", method, path, res.Status, truncate(raw, 200))}
	}
	return json.Unmarshal(raw, dst)
}
