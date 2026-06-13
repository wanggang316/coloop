// Package executor defines how a single task execution is carried out.
// Implementations (ShellExecutor, AgentExecutor) are selected by task type.
package executor

import (
	"context"
	"io"

	"github.com/wanggang316/the-loop/internal/model"
)

// Result is the terminal outcome of a single execution.
type Result struct {
	ExitCode int
	Status   model.RunStatus
}

// Executor runs one task execution, streaming combined stdout/stderr to w.
//
// It returns the terminal Result (success/failed/timeout with an exit code).
// A non-nil error indicates an internal failure to even start/execute the
// task (e.g. bad working dir); a non-zero process exit is NOT an error and is
// reported through Result instead.
type Executor interface {
	Run(ctx context.Context, task model.Task, w io.Writer) (Result, error)
}
