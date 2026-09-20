package engine

import "github.com/sannrox/rusui/internal/store"

// DrainReport is the operator view of in-flight work after new claims stop.
type DrainReport struct {
	Paused bool             `json:"paused"`
	Live   []store.LiveTurn `json:"live"`
}

// Drain pauses new claims globally and lists leased turns. It does not
// cancel live leases or delete receipts.
func (e *Engine) Drain() (DrainReport, error) {
	if err := e.SetPause("", true); err != nil {
		return DrainReport{}, err
	}
	live, err := store.ListLeasedTurns(e.Store)
	if err != nil {
		return DrainReport{}, err
	}
	if live == nil {
		live = []store.LiveTurn{}
	}
	return DrainReport{Paused: true, Live: live}, nil
}
