package ops

import (
	"fmt"

	"github.com/sannrox/rusui/internal/store"
)

// InspectLive reads schema, global pause, and leased turns from a SQLite
// file without running migrations or taking the plane's write lock.
func InspectLive(path string) (schema int, paused bool, live []store.LiveTurn, err error) {
	if path == "" {
		return 0, false, nil, fmt.Errorf("diagnostics: db path required")
	}
	return store.InspectLive(path)
}
