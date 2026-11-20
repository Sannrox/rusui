package gl

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/sannrox/rusui/internal/snapshot"
)

const maxGitLabPages = 50

// Client is the read-only Issues API used to refresh a bound GitLab.com project.
type Client interface {
	GetIssue(projectID int64, path string, iid int) (snapshot.Item, error)
	ListIssues(projectID int64, path string, updatedAfter time.Time) ([]snapshot.Item, error)
}

// API talks to https://gitlab.com/api/v4 with a read_api token.
type API struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

func NewAPI(token string) *API {
	return &API{
		BaseURL: apiBase,
		Token:   token,
		HTTP:    &http.Client{Timeout: 30 * time.Second},
	}
}

func (a *API) GetIssue(projectID int64, path string, iid int) (snapshot.Item, error) {
	if projectID <= 0 || iid <= 0 {
		return snapshot.Item{}, fmt.Errorf("gitlab: project id and iid required")
	}
	var issue glIssue
	if err := a.get("/projects/"+strconv.FormatInt(projectID, 10)+"/issues/"+strconv.Itoa(iid), &issue); err != nil {
		return snapshot.Item{}, err
	}
	return itemFromIssue(projectID, path, issue)
}

func (a *API) ListIssues(projectID int64, path string, updatedAfter time.Time) ([]snapshot.Item, error) {
	if projectID <= 0 {
		return nil, fmt.Errorf("gitlab: project id required")
	}
	q := url.Values{}
	q.Set("state", "all")
	q.Set("confidential", "false")
	q.Set("per_page", "100")
	q.Set("order_by", "updated_at")
	q.Set("sort", "asc")
	if !updatedAfter.IsZero() {
		q.Set("updated_after", updatedAfter.UTC().Format(time.RFC3339))
	}
	pathURL := "/projects/" + strconv.FormatInt(projectID, 10) + "/issues?" + q.Encode()
	var out []snapshot.Item
	for range maxGitLabPages {
		var page []glIssue
		next, err := a.getPage(pathURL, &page)
		if err != nil {
			return nil, err
		}
		for _, issue := range page {
			it, err := itemFromIssue(projectID, path, issue)
			if err != nil {
				continue
			}
			out = append(out, it)
		}
		if next == "" {
			return out, nil
		}
		pathURL = next
	}
	return nil, fmt.Errorf("gitlab: too many issue pages")
}

type glIssue struct {
	IID          int       `json:"iid"`
	Title        string    `json:"title"`
	Description  string    `json:"description"`
	State        string    `json:"state"`
	Confidential bool      `json:"confidential"`
	Labels       []string  `json:"labels"`
	Assignees    []glUser  `json:"assignees"`
	UpdatedAt    time.Time `json:"updated_at"`
	CreatedAt    time.Time `json:"created_at"`
}

type glUser struct {
	Username string `json:"username"`
}

func (a *API) get(path string, dst any) error {
	_, err := a.getPage(path, dst)
	return err
}

func (a *API) getPage(pathOrURL string, dst any) (next string, err error) {
	u := pathOrURL
	if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		u = strings.TrimRight(a.BaseURL, "/") + pathOrURL
	}
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "rusui")
	if a.Token != "" {
		req.Header.Set("PRIVATE-TOKEN", a.Token)
	}
	res, err := a.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = res.Body.Close() }()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return "", err
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		msg := string(body)
		if len(msg) > 200 {
			msg = msg[:200] + "..."
		}
		return "", fmt.Errorf("gitlab %s: %s %s", pathOrURL, res.Status, msg)
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
		return "", fmt.Errorf("gitlab: bad Link next")
	}
	base, err := url.Parse(a.BaseURL)
	if err != nil {
		return "", err
	}
	if !strings.EqualFold(u.Host, base.Host) {
		return "", fmt.Errorf("gitlab: Link next host %q refused", u.Host)
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
