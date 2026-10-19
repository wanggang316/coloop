package executor

import (
	"context"
	"io"
	"os/exec"
)

// defaultShell is used when no shell is configured.
const defaultShell = "/bin/sh"

// ShellExecutor runs a task's Command through a shell with `-c`.
type ShellExecutor struct {
	shell string
}

// NewShell builds a ShellExecutor. An empty shell defaults to /bin/sh.
func NewShell(shell string) *ShellExecutor {
	if shell == "" {
		shell = defaultShell
	}
	return &ShellExecutor{shell: shell}
}

// Run executes task.Command via `shell -c`.
func (e *ShellExecutor) Run(ctx context.Context, inv Invocation, w io.Writer) (Result, error) {
	task := inv.Task
	cmd := exec.CommandContext(ctx, e.shell, "-c", task.Command)
	return runCmd(ctx, cmd, inv, w)
}
