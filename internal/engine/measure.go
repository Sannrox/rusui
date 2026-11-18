package engine

import (
	"encoding/json"
	"time"

	"github.com/sannrox/rusui/internal/store"
)

func measurementPublication(r *TaskResult) string {
	if r == nil {
		return ""
	}
	switch r.Outcome {
	case OutcomePublished:
		return "succeeded"
	case OutcomeUnconfirmed:
		return "uncertain"
	case OutcomeBlocked:
		if r.PullRequest > 0 {
			return "failed"
		}
	}
	return ""
}

// turnTokensTx sums the usage on the turn's model.proxy rows: input is
// uncached input plus cache writes, output is output. Both are nil when the
// turn made no call through the model proxy, or when the plane may have
// missed some usage (a row without full usage, a summary dropped while the
// turn ran, or a turn started before this process). Usage a runner or guest
// reports is never counted.
func (e *Engine) turnTokensTx(tx *store.Tx, turnID int64) (in, out *int64, err error) {
	bodies, err := store.TurnActionBodiesTx(tx, turnID, "model.proxy")
	if err != nil || len(bodies) == 0 {
		return nil, nil, err
	}
	started, err := store.MeasurementStartedTx(tx, turnID)
	if err != nil {
		return nil, nil, err
	}
	e.modelMu.Lock()
	lost := e.modelUsageLostAt
	e.modelMu.Unlock()
	if started.IsZero() || started.Before(lost) {
		return nil, nil, nil
	}
	var total ModelUsage
	for _, body := range bodies {
		// Rows from before usage was recorded have no usage object.
		var row struct {
			Usage        *ModelUsage `json:"usage"`
			UsageUnknown bool        `json:"usage_unknown"`
		}
		if json.Unmarshal([]byte(body), &row) != nil || row.Usage == nil || row.UsageUnknown {
			return nil, nil, nil
		}
		total.add(*row.Usage)
	}
	in, out = new(total.Input+total.CacheWrite), new(total.Output)
	return in, out, nil
}

func (e *Engine) finishMeasurementTx(tx *store.Tx, turnID int64, terminal string, art Artifact, ended time.Time) error {
	if err := e.recordModelSummaryTx(tx, turnID); err != nil {
		e.Log.Printf("model summary: %v", err)
	}
	tokensIn, tokensOut, err := e.turnTokensTx(tx, turnID)
	if err != nil {
		return err
	}
	if err := store.FinishMeasurementTx(tx, turnID, terminal, ended, measurementPublication(art.Result), tokensIn, tokensOut); err != nil {
		return err
	}
	if terminal != "completed" && terminal != "failed" {
		return nil
	}
	return e.recordChildOutcomeTx(tx, turnID, terminal)
}
