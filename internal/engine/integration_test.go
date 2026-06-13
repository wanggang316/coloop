package engine

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wanggang316/the-loop/internal/executor"
	"github.com/wanggang316/the-loop/internal/model"
	"github.com/wanggang316/the-loop/internal/notify"
	"github.com/wanggang316/the-loop/internal/store"
)

// pollUntil spins on cond up to a short deadline, failing the test if it never
// holds. Kept independent of the unit-test helpers in engine_test.go.
func pollUntil(t *testing.T, d time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met within deadline")
}

// telegramServer is an httptest server standing in for the Telegram Bot API. It
// records each delivery so a test can assert a notification was sent.
type telegramServer struct {
	srv   *httptest.Server
	hits  int32
	gotCh chan string // last request body, per call
}

func newTelegramServer() *telegramServer {
	ts := &telegramServer{gotCh: make(chan string, 4)}
	ts.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&ts.hits, 1)
		body, _ := io.ReadAll(r.Body)
		select {
		case ts.gotCh <- string(body):
		default:
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"ok":true}`)
	}))
	return ts
}

func (ts *telegramServer) close()       { ts.srv.Close() }
func (ts *telegramServer) count() int32 { return atomic.LoadInt32(&ts.hits) }

// buildIntegrationEngine wires the real store, dispatcher and shell/agent
// executors together with an Engine, pointing the Telegram notifier at srv.
func buildIntegrationEngine(t *testing.T, cfg *model.Config, apiBase string) *Engine {
	t.Helper()

	st, err := store.New(t.TempDir())
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}

	cfg.Notifiers = model.NotifierConfig{
		Telegram: &model.TelegramConfig{
			BotToken: "test-token",
			ChatID:   "123",
			APIBase:  apiBase,
		},
	}
	disp := notify.NewDispatcher(cfg.Notifiers)

	shellExec := executor.NewShell(cfg.Shell)
	agentExec := executor.NewAgent(cfg.Shell)
	return New(cfg, st, disp, shellExec, agentExec)
}

// TestEngineIntegrationShellSuccessNotifies drives a real shell task end to end:
// it runs a printf that emits two lines, polls until the run is terminal, and
// asserts the run succeeded, the on-disk log holds both lines, ListTasks reports
// the last run, and the Telegram notifier received the alert.
func TestEngineIntegrationShellSuccessNotifies(t *testing.T) {
	ts := newTelegramServer()
	defer ts.close()

	const (
		line1 = "hello-loop"
		line2 = "second-line"
	)
	cfg := &model.Config{
		Tasks: []model.Task{{
			Name:    "printer",
			Type:    model.TaskShell,
			Command: "printf '%s\\n%s\\n' " + line1 + " " + line2,
			Alert:   model.AlertAlways,
			Timeout: model.Duration(5 * time.Second),
		}},
	}
	e := buildIntegrationEngine(t, cfg, ts.srv.URL)

	id, err := e.Trigger("printer", model.TriggerManual)
	if err != nil {
		t.Fatalf("Trigger: %v", err)
	}

	// Poll GetRun until the run reaches a terminal state.
	var run model.Run
	pollUntil(t, 5*time.Second, func() bool {
		run, err = e.GetRun("printer", id)
		return err == nil && run.Status.Terminal()
	})
	if run.Status != model.StatusSuccess {
		t.Fatalf("status = %q, want success", run.Status)
	}
	if run.ExitCode != 0 {
		t.Fatalf("exit code = %d, want 0", run.ExitCode)
	}

	// The on-disk log must contain both printed lines.
	rc, err := e.OpenLog("printer", id)
	if err != nil {
		t.Fatalf("OpenLog: %v", err)
	}
	logBytes, _ := io.ReadAll(rc)
	rc.Close()
	logStr := string(logBytes)
	if !strings.Contains(logStr, line1) || !strings.Contains(logStr, line2) {
		t.Fatalf("log %q missing printed lines", logStr)
	}

	// ListTasks must surface this run as the task's LastRun.
	views := e.ListTasks()
	if len(views) != 1 {
		t.Fatalf("ListTasks len = %d, want 1", len(views))
	}
	last := views[0].LastRun
	if last == nil || last.ID != id || last.Status != model.StatusSuccess {
		t.Fatalf("LastRun = %+v, want id=%d success", last, id)
	}

	// The notifier must have received exactly the success alert. Dispatch runs
	// synchronously before the single-flight slot is released, so by the time the
	// run is terminal AND no longer Running the request has been delivered; wait
	// on the recorded body to avoid any residual race.
	select {
	case body := <-ts.gotCh:
		if !strings.Contains(body, "printer") {
			t.Fatalf("notifier body %q missing task name", body)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("notifier never received a request")
	}
	if ts.count() == 0 {
		t.Fatal("notifier hit count is zero")
	}
}

// TestEngineIntegrationStreamLogReplaysAndDone confirms that after a real shell
// run completes, StreamLog replays the stored log lines and ends with a Done
// event carrying the terminal status.
func TestEngineIntegrationStreamLogReplaysAndDone(t *testing.T) {
	ts := newTelegramServer()
	defer ts.close()

	const (
		line1 = "alpha"
		line2 = "beta"
	)
	cfg := &model.Config{
		Tasks: []model.Task{{
			Name:    "streamer",
			Type:    model.TaskShell,
			Command: "printf '%s\\n%s\\n' " + line1 + " " + line2,
			Alert:   model.AlertAlways,
			Timeout: model.Duration(5 * time.Second),
		}},
	}
	e := buildIntegrationEngine(t, cfg, ts.srv.URL)

	id, err := e.Trigger("streamer", model.TriggerManual)
	if err != nil {
		t.Fatalf("Trigger: %v", err)
	}
	// Wait for the run to terminate so StreamLog takes the replay-then-Done path.
	pollUntil(t, 5*time.Second, func() bool {
		r, err := e.GetRun("streamer", id)
		return err == nil && r.Status.Terminal()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ch, err := e.StreamLog(ctx, "streamer", id)
	if err != nil {
		t.Fatalf("StreamLog: %v", err)
	}

	var lines []string
	var doneStatus model.RunStatus
	sawDone := false
	for ev := range ch {
		if ev.Done {
			sawDone = true
			doneStatus = ev.Status
			continue
		}
		lines = append(lines, ev.Line)
	}
	if !sawDone {
		t.Fatal("StreamLog never emitted a Done event")
	}
	if doneStatus != model.StatusSuccess {
		t.Fatalf("Done status = %q, want success", doneStatus)
	}
	if len(lines) < 2 || lines[0] != line1 || lines[1] != line2 {
		t.Fatalf("replayed lines = %v, want %q,%q", lines, line1, line2)
	}
}
