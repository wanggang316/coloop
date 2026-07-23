package executor

import (
	"context"
	"io"
	"os"
	"os/exec"
	"sort"
	"syscall"
	"time"

	"github.com/wanggang316/coloop/internal/model"
)

// waitDelay bounds how long cmd.Wait blocks after the process is signalled.
// If a descendant survives the process-group kill and keeps the stdout/stderr
// pipe open, os/exec force-closes the I/O pipes after this delay so Wait (and
// thus runCmd) returns promptly instead of leaking a goroutine and the
// engine's single-flight slot.
const waitDelay = 5 * time.Second

// runCmd wires a prepared command to the task's environment and working dir,
// runs it in its own process group, streams combined output to w, and maps the
// outcome to a Result.
//
// extraEnv carries executor-specific variables (e.g. the agent's PROMPT /
// TASK_NAME) that are layered on top of the process environment and task.Env.
//
// On a context deadline the whole process group is killed so child processes
// die with the parent. A non-nil error signals an internal failure to start or
// wait on the process; a non-zero exit is reported through Result instead.
func runCmd(ctx context.Context, cmd *exec.Cmd, task model.Task, w io.Writer, extraEnv ...string) (Result, error) {
	if task.WorkingDir != "" {
		cmd.Dir = task.WorkingDir
	}
	cmd.Stdout = w
	cmd.Stderr = w
	cmd.Env = buildEnv(task.Env, extraEnv)

	// Place the child in its own process group so we can signal the whole tree.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	// Bound how long Wait blocks after the process is signalled: if a
	// descendant escapes the group kill and keeps the output pipe open, the
	// stdout/stderr copy goroutine never sees EOF and Wait would otherwise
	// block forever. WaitDelay lets os/exec force-close the pipes so Wait
	// returns. Cancel is a no-op because we kill the whole process group
	// ourselves below (os/exec would only signal the leader); it must be set
	// for WaitDelay to take effect on context cancellation.
	cmd.WaitDelay = waitDelay
	cmd.Cancel = func() error { return nil }

	if err := cmd.Start(); err != nil {
		return Result{}, err
	}

	pgid := cmd.Process.Pid

	// Run cmd.Wait off the goroutine and decide the outcome from how the
	// process actually terminated, not from a re-read of ctx.Err(). Only a run
	// that WE killed because the context fired is a timeout; a process that
	// exits on its own a hair before the deadline must keep its real status.
	waitErr := make(chan error, 1)
	go func() { waitErr <- cmd.Wait() }()

	var killedByTimeout bool
	select {
	case <-ctx.Done():
		killedByTimeout = ctx.Err() == context.DeadlineExceeded
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
		<-waitErr // reap the process (bounded by WaitDelay)
	case <-waitErr:
	}

	if killedByTimeout {
		return Result{Status: model.StatusTimeout, ExitCode: -1}, nil
	}

	// ProcessState.ExitCode reports the exit code carried by an *exec.ExitError;
	// it returns -1 when the process was signalled.
	if code := cmd.ProcessState.ExitCode(); code == 0 {
		return Result{Status: model.StatusSuccess, ExitCode: 0}, nil
	} else {
		return Result{Status: model.StatusFailed, ExitCode: code}, nil
	}
}

// buildEnv layers task.Env and extra variables on top of the inherited
// environment, in a stable order.
func buildEnv(taskEnv map[string]string, extra []string) []string {
	env := os.Environ()
	if len(taskEnv) > 0 {
		keys := make([]string, 0, len(taskEnv))
		for k := range taskEnv {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			env = append(env, k+"="+taskEnv[k])
		}
	}
	env = append(env, extra...)
	return env
}
