package engine

import (
	"context"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/wanggang316/the-loop/internal/executor"
	"github.com/wanggang316/the-loop/internal/model"
)

// steppedExec emits a fixed number of numbered lines, one per release on step,
// so a test can advance the run line by line with small gaps. This lets a test
// attach StreamLog mid-run and exercise the replay/live boundary deterministically.
type steppedExec struct {
	total  int           // total lines to emit
	step   chan struct{} // test releases one line per send
	wrote  chan int      // executor reports the index it just wrote
	status model.RunStatus
}

func newSteppedExec(total int, status model.RunStatus) *steppedExec {
	return &steppedExec{
		total:  total,
		step:   make(chan struct{}),
		wrote:  make(chan int, total),
		status: status,
	}
}

func (s *steppedExec) Run(ctx context.Context, task model.Task, w io.Writer) (executor.Result, error) {
	for i := 0; i < s.total; i++ {
		select {
		case <-s.step:
		case <-ctx.Done():
			return executor.Result{Status: model.StatusTimeout, ExitCode: -1}, nil
		}
		io.WriteString(w, fmt.Sprintf("line-%d\n", i))
		// Small gap so the on-disk write and live publish are not instantaneous,
		// stressing the subscribe/replay window the test cares about.
		time.Sleep(2 * time.Millisecond)
		select {
		case s.wrote <- i:
		default:
		}
	}
	return executor.Result{Status: s.status, ExitCode: 0}, nil
}

// emit releases one line and waits for the executor to confirm it was written,
// so the caller knows the line is on disk and in the broadcaster history. It
// returns an error rather than failing the test directly, so it is safe to call
// from a spawned goroutine (the Go testing API forbids FailNow off the test
// goroutine).
func (s *steppedExec) emit() error {
	select {
	case s.step <- struct{}{}:
	case <-time.After(2 * time.Second):
		return fmt.Errorf("executor not ready to accept a step")
	}
	select {
	case <-s.wrote:
		return nil
	case <-time.After(2 * time.Second):
		return fmt.Errorf("executor did not confirm a written line")
	}
}

// TestStreamLogExactlyOnceAcrossReplayLiveBoundary attaches StreamLog to a
// RUNNING task that has already emitted some lines (replay region) and then emits
// more lines (live region) with small gaps. It asserts every emitted line is
// delivered exactly once -- no loss across the replay/live seam, no duplication
// of a line that straddles subscribe -- and that exactly one Done event arrives
// last carrying the terminal status.
func TestStreamLogExactlyOnceAcrossReplayLiveBoundary(t *testing.T) {
	const total = 6
	exec := newSteppedExec(total, model.StatusSuccess)
	cfg := &model.Config{Tasks: []model.Task{{Name: "t", Type: model.TaskShell, Timeout: model.Duration(10 * time.Second)}}}
	e, _ := newTestEngine(t, cfg, exec)

	id, err := e.Trigger("t", model.TriggerManual)
	if err != nil {
		t.Fatalf("Trigger: %v", err)
	}
	waitFor(t, func() bool { return e.Running("t") })

	// Replay region: emit the first half before subscribing. Each emit confirms
	// the line is persisted and recorded in the broadcaster history.
	const before = 3
	for i := 0; i < before; i++ {
		if err := exec.emit(); err != nil {
			t.Fatalf("replay emit %d: %v", i, err)
		}
	}

	// Attach mid-run. The first `before` lines must be replayed; the rest arrive
	// live. The broadcaster captures the history snapshot atomically with the
	// live subscription, so no line in this window is dropped or duplicated.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ch, err := e.StreamLog(ctx, "t", id)
	if err != nil {
		t.Fatalf("StreamLog: %v", err)
	}

	// Live region: emit the remaining lines with the same small gaps, then let
	// the run terminate. Run in a goroutine so the test can drain ch concurrently;
	// any emit failure is surfaced on emitErr and checked after the stream closes.
	emitErr := make(chan error, 1)
	go func() {
		for i := before; i < total; i++ {
			if err := exec.emit(); err != nil {
				emitErr <- fmt.Errorf("live emit %d: %w", i, err)
				return
			}
		}
		emitErr <- nil
	}()

	// Collect every event from the stream until it closes.
	counts := make(map[string]int)
	var order []string
	doneCount := 0
	var doneStatus model.RunStatus
	for ev := range ch {
		if ev.Done {
			doneCount++
			doneStatus = ev.Status
			continue
		}
		counts[ev.Line]++
		order = append(order, ev.Line)
	}

	// Surface any failure from the live-emit goroutine.
	if err := <-emitErr; err != nil {
		t.Fatalf("%v", err)
	}

	// Exactly one Done, carrying the terminal status, and no line after Done was
	// possible because the loop ends when the channel closes (Done is last).
	if doneCount != 1 {
		t.Fatalf("Done events = %d, want exactly 1", doneCount)
	}
	if doneStatus != model.StatusSuccess {
		t.Fatalf("Done status = %q, want success", doneStatus)
	}

	// Every emitted line appears exactly once: no loss, no duplication.
	if len(order) != total {
		t.Fatalf("got %d log events %v, want %d distinct lines", len(order), order, total)
	}
	for i := 0; i < total; i++ {
		want := fmt.Sprintf("line-%d", i)
		switch counts[want] {
		case 0:
			t.Fatalf("line %q missing (loss across replay/live boundary): got %v", want, order)
		case 1:
			// exactly once: good
		default:
			t.Fatalf("line %q delivered %d times (duplication across boundary): got %v", want, counts[want], order)
		}
	}

	// Lines must also be delivered in emission order (replay region precedes the
	// live region, each region in order).
	for i, line := range order {
		want := fmt.Sprintf("line-%d", i)
		if line != want {
			t.Fatalf("out-of-order delivery at %d: got %q want %q (full: %v)", i, line, want, order)
		}
	}
}
