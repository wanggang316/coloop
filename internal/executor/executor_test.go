package executor

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/wanggang316/the-loop/internal/model"
)

func TestShellExecutor(t *testing.T) {
	tests := []struct {
		name       string
		command    string
		wantStatus model.RunStatus
		wantExit   int
		wantOutput string // substring expected in captured output (empty => skip check)
	}{
		{
			name:       "success captures output",
			command:    "echo hello",
			wantStatus: model.StatusSuccess,
			wantExit:   0,
			wantOutput: "hello",
		},
		{
			name:       "non-zero exit reports failed",
			command:    "exit 1",
			wantStatus: model.StatusFailed,
			wantExit:   1,
		},
		{
			name:       "stderr is captured too",
			command:    "echo oops 1>&2; exit 3",
			wantStatus: model.StatusFailed,
			wantExit:   3,
			wantOutput: "oops",
		},
	}

	exec := NewShell("")
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			task := model.Task{
				Name:       tt.name,
				Type:       model.TaskShell,
				Command:    tt.command,
				WorkingDir: t.TempDir(),
			}

			var buf bytes.Buffer
			res, err := exec.Run(context.Background(), task, &buf)
			if err != nil {
				t.Fatalf("Run returned internal error: %v", err)
			}
			if res.Status != tt.wantStatus {
				t.Errorf("status = %q, want %q", res.Status, tt.wantStatus)
			}
			if res.ExitCode != tt.wantExit {
				t.Errorf("exit code = %d, want %d", res.ExitCode, tt.wantExit)
			}
			if tt.wantOutput != "" && !strings.Contains(buf.String(), tt.wantOutput) {
				t.Errorf("output = %q, want it to contain %q", buf.String(), tt.wantOutput)
			}
		})
	}
}

// TestShellExecutorTimeout verifies that a context deadline (the mechanism the
// scheduler uses to enforce task.Timeout) yields StatusTimeout for a long-
// running command, and that the command does not run to completion.
func TestShellExecutorTimeout(t *testing.T) {
	task := model.Task{
		Name:       "timeout",
		Type:       model.TaskShell,
		Command:    "sleep 5",
		WorkingDir: t.TempDir(),
		Timeout:    model.Duration(50 * time.Millisecond),
	}

	ctx, cancel := context.WithTimeout(context.Background(), task.Timeout.Std())
	defer cancel()

	exec := NewShell("")
	start := time.Now()

	var buf bytes.Buffer
	res, err := exec.Run(ctx, task, &buf)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Run returned internal error: %v", err)
	}
	if res.Status != model.StatusTimeout {
		t.Errorf("status = %q, want %q", res.Status, model.StatusTimeout)
	}
	// The process was killed, not finished naturally: it must return well before
	// the 5s sleep would have completed.
	if elapsed >= 5*time.Second {
		t.Errorf("Run took %v, expected it to be cut short by the deadline", elapsed)
	}
}

// TestShellExecutorTimeoutIgnoresSIGTERM verifies the kill path is forceful: a
// command that traps and ignores SIGTERM (and SIGINT/SIGHUP) must still be cut
// short by the deadline and reported as StatusTimeout. runCmd signals the whole
// process group with SIGKILL, which cannot be caught, so even a SIGTERM-immune
// process dies promptly rather than running to completion.
func TestShellExecutorTimeoutIgnoresSIGTERM(t *testing.T) {
	// Trap the catchable termination signals to no-ops, then sleep far past the
	// deadline. If runCmd relied on SIGTERM the process would survive its full
	// sleep; only an uncatchable SIGKILL on the group ends it on time.
	const command = "trap '' TERM INT HUP; sleep 30"

	task := model.Task{
		Name:       "timeout-ignores-sigterm",
		Type:       model.TaskShell,
		Command:    command,
		WorkingDir: t.TempDir(),
		Timeout:    model.Duration(100 * time.Millisecond),
	}

	ctx, cancel := context.WithTimeout(context.Background(), task.Timeout.Std())
	defer cancel()

	start := time.Now()
	var buf bytes.Buffer
	res, err := NewShell("").Run(ctx, task, &buf)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Run returned internal error: %v", err)
	}
	if res.Status != model.StatusTimeout {
		t.Errorf("status = %q, want %q", res.Status, model.StatusTimeout)
	}
	if res.ExitCode != -1 {
		t.Errorf("exit code = %d, want -1 for a killed (timed-out) run", res.ExitCode)
	}
	// "Promptly": must return well before the 30s sleep would finish. A generous
	// upper bound keeps the test robust on slow/loaded CI while still proving the
	// process was killed rather than allowed to run to completion.
	if elapsed >= 5*time.Second {
		t.Errorf("Run took %v, expected the SIGTERM-ignoring process to be SIGKILLed near the deadline", elapsed)
	}
}

// TestShellExecutorFastCommandNotTimeout guards against the TOCTOU misclassification:
// a quick command that exits successfully just inside a short timeout window must
// be reported by its real exit status, never mislabeled StatusTimeout, even though
// the context deadline expires almost immediately afterward.
func TestShellExecutorFastCommandNotTimeout(t *testing.T) {
	task := model.Task{
		Name:       "fast-not-timeout",
		Type:       model.TaskShell,
		Command:    "echo done",
		WorkingDir: t.TempDir(),
		Timeout:    model.Duration(50 * time.Millisecond),
	}

	exec := NewShell("")

	// Run repeatedly: the race only surfaces when the process finishes a hair
	// before the deadline, so we sample many short-timeout runs to make a stray
	// "timeout" reclassification likely to show up if the bug regressed.
	for i := 0; i < 50; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), task.Timeout.Std())
		var buf bytes.Buffer
		res, err := exec.Run(ctx, task, &buf)
		cancel()

		if err != nil {
			t.Fatalf("iteration %d: Run returned internal error: %v", i, err)
		}
		if res.Status != model.StatusSuccess {
			t.Fatalf("iteration %d: status = %q, want %q (a completed command must not be mislabeled timeout)",
				i, res.Status, model.StatusSuccess)
		}
		if res.ExitCode != 0 {
			t.Fatalf("iteration %d: exit code = %d, want 0", i, res.ExitCode)
		}
		if got := strings.TrimSpace(buf.String()); got != "done" {
			t.Fatalf("iteration %d: output = %q, want %q", i, got, "done")
		}
	}
}

func TestAgentExecutor(t *testing.T) {
	exec := NewAgent("")

	t.Run("renders CmdTmpl with Prompt", func(t *testing.T) {
		task := model.Task{
			Name:       "agent-tmpl",
			Type:       model.TaskAgent,
			Prompt:     "rendered-prompt",
			CmdTmpl:    "echo {{.Prompt}}",
			WorkingDir: t.TempDir(),
		}

		var buf bytes.Buffer
		res, err := exec.Run(context.Background(), task, &buf)
		if err != nil {
			t.Fatalf("Run returned internal error: %v", err)
		}
		if res.Status != model.StatusSuccess {
			t.Errorf("status = %q, want %q", res.Status, model.StatusSuccess)
		}
		if res.ExitCode != 0 {
			t.Errorf("exit code = %d, want 0", res.ExitCode)
		}
		if got := strings.TrimSpace(buf.String()); got != "rendered-prompt" {
			t.Errorf("output = %q, want %q", got, "rendered-prompt")
		}
	})

	t.Run("exports PROMPT into the environment", func(t *testing.T) {
		// The template prints $PROMPT, which is only set if the executor exported
		// it. The literal "$PROMPT" must not appear in the template itself, so the
		// only way the value reaches output is via the environment.
		task := model.Task{
			Name:       "agent-env",
			Type:       model.TaskAgent,
			Prompt:     "env-prompt-value",
			CmdTmpl:    "printf '%s' \"$PROMPT\"",
			WorkingDir: t.TempDir(),
		}

		var buf bytes.Buffer
		res, err := exec.Run(context.Background(), task, &buf)
		if err != nil {
			t.Fatalf("Run returned internal error: %v", err)
		}
		if res.Status != model.StatusSuccess {
			t.Errorf("status = %q, want %q", res.Status, model.StatusSuccess)
		}
		if got := buf.String(); got != "env-prompt-value" {
			t.Errorf("$PROMPT output = %q, want %q", got, "env-prompt-value")
		}
	})

	t.Run("empty CmdTmpl is an internal error", func(t *testing.T) {
		task := model.Task{
			Name:       "agent-empty",
			Type:       model.TaskAgent,
			WorkingDir: t.TempDir(),
		}

		var buf bytes.Buffer
		if _, err := exec.Run(context.Background(), task, &buf); err == nil {
			t.Fatal("expected an error for empty cmd_tmpl, got nil")
		}
	})
}
