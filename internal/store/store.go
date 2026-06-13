// Package store persists run records and their logs on the filesystem.
// Layout: <dataDir>/runs/<task>/<id-timestamp>/{meta.json,output.log}
// plus <dataDir>/runs/<task>/history.jsonl as a fast list index.
package store

import (
	"io"

	"github.com/wanggang316/the-loop/internal/model"
)

// Store is the filesystem-backed run repository. A single daemon owns all
// writes, so implementations serialize writes in-process (no DB needed).
type Store interface {
	// Create allocates a new run for a task and returns it in pending state.
	Create(taskName string, trigger model.TriggerKind) (model.Run, error)

	// LogWriter returns a writer that appends to the run's output log.
	// The caller closes it when execution finishes.
	LogWriter(taskName string, runID int64) (io.WriteCloser, error)

	// Finish records the terminal status, exit code and finish time, and
	// updates the task's history index.
	Finish(taskName string, runID int64, status model.RunStatus, exitCode int) error

	// Get returns a single run record.
	Get(taskName string, runID int64) (model.Run, error)

	// List returns the most recent runs for a task, newest first.
	// A limit <= 0 means "all".
	List(taskName string, limit int) ([]model.Run, error)

	// Open returns a reader over the run's full output log.
	Open(taskName string, runID int64) (io.ReadCloser, error)

	// Tail returns up to n trailing lines of the run's log.
	Tail(taskName string, runID int64, n int) (string, error)
}
