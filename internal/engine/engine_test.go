package engine

import (
	"context"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/wanggang316/the-loop/internal/executor"
	"github.com/wanggang316/the-loop/internal/model"
	"github.com/wanggang316/the-loop/internal/notify"
	"github.com/wanggang316/the-loop/internal/store"
)

func newTestEngine(t *testing.T, cfg *model.Config, exec executor.Executor) (*Engine, store.Store) {
	t.Helper()
	st, err := store.New(t.TempDir())
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	disp := notify.NewDispatcher(nil) // no channels: dispatch is a no-op
	e := New(cfg, st, disp, exec, exec)
	return e, st
}

// blockingExec writes a fixed set of lines, then blocks until released, so tests
// can observe the in-flight state deterministically.
type blockingExec struct {
	lines   []string
	release chan struct{}
	status  model.RunStatus
	exit    int
}

func (b *blockingExec) Run(ctx context.Context, task model.Task, w io.Writer) (executor.Result, error) {
	for _, l := range b.lines {
		io.WriteString(w, l+"\n")
	}
	select {
	case <-b.release:
	case <-ctx.Done():
		return executor.Result{Status: model.StatusTimeout, ExitCode: -1}, nil
	}
	return executor.Result{Status: b.status, ExitCode: b.exit}, nil
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met within deadline")
}

func TestTriggerSingleFlight(t *testing.T) {
	exec := &blockingExec{release: make(chan struct{}), status: model.StatusSuccess}
	cfg := &model.Config{Tasks: []model.Task{{Name: "t", Type: model.TaskShell, Command: "echo hi"}}}
	e, _ := newTestEngine(t, cfg, exec)

	id1, err := e.Trigger("t", model.TriggerManual)
	if err != nil {
		t.Fatalf("Trigger: %v", err)
	}
	waitFor(t, func() bool { return e.Running("t") })

	id2, err := e.Trigger("t", model.TriggerManual)
	if err != nil {
		t.Fatalf("Trigger 2: %v", err)
	}
	if id2 != id1 {
		t.Fatalf("single-flight broken: id1=%d id2=%d", id1, id2)
	}

	close(exec.release)
	waitFor(t, func() bool { return !e.Running("t") })

	run, err := e.GetRun("t", id1)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if run.Status != model.StatusSuccess {
		t.Fatalf("status = %q, want success", run.Status)
	}
}

func TestTriggerUnknownTask(t *testing.T) {
	e, _ := newTestEngine(t, &model.Config{}, &blockingExec{release: make(chan struct{})})
	if _, err := e.Trigger("nope", model.TriggerManual); err != ErrNotFound {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestStreamLogReplayLiveDone(t *testing.T) {
	exec := &blockingExec{lines: []string{"a", "b"}, release: make(chan struct{}), status: model.StatusFailed, exit: 2}
	cfg := &model.Config{Tasks: []model.Task{{Name: "t", Type: model.TaskShell}}}
	e, _ := newTestEngine(t, cfg, exec)

	id, err := e.Trigger("t", model.TriggerManual)
	if err != nil {
		t.Fatalf("Trigger: %v", err)
	}
	// Wait until the early lines have been written so replay has content.
	waitFor(t, func() bool {
		tail, _ := e.GetRun("t", id)
		return tail.ID == id && e.Running("t")
	})
	waitFor(t, func() bool {
		s, _ := e.store.Tail("t", id, 10)
		return s != ""
	})

	ch, err := e.StreamLog(context.Background(), "t", id)
	if err != nil {
		t.Fatalf("StreamLog: %v", err)
	}

	// Let the run finish so we get the live tail + Done.
	close(exec.release)

	var lines []string
	var done *LogEvent
	for ev := range ch {
		if ev.Done {
			d := ev
			done = &d
			continue
		}
		lines = append(lines, ev.Line)
	}
	if done == nil {
		t.Fatal("never received Done event")
	}
	if done.Status != model.StatusFailed {
		t.Fatalf("Done status = %q, want failed", done.Status)
	}
	if len(lines) < 2 || lines[0] != "a" || lines[1] != "b" {
		t.Fatalf("replayed lines = %v, want a,b prefix", lines)
	}
}

func TestStreamLogAlreadyTerminal(t *testing.T) {
	exec := &blockingExec{lines: []string{"x"}, release: make(chan struct{}), status: model.StatusSuccess}
	cfg := &model.Config{Tasks: []model.Task{{Name: "t", Type: model.TaskShell}}}
	e, _ := newTestEngine(t, cfg, exec)

	id, _ := e.Trigger("t", model.TriggerManual)
	waitFor(t, func() bool { return e.Running("t") })
	close(exec.release)
	waitFor(t, func() bool { return !e.Running("t") })

	ch, err := e.StreamLog(context.Background(), "t", id)
	if err != nil {
		t.Fatalf("StreamLog: %v", err)
	}
	var sawLine, sawDone bool
	var status model.RunStatus
	for ev := range ch {
		if ev.Done {
			sawDone = true
			status = ev.Status
		} else if ev.Line == "x" {
			sawLine = true
		}
	}
	if !sawLine || !sawDone || status != model.StatusSuccess {
		t.Fatalf("sawLine=%v sawDone=%v status=%q", sawLine, sawDone, status)
	}
}

func TestStreamLogCtxCancel(t *testing.T) {
	exec := &blockingExec{lines: []string{"a"}, release: make(chan struct{}), status: model.StatusSuccess}
	cfg := &model.Config{Tasks: []model.Task{{Name: "t", Type: model.TaskShell}}}
	e, _ := newTestEngine(t, cfg, exec)

	id, _ := e.Trigger("t", model.TriggerManual)
	waitFor(t, func() bool { return e.Running("t") })

	ctx, cancel := context.WithCancel(context.Background())
	ch, err := e.StreamLog(ctx, "t", id)
	if err != nil {
		t.Fatalf("StreamLog: %v", err)
	}
	cancel()

	// Channel must close without blocking, even though the run is still live.
	timeout := time.After(2 * time.Second)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				// Release the run and wait for its goroutine to fully drain
				// (persisting the terminal record) before the test returns, so
				// TempDir cleanup does not race the still-writing run.
				close(exec.release)
				if err := e.Stop(context.Background()); err != nil {
					t.Fatalf("Stop: %v", err)
				}
				return
			}
		case <-timeout:
			t.Fatal("channel did not close after ctx cancel")
		}
	}
}

func TestListAndGetTask(t *testing.T) {
	exec := &blockingExec{release: make(chan struct{}), status: model.StatusSuccess}
	close(exec.release) // never blocks
	cfg := &model.Config{Tasks: []model.Task{{Name: "t", Type: model.TaskShell}}}
	e, _ := newTestEngine(t, cfg, exec)

	id, _ := e.Trigger("t", model.TriggerManual)
	waitFor(t, func() bool { return !e.Running("t") })

	views := e.ListTasks()
	if len(views) != 1 || views[0].LastRun == nil || views[0].LastRun.ID != id {
		t.Fatalf("ListTasks = %+v", views)
	}
	detail, err := e.GetTask("t")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if len(detail.Runs) == 0 || detail.Runs[0].ID != id {
		t.Fatalf("GetTask runs = %+v", detail.Runs)
	}
	if _, err := e.GetTask("nope"); err != ErrNotFound {
		t.Fatalf("GetTask unknown err = %v, want ErrNotFound", err)
	}
}

// concurrency smoke test: many triggers + streams should not race or leak.
func TestConcurrentTriggers(t *testing.T) {
	exec := &blockingExec{release: make(chan struct{}), status: model.StatusSuccess}
	close(exec.release)
	cfg := &model.Config{Tasks: []model.Task{{Name: "t", Type: model.TaskShell}}}
	e, _ := newTestEngine(t, cfg, exec)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = e.Trigger("t", model.TriggerManual)
		}()
	}
	wg.Wait()
	waitFor(t, func() bool { return !e.Running("t") })

	if err := e.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}
