package engine

import (
	"database/sql"
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

func (e *Engine) finishMeasurementTx(tx *sql.Tx, turnID int64, terminal string, art Artifact, ended time.Time) error {
	if err := e.recordModelSummaryTx(tx, turnID); err != nil {
		e.Log.Printf("model summary: %v", err)
	}
	if err := store.FinishMeasurementTx(tx, turnID, terminal, ended, measurementPublication(art.Result), art.InputTokens, art.OutputTokens); err != nil {
		return err
	}
	if terminal != "completed" && terminal != "failed" {
		return nil
	}
	return e.recordChildOutcomeTx(tx, turnID, terminal)
}
