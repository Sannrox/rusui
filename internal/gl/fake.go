package gl

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/sannrox/rusui/internal/snapshot"
)

// Fake is an in-memory Issues API for tests. It never dials the network.
type Fake struct {
	Mu     sync.Mutex
	Issues map[string]*snapshot.Item
	Err    error
	Calls  int
}

func NewFake() *Fake {
	return &Fake{Issues: map[string]*snapshot.Item{}}
}

func issueKey(projectID int64, iid int) string {
	return fmt.Sprintf("%d#%d", projectID, iid)
}

func (f *Fake) Put(it snapshot.Item) {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	cp := it
	f.Issues[issueKey(it.ProjectID, it.Item)] = &cp
}

func (f *Fake) CallCount() int {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	return f.Calls
}

func (f *Fake) GetIssue(projectID int64, path string, iid int) (snapshot.Item, error) {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	f.Calls++
	if f.Err != nil {
		return snapshot.Item{}, f.Err
	}
	it := f.Issues[issueKey(projectID, iid)]
	if it == nil {
		return snapshot.Item{}, fmt.Errorf("gitlab: not found")
	}
	out := *it
	if path != "" {
		out.ProjectPath = path
	}
	return out, nil
}

func (f *Fake) ListIssues(projectID int64, path string, updatedAfter time.Time) ([]snapshot.Item, error) {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	f.Calls++
	if f.Err != nil {
		return nil, f.Err
	}
	var out []snapshot.Item
	prefix := fmt.Sprintf("%d#", projectID)
	for k, it := range f.Issues {
		if !strings.HasPrefix(k, prefix) {
			continue
		}
		if !updatedAfter.IsZero() && it.UpdatedAt != "" && it.UpdatedAt < updatedAfter.UTC().Format(time.RFC3339) {
			continue
		}
		cp := *it
		if path != "" {
			cp.ProjectPath = path
		}
		out = append(out, cp)
	}
	return out, nil
}
