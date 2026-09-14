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
	HookIDs map[string]string // repo -> hook id
	HTTP    *http.Client
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
	owner, name, ok := strings.Cut(repo, "/")
	if !ok || owner == "" || name == "" {
		return "", "", fmt.Errorf("github: bad repo %q", repo)
	}
	return owner, name, nil
}

func (a *API) get(path string, dst any) error {
	u := strings.TrimRight(a.BaseURL, "/") + path
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "rusui")
	if a.Token != "" {
		req.Header.Set("Authorization", "Bearer "+a.Token)
	}
	res, err := a.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return err
	}
	if res.StatusCode == http.StatusNotFound {
		return errNotFound
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("github %s: %s %s", path, res.Status, truncate(body, 200))
	}
	if dst == nil {
		return nil
	}
	return json.Unmarshal(body, dst)
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

type ghIssue struct {
	Number      int       `json:"number"`
	Title       string    `json:"title"`
	Body        string    `json:"body"`
	State       string    `json:"state"`
	Draft       bool      `json:"draft"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	Labels      []ghLabel `json:"labels"`
	PullRequest *struct {
		URL string `json:"url"`
	} `json:"pull_request"`
}

type ghLabel struct {
	Name string `json:"name"`
}

type ghPR struct {
	Number         int       `json:"number"`
	Title          string    `json:"title"`
	Body           string    `json:"body"`
	State          string    `json:"state"`
	Draft          bool      `json:"draft"`
	Merged         bool      `json:"merged"`
	MergeCommitSHA string    `json:"merge_commit_sha"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
	Labels         []ghLabel `json:"labels"`
	Base           struct {
		Ref string `json:"ref"`
		SHA string `json:"sha"`
	} `json:"base"`
	Head struct {
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
		Repo:          repo,
		Item:          item,
		ItemKind:      "issue",
		State:         issue.State,
		Title:         issue.Title,
		Body:          issue.Body,
		Draft:         issue.Draft,
		UpdatedAt:     issue.UpdatedAt.UTC().Format(time.RFC3339),
		CreatedAt:     issue.CreatedAt.UTC().Format(time.RFC3339),
		DefaultBranch: defaultBranch,
		MainSHA:       mainSHA,
	}
	for _, l := range issue.Labels {
		it.Labels = append(it.Labels, l.Name)
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
	it.LinkedSameRepoItems = parseLinked(it.Body, item)
	return it, nil
}

func isBot(typ, login string) bool {
	if strings.EqualFold(typ, "Bot") {
		return true
	}
	return strings.HasSuffix(strings.ToLower(login), "[bot]")
}

var hashNum = regexp.MustCompile(`#(\d+)`)

func parseLinked(body string, self int) []int {
	seen := map[int]bool{self: true}
	var out []int
	for _, m := range hashNum.FindAllStringSubmatch(body, -1) {
		n, _ := strconv.Atoi(m[1])
		if n == 0 || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
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

func (a *API) ListOpenItems(repo string) ([]snapshot.Item, error) {
	owner, name, err := splitRepo(repo)
	if err != nil {
		return nil, err
	}
	var issues []ghIssue
	if err := a.get(fmt.Sprintf("/repos/%s/%s/issues?state=open&per_page=100", owner, name), &issues); err != nil {
		return nil, err
	}
	out := make([]snapshot.Item, 0, len(issues))
	for _, issue := range issues {
		kind := "issue"
		if issue.PullRequest != nil {
			kind = "pull"
		}
		out = append(out, snapshot.Item{Repo: repo, Item: issue.Number, ItemKind: kind, State: issue.State})
	}
	return out, nil
}
