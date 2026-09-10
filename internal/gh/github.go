package gh

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/sannrox/rusui/internal/snapshot"
)

type Client interface {
	GetItem(repo string, item int, kind string) (snapshot.Item, error)
	IsAncestor(repo, commit, defaultTip string) (bool, error)
	GetItemExists(repo string, item int) (bool, error)
	ListDeliveries(repo string) ([]DeliveryListItem, error)
	GetDelivery(repo, id string) (*DeliveryDetail, error)
}

type DeliveryListItem struct {
	ID          string `json:"id"`
	DeliveredAt string `json:"delivered_at"`
	Status      string `json:"status"`
}

type DeliveryDetail struct {
	ID          string          `json:"id"`
	DeliveredAt string          `json:"delivered_at"`
	Event       string          `json:"event"`
	Payload     json.RawMessage `json:"payload"`
}

func Sign(secret string, body []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write(body)
	return "sha256=" + hex.EncodeToString(m.Sum(nil))
}

func Verify(secret string, sig string, body []byte) bool {
	want := Sign(secret, body)
	return hmac.Equal([]byte(want), []byte(sig))
}

type ItemRec struct {
	snapshot.Item
	Comments []Comment
	Patch    string
}

type Comment struct {
	User      string
	Body      string
	CreatedAt string
	Bot       bool
}

type Fake struct {
	Mu            sync.Mutex
	Items         map[string]*ItemRec // repo#n
	Deliveries    []DeliveryDetail
	DetailFailIDs map[string]int
	DetailFails   map[string]int
	FetchDelay    time.Duration
	FetchHang     time.Duration
	BlockFetch    chan struct{}
	BlockVerify   chan struct{}
	FetchCalls    int
	HookCalls     int
}

func NewFake() *Fake {
	return &Fake{
		Items:         map[string]*ItemRec{},
		DetailFailIDs: map[string]int{},
		DetailFails:   map[string]int{},
	}
}

func key(repo string, item int) string { return fmt.Sprintf("%s#%d", repo, item) }

func (f *Fake) Put(it snapshot.Item) {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	k := key(it.Repo, it.Item)
	cur := f.Items[k]
	if cur == nil {
		f.Items[k] = &ItemRec{Item: it}
		return
	}
	cur.Item = it
}

func (f *Fake) SetFetchHang(d time.Duration) {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	f.FetchHang = d
}

func (f *Fake) SetBlockFetch(ch chan struct{}) {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	f.BlockFetch = ch
}

func (f *Fake) CloseBlockFetch() {
	f.Mu.Lock()
	ch := f.BlockFetch
	f.Mu.Unlock()
	if ch != nil {
		close(ch)
	}
}

func (f *Fake) CallCount() int {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	return f.FetchCalls
}

func (f *Fake) GetItem(repo string, item int, kind string) (snapshot.Item, error) {
	f.Mu.Lock()
	delay, hang := f.FetchDelay, f.FetchHang
	block := f.BlockFetch
	f.FetchCalls++
	f.Mu.Unlock()
	if hang > 0 {
		time.Sleep(hang)
	}
	if delay > 0 {
		time.Sleep(delay)
	}
	if block != nil {
		<-block
	}
	f.Mu.Lock()
	defer f.Mu.Unlock()
	rec := f.Items[key(repo, item)]
	if rec == nil {
		return snapshot.Item{}, fmt.Errorf("not found")
	}
	return rec.Item, nil
}

func (f *Fake) IsAncestor(repo, commit, defaultTip string) (bool, error) {
	f.Mu.Lock()
	ch := f.BlockVerify
	f.Mu.Unlock()
	if ch != nil {
		<-ch
	}
	f.Mu.Lock()
	defer f.Mu.Unlock()
	if commit == "" || defaultTip == "" {
		return false, nil
	}
	if commit == defaultTip {
		return true, nil
	}
	for _, rec := range f.Items {
		if rec.Item.Repo != repo {
			continue
		}
		if rec.Item.MergeCommitSHA == commit && rec.Item.MergedIntoDefault && rec.Item.MainSHA == defaultTip {
			return true, nil
		}
		if rec.Item.MergeCommitSHA == commit && rec.Item.MergedIntoDefault {
			return true, nil
		}
	}
	return false, nil
}

func (f *Fake) GetItemExists(repo string, item int) (bool, error) {
	f.Mu.Lock()
	ch := f.BlockVerify
	f.Mu.Unlock()
	if ch != nil {
		<-ch
	}
	f.Mu.Lock()
	defer f.Mu.Unlock()
	_, ok := f.Items[key(repo, item)]
	return ok, nil
}

func (f *Fake) ListDeliveries(repo string) ([]DeliveryListItem, error) {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	var out []DeliveryListItem
	for _, d := range f.Deliveries {
		out = append(out, DeliveryListItem{ID: d.ID, DeliveredAt: d.DeliveredAt, Status: "OK"})
	}
	return out, nil
}

func (f *Fake) GetDelivery(repo, id string) (*DeliveryDetail, error) {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	if n, ok := f.DetailFailIDs[id]; ok {
		f.DetailFails[id]++
		if f.DetailFails[id] <= n {
			return nil, fmt.Errorf("detail fail")
		}
	}
	for i := range f.Deliveries {
		if f.Deliveries[i].ID == id {
			d := f.Deliveries[i]
			return &d, nil
		}
	}
	return nil, fmt.Errorf("no delivery")
}

func (f *Fake) Handler(secret string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /repos/{owner}/{repo}/hooks/{id}/deliveries/{did}", func(w http.ResponseWriter, r *http.Request) {
		repo := r.PathValue("owner") + "/" + r.PathValue("repo")
		d, err := f.GetDelivery(repo, r.PathValue("did"))
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		json.NewEncoder(w).Encode(d)
	})
	mux.HandleFunc("GET /repos/{owner}/{repo}/hooks/{id}/deliveries", func(w http.ResponseWriter, r *http.Request) {
		repo := r.PathValue("owner") + "/" + r.PathValue("repo")
		list, _ := f.ListDeliveries(repo)
		json.NewEncoder(w).Encode(list)
	})
	mux.HandleFunc("GET /repos/{owner}/{repo}/issues/{n}", func(w http.ResponseWriter, r *http.Request) {
		var n int
		fmt.Sscanf(r.PathValue("n"), "%d", &n)
		repo := r.PathValue("owner") + "/" + r.PathValue("repo")
		it, err := f.GetItem(repo, n, "issue")
		if err != nil {
			http.Error(w, err.Error(), 404)
			return
		}
		json.NewEncoder(w).Encode(it)
	})
	return mux
}

func ParseWebhook(body []byte) (repo string, item int, kind string, err error) {
	var p struct {
		Repository struct {
			FullName string `json:"full_name"`
		} `json:"repository"`
		Issue *struct {
			Number int `json:"number"`
		} `json:"issue"`
		PullRequest *struct {
			Number int `json:"number"`
		} `json:"pull_request"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		return "", 0, "", err
	}
	repo = p.Repository.FullName
	if p.PullRequest != nil {
		return repo, p.PullRequest.Number, "pull", nil
	}
	if p.Issue != nil {
		kind := "issue"
		return repo, p.Issue.Number, kind, nil
	}
	return "", 0, "", fmt.Errorf("no item")
}

func ReadBody(r *http.Request) ([]byte, error) {
	return io.ReadAll(r.Body)
}

func EventKind(event string) string {
	if strings.HasPrefix(event, "pull") {
		return "pull"
	}
	return "issue"
}
