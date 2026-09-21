package gh

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
)

// PullRequest is the remote publication object whose head must equal the
// proven candidate SHA.
type PullRequest struct {
	Number  int
	HeadSHA string
	HeadRef string
	Base    string
	Draft   bool
	HTMLURL string
	Title   string
	Body    string
}

var (
	_ Publisher = (*API)(nil)
	_ Publisher = (*FakePublisher)(nil)
)

// Publisher is the plane-owned GitHub write surface for ADR 0013.
// It is not part of Client. Guests and verifier processes must not hold it.
type Publisher interface {
	FindDraftPR(repo, headRef string) (*PullRequest, error)
	OpenDraftPR(repo, title, body, headRef, base string) (*PullRequest, error)
	GetPR(repo string, number int) (*PullRequest, error)
	DefaultBranch(repo string) (string, error)
}

func branchName(ref string) string {
	return strings.TrimPrefix(ref, "refs/heads/")
}

func (a *API) post(path string, payload any, dst any) error {
	var rdr io.Reader
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	u := strings.TrimRight(a.BaseURL, "/") + path
	req, err := http.NewRequest(http.MethodPost, u, rdr)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "rusui")
	req.Header.Set("Content-Type", "application/json")
	if t := a.bearer(); t != "" {
		req.Header.Set("Authorization", "Bearer "+t)
	}
	res, err := a.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return err
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("github %s: %s %s", path, res.Status, truncate(body, 200))
	}
	if dst == nil {
		return nil
	}
	return json.Unmarshal(body, dst)
}

func prFromAPI(p ghPR, headRef string) *PullRequest {
	return &PullRequest{
		Number:  p.Number,
		HeadSHA: p.Head.SHA,
		HeadRef: headRef,
		Base:    p.Base.Ref,
		Draft:   p.Draft,
		HTMLURL: p.HTMLURL,
		Title:   p.Title,
		Body:    p.Body,
	}
}

func (a *API) FindDraftPR(repo, headRef string) (*PullRequest, error) {
	owner, name, err := splitRepo(repo)
	if err != nil {
		return nil, err
	}
	headRef = branchName(headRef)
	var pulls []ghPR
	q := "/repos/" + owner + "/" + name + "/pulls?state=open&" + url.Values{"head": {owner + ":" + headRef}}.Encode()
	if err := a.get(q, &pulls); err != nil {
		return nil, err
	}
	for i := range pulls {
		if pulls[i].Draft {
			return prFromAPI(pulls[i], headRef), nil
		}
	}
	if len(pulls) == 0 {
		return nil, nil
	}
	return prFromAPI(pulls[0], headRef), nil
}

func (a *API) OpenDraftPR(repo, title, body, headRef, base string) (*PullRequest, error) {
	owner, name, err := splitRepo(repo)
	if err != nil {
		return nil, err
	}
	headRef = branchName(headRef)
	var p ghPR
	err = a.post("/repos/"+owner+"/"+name+"/pulls", map[string]any{
		"title": title,
		"body":  body,
		"head":  headRef,
		"base":  base,
		"draft": true,
	}, &p)
	if err != nil {
		return nil, err
	}
	return prFromAPI(p, headRef), nil
}

func (a *API) DefaultBranch(repo string) (string, error) {
	owner, name, err := splitRepo(repo)
	if err != nil {
		return "", err
	}
	var repoInfo ghRepo
	if err := a.get("/repos/"+owner+"/"+name, &repoInfo); err != nil {
		return "", err
	}
	if repoInfo.DefaultBranch == "" {
		return "", fmt.Errorf("github: empty default branch")
	}
	return repoInfo.DefaultBranch, nil
}

func (a *API) GetPR(repo string, number int) (*PullRequest, error) {
	owner, name, err := splitRepo(repo)
	if err != nil {
		return nil, err
	}
	var p ghPR
	if err := a.get(fmt.Sprintf("/repos/%s/%s/pulls/%d", owner, name, number), &p); err != nil {
		return nil, err
	}
	return prFromAPI(p, p.Head.Ref), nil
}

// FakePublisher is an in-process GitHub stand-in for publication tests.
type FakePublisher struct {
	Mu          sync.Mutex
	PRs         map[string]*PullRequest
	Next        int
	OpenErr     error
	OpenHeadSHA string
	Default     string
	AfterOpen   func(*PullRequest) error
	Calls       []string
}

func NewFakePublisher() *FakePublisher {
	return &FakePublisher{PRs: map[string]*PullRequest{}, Next: 1}
}

func (f *FakePublisher) key(repo, headRef string) string {
	return repo + "#" + branchName(headRef)
}

func (f *FakePublisher) FindDraftPR(repo, headRef string) (*PullRequest, error) {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	f.Calls = append(f.Calls, "find")
	pr := f.PRs[f.key(repo, headRef)]
	if pr == nil {
		return nil, nil
	}
	cp := *pr
	return &cp, nil
}

func (f *FakePublisher) OpenDraftPR(repo, title, body, headRef, base string) (*PullRequest, error) {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	f.Calls = append(f.Calls, "open")
	if f.OpenErr != nil {
		return nil, f.OpenErr
	}
	headRef = branchName(headRef)
	pr := &PullRequest{
		Number:  f.Next,
		HeadSHA: f.OpenHeadSHA,
		HeadRef: headRef,
		Base:    base,
		Draft:   true,
		HTMLURL: "https://github.com/" + repo + "/pull/",
		Title:   title,
		Body:    body,
	}
	f.Next++
	pr.HTMLURL += fmt.Sprint(pr.Number)
	f.PRs[f.key(repo, headRef)] = pr
	if f.AfterOpen != nil {
		if err := f.AfterOpen(pr); err != nil {
			return nil, err
		}
	}
	cp := *pr
	return &cp, nil
}

func (f *FakePublisher) GetPR(repo string, number int) (*PullRequest, error) {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	f.Calls = append(f.Calls, "get")
	for _, pr := range f.PRs {
		if pr.Number == number {
			cp := *pr
			return &cp, nil
		}
	}
	return nil, fmt.Errorf("github: not found")
}

func (f *FakePublisher) DefaultBranch(string) (string, error) {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	if f.Default == "" {
		return "main", nil
	}
	return f.Default, nil
}

func (f *FakePublisher) SetHead(repo, headRef, sha string) {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	if pr := f.PRs[f.key(repo, headRef)]; pr != nil {
		pr.HeadSHA = sha
	}
}

func (f *FakePublisher) OpenCount() int {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	n := 0
	for _, c := range f.Calls {
		if c == "open" {
			n++
		}
	}
	return n
}
