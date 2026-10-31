package gh

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/sannrox/rusui/internal/snapshot"
)

const githubAPI = "https://api.github.com"

type API struct {
	BaseURL string
	Token   string
	Tokens  TokenSource
	HookIDs map[string]string // repo -> hook id
	HTTP    *http.Client
}

func (a *API) bearer() string {
	if a.Tokens != nil {
		if t, err := a.Tokens.Token(); err == nil && t != "" {
			return t
		}
	}
	return a.Token
}

func NewAPI(token string, hookIDs map[string]string) *API {
	if hookIDs == nil {
		hookIDs = map[string]string{}
	}
	return &API{
		BaseURL: githubAPI,
		Token:   token,
		HookIDs: hookIDs,
		HTTP:    &http.Client{Timeout: 30 * time.Second},
	}
}

func ParseHookIDs(s string) map[string]string {
	out := map[string]string{}
	s = strings.TrimSpace(s)
	if s == "" {
		return out
	}
	for part := range strings.SplitSeq(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		repo, id, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		out[strings.TrimSpace(repo)] = strings.TrimSpace(id)
	}
	return out
}

func splitRepo(repo string) (owner, name string, err error) {
	if strings.Contains(repo, ":") {
		return "", "", fmt.Errorf("github: bad repo %q", repo)
	}
	owner, name, ok := strings.Cut(repo, "/")
	if !ok || owner == "" || name == "" {
		return "", "", fmt.Errorf("github: bad repo %q", repo)
	}
	return owner, name, nil
}

func (a *API) DefaultSHA(repo string) (string, error) {
	owner, name, err := splitRepo(repo)
	if err != nil {
		return "", err
	}
	var repoInfo ghRepo
	if err := a.get("/repos/"+owner+"/"+name, &repoInfo); err != nil {
		return "", err
	}
	branch := repoInfo.DefaultBranch
	if branch == "" {
		branch = "main"
	}
	var ref ghRef
	if err := a.get("/repos/"+owner+"/"+name+"/git/ref/heads/"+branch, &ref); err != nil {
		return "", err
	}
	if ref.Object.SHA == "" {
		return "", fmt.Errorf("github: empty sha")
	}
	return ref.Object.SHA, nil
}

func (a *API) get(path string, dst any) error {
	_, err := a.getPage(path, dst)
	return err
}

const maxGitHubPages = 50

func (a *API) getPage(pathOrURL string, dst any) (next string, err error) {
	u := pathOrURL
	if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		u = strings.TrimRight(a.BaseURL, "/") + pathOrURL
	}
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "rusui")
	if t := a.bearer(); t != "" {
		req.Header.Set("Authorization", "Bearer "+t)
	}
	res, err := a.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return "", err
	}
	if res.StatusCode == http.StatusNotFound {
		return "", errNotFound
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return "", fmt.Errorf("github %s: %s %s", pathOrURL, res.Status, truncate(body, 200))
	}
	next, err = a.nextPage(res.Header.Get("Link"))
	if err != nil {
		return "", err
	}
	if dst == nil {
		return next, nil
	}
	if err := json.Unmarshal(body, dst); err != nil {
		return "", err
	}
	return next, nil
}

func (a *API) nextPage(link string) (string, error) {
	next := parseLinkNext(link)
	if next == "" {
		return "", nil
	}
	u, err := url.Parse(next)
	if err != nil {
		return "", fmt.Errorf("github: bad Link next")
	}
	base, err := url.Parse(a.BaseURL)
	if err != nil {
		return "", err
	}
	if !strings.EqualFold(u.Host, base.Host) {
		return "", fmt.Errorf("github: Link next host %q refused", u.Host)
	}
	return next, nil
}

func parseLinkNext(h string) string {
	for part := range strings.SplitSeq(h, ",") {
		part = strings.TrimSpace(part)
		if !strings.Contains(part, `rel="next"`) && !strings.Contains(part, "rel=next") {
			continue
		}
		start := strings.Index(part, "<")
		end := strings.Index(part, ">")
		if start >= 0 && end > start {
			return part[start+1 : end]
		}
	}
	return ""
}

var errNotFound = fmt.Errorf("github: not found")

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "..."
}

type ghRepo struct {
	DefaultBranch string `json:"default_branch"`
	FullName      string `json:"full_name"`
}

type ghRef struct {
	Object struct {
		SHA string `json:"sha"`
	} `json:"object"`
}

type ghUser struct {
	Login string `json:"login"`
	Type  string `json:"type"`
}

type ghIssue struct {
	Number            int       `json:"number"`
	Title             string    `json:"title"`
	Body              string    `json:"body"`
	State             string    `json:"state"`
	Draft             bool      `json:"draft"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
	Labels            []ghLabel `json:"labels"`
	Assignees         []ghUser  `json:"assignees"`
	AuthorAssociation string    `json:"author_association"`
	PullRequest       *struct {
		URL string `json:"url"`
	} `json:"pull_request"`
}

type ghLabel struct {
	Name string `json:"name"`
}

type ghPR struct {
	Number            int       `json:"number"`
	Title             string    `json:"title"`
	Body              string    `json:"body"`
	HTMLURL           string    `json:"html_url"`
	State             string    `json:"state"`
	Draft             bool      `json:"draft"`
	Merged            bool      `json:"merged"`
	MergeCommitSHA    string    `json:"merge_commit_sha"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
	Labels            []ghLabel `json:"labels"`
	Assignees         []ghUser  `json:"assignees"`
	AuthorAssociation string    `json:"author_association"`
	Base              struct {
		Ref string `json:"ref"`
		SHA string `json:"sha"`
	} `json:"base"`
	Head struct {
		Ref string `json:"ref"`
		SHA string `json:"sha"`
	} `json:"head"`
}

type ghComment struct {
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
	User      struct {
		Login string `json:"login"`
		Type  string `json:"type"`
	} `json:"user"`
}

type ghCompare struct {
	Status string `json:"status"`
}

func (a *API) GetItem(repo string, item int, kind string) (snapshot.Item, error) {
	owner, name, err := splitRepo(repo)
	if err != nil {
		return snapshot.Item{}, err
	}
	var rr ghRepo
	if err := a.get(fmt.Sprintf("/repos/%s/%s", owner, name), &rr); err != nil {
		return snapshot.Item{}, err
	}
	defaultBranch := rr.DefaultBranch
	if defaultBranch == "" {
		defaultBranch = "main"
	}
	mainSHA := ""
	var ref ghRef
	if err := a.get(fmt.Sprintf("/repos/%s/%s/git/ref/heads/%s", owner, name, url.PathEscape(defaultBranch)), &ref); err == nil {
		mainSHA = ref.Object.SHA
	}

	var issue ghIssue
	if err := a.get(fmt.Sprintf("/repos/%s/%s/issues/%d", owner, name, item), &issue); err != nil {
		return snapshot.Item{}, err
	}
	isPR := kind == "pull" || issue.PullRequest != nil
	it := snapshot.Item{
		Repo:              repo,
		Item:              item,
		ItemKind:          "issue",
		State:             issue.State,
		Title:             issue.Title,
		Body:              issue.Body,
		Draft:             issue.Draft,
		UpdatedAt:         issue.UpdatedAt.UTC().Format(time.RFC3339),
		CreatedAt:         issue.CreatedAt.UTC().Format(time.RFC3339),
		DefaultBranch:     defaultBranch,
		MainSHA:           mainSHA,
		AuthorAssociation: issue.AuthorAssociation,
	}
	for _, l := range issue.Labels {
		it.Labels = append(it.Labels, l.Name)
	}
	for _, a := range issue.Assignees {
		if a.Login != "" {
			it.Assignees = append(it.Assignees, a.Login)
		}
	}
	if isPR {
		it.ItemKind = "pull"
		var pr ghPR
		if err := a.get(fmt.Sprintf("/repos/%s/%s/pulls/%d", owner, name, item), &pr); err != nil {
			return snapshot.Item{}, err
		}
		it.Draft = pr.Draft
		it.Merged = pr.Merged
		it.MergeCommitSHA = pr.MergeCommitSHA
		it.HeadSHA = pr.Head.SHA
		it.BaseSHA = pr.Base.SHA
		it.BaseRef = pr.Base.Ref
		it.MergedIntoDefault = pr.Merged && pr.Base.Ref == defaultBranch
		if pr.Title != "" {
			it.Title = pr.Title
		}
		if pr.Body != "" {
			it.Body = pr.Body
		}
		if len(pr.Labels) > 0 {
			it.Labels = nil
			for _, l := range pr.Labels {
				it.Labels = append(it.Labels, l.Name)
			}
		}
		if pr.AuthorAssociation != "" {
			it.AuthorAssociation = pr.AuthorAssociation
		}
		if len(pr.Assignees) > 0 {
			it.Assignees = nil
			for _, a := range pr.Assignees {
				if a.Login != "" {
					it.Assignees = append(it.Assignees, a.Login)
				}
			}
		}
	}

	var comments []ghComment
	_ = a.get(fmt.Sprintf("/repos/%s/%s/issues/%d/comments?per_page=100", owner, name, item), &comments)
	var last time.Time
	for _, c := range comments {
		if isBot(c.User.Type, c.User.Login) {
			continue
		}
		it.NonBotCommentCount++
		if c.CreatedAt.After(last) {
			last = c.CreatedAt
		}
	}
	if !last.IsZero() {
		it.LastNonBotCommentAt = last.UTC().Format(time.RFC3339)
	}
	it.LinkedSameRepoItems = LinkedSameRepo(it.Body, repo, item)
	return it, nil
}

func isBot(typ, login string) bool {
	if strings.EqualFold(typ, "Bot") {
		return true
	}
	return strings.HasSuffix(strings.ToLower(login), "[bot]")
}

var (
	hashNum       = regexp.MustCompile(`([\w.-]+/[\w.-]+)?#(\d+)`)
	githubItemURL = regexp.MustCompile(`(?:https?://)?github\.com/([\w.-]+/[\w.-]+)/(?:issues|pull)/(\d+)`)
)

// LinkedSameRepo returns same-repository item numbers referenced in body,
// excluding self. Bare #N, owner/repo#N for this repo, and github.com
// issue/pull URLs for this repo count; other repositories do not.
func LinkedSameRepo(body, repo string, self int) []int {
	seen := map[int]bool{self: true}
	var out []int
	add := func(n int) {
		if n == 0 || seen[n] {
			return
		}
		seen[n] = true
		out = append(out, n)
	}
	for _, m := range hashNum.FindAllStringSubmatchIndex(body, -1) {
		if m[0] > 0 && body[m[0]-1] == '&' {
			continue
		}
		if m[2] != -1 && !strings.EqualFold(body[m[2]:m[3]], repo) {
			continue
		}
		n, _ := strconv.Atoi(body[m[4]:m[5]])
		add(n)
	}
	for _, m := range githubItemURL.FindAllStringSubmatch(body, -1) {
		if !strings.EqualFold(m[1], repo) {
			continue
		}
		n, _ := strconv.Atoi(m[2])
		add(n)
	}
	return out
}

func (a *API) IsAncestor(repo, commit, defaultTip string) (bool, error) {
	if commit == "" || defaultTip == "" {
		return false, nil
	}
	if commit == defaultTip {
		return true, nil
	}
	owner, name, err := splitRepo(repo)
	if err != nil {
		return false, err
	}
	var cmp ghCompare
	path := fmt.Sprintf("/repos/%s/%s/compare/%s...%s", owner, name, url.PathEscape(commit), url.PathEscape(defaultTip))
	err = a.get(path, &cmp)
	if err == errNotFound {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return cmp.Status == "ahead" || cmp.Status == "identical", nil
}

func (a *API) GetItemExists(repo string, item int) (bool, error) {
	owner, name, err := splitRepo(repo)
	if err != nil {
		return false, err
	}
	var issue ghIssue
	err = a.get(fmt.Sprintf("/repos/%s/%s/issues/%d", owner, name, item), &issue)
	if err == errNotFound {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

type ghDeliveryList struct {
	ID          int64  `json:"id"`
	DeliveredAt string `json:"delivered_at"`
	Status      string `json:"status"`
}

type ghDeliveryDetail struct {
	ID          int64           `json:"id"`
	DeliveredAt string          `json:"delivered_at"`
	Event       string          `json:"event"`
	Payload     json.RawMessage `json:"payload"`
}

func (a *API) hookID(repo string) (string, error) {
	id := a.HookIDs[repo]
	if id == "" {
		return "", fmt.Errorf("github: no hook id for %s (set RUSUI_GITHUB_HOOK_IDS)", repo)
	}
	return id, nil
}

func (a *API) ListDeliveries(repo string) ([]DeliveryListItem, error) {
	owner, name, err := splitRepo(repo)
	if err != nil {
		return nil, err
	}
	hook, err := a.hookID(repo)
	if err != nil {
		return nil, err
	}
	var raw []ghDeliveryList
	if err := a.get(fmt.Sprintf("/repos/%s/%s/hooks/%s/deliveries?per_page=100", owner, name, url.PathEscape(hook)), &raw); err != nil {
		return nil, err
	}
	out := make([]DeliveryListItem, 0, len(raw))
	for _, d := range raw {
		out = append(out, DeliveryListItem{
			ID:          strconv.FormatInt(d.ID, 10),
			DeliveredAt: d.DeliveredAt,
			Status:      d.Status,
		})
	}
	return out, nil
}

func (a *API) GetDelivery(repo, id string) (*DeliveryDetail, error) {
	owner, name, err := splitRepo(repo)
	if err != nil {
		return nil, err
	}
	hook, err := a.hookID(repo)
	if err != nil {
		return nil, err
	}
	var raw ghDeliveryDetail
	if err := a.get(fmt.Sprintf("/repos/%s/%s/hooks/%s/deliveries/%s", owner, name, url.PathEscape(hook), url.PathEscape(id)), &raw); err != nil {
		return nil, err
	}
	return &DeliveryDetail{
		ID:          strconv.FormatInt(raw.ID, 10),
		DeliveredAt: raw.DeliveredAt,
		Event:       raw.Event,
		Payload:     raw.Payload,
	}, nil
}

// ListOpenItems lists a repository's open issues and pull requests with
// what advisory eligibility and catch-up need (ADR 0038 D2, D5): creation
// and update time, and for
// pull requests draft state, base branch, and the default branch.
func (a *API) ListOpenItems(repo string) ([]snapshot.Item, error) {
	owner, name, err := splitRepo(repo)
	if err != nil {
		return nil, err
	}
	var rr ghRepo
	if err := a.get(fmt.Sprintf("/repos/%s/%s", owner, name), &rr); err != nil {
		return nil, err
	}
	issues, err := getJSONPages[ghIssue](a, fmt.Sprintf("/repos/%s/%s/issues?state=open&per_page=100", owner, name))
	if err != nil {
		return nil, err
	}
	pulls, err := getJSONPages[ghPR](a, fmt.Sprintf("/repos/%s/%s/pulls?state=open&per_page=100", owner, name))
	if err != nil {
		return nil, err
	}
	byNumber := make(map[int]ghPR, len(pulls))
	for _, pr := range pulls {
		byNumber[pr.Number] = pr
	}
	out := make([]snapshot.Item, 0, len(issues))
	for _, issue := range issues {
		it := snapshot.Item{Repo: repo, Item: issue.Number, ItemKind: "issue", State: issue.State,
			Title: issue.Title, Body: issue.Body,
			CreatedAt: issue.CreatedAt.UTC().Format(time.RFC3339), UpdatedAt: issue.UpdatedAt.UTC().Format(time.RFC3339),
			AuthorAssociation: issue.AuthorAssociation}
		for _, l := range issue.Labels {
			it.Labels = append(it.Labels, l.Name)
		}
		for _, a := range issue.Assignees {
			if a.Login != "" {
				it.Assignees = append(it.Assignees, a.Login)
			}
		}
		if issue.PullRequest != nil {
			it.ItemKind = "pull"
			it.DefaultBranch = rr.DefaultBranch
			// A pull request missing from the pulls page keeps an empty
			// base, which advisory eligibility refuses.
			if pr, ok := byNumber[issue.Number]; ok {
				it.Draft = pr.Draft
				it.BaseRef = pr.Base.Ref
				if pr.Body != "" {
					it.Body = pr.Body
				}
			}
		}
		it.LinkedSameRepoItems = LinkedSameRepo(it.Body, repo, issue.Number)
		out = append(out, it)
	}
	return out, nil
}

func (a *API) ListHeads(repo string) ([]string, error) {
	owner, name, err := splitRepo(repo)
	if err != nil {
		return nil, err
	}
	refs, err := getJSONPages[struct {
		Ref string `json:"ref"`
	}](a, fmt.Sprintf("/repos/%s/%s/git/matching-refs/heads?per_page=100", owner, name))
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(refs))
	for _, r := range refs {
		out = append(out, strings.TrimPrefix(r.Ref, "refs/heads/"))
	}
	return out, nil
}

func getJSONPages[T any](a *API, path string) ([]T, error) {
	var all []T
	for n := 0; path != "" && n < maxGitHubPages; n++ {
		var page []T
		next, err := a.getPage(path, &page)
		if err != nil {
			return nil, err
		}
		all = append(all, page...)
		path = next
	}
	if path != "" {
		return nil, fmt.Errorf("github: list truncated after %d pages", maxGitHubPages)
	}
	return all, nil
}
