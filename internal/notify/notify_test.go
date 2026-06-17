package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wanggang316/the-loop/internal/model"
)

func TestShouldAlert(t *testing.T) {
	// All RunStatus values, including non-terminal ones, to make sure policies
	// only fire on the states they promise.
	statuses := []model.RunStatus{
		model.StatusPending,
		model.StatusRunning,
		model.StatusSuccess,
		model.StatusFailed,
		model.StatusTimeout,
	}

	// want[policy][status] is the expected ShouldAlert result.
	want := map[model.AlertPolicy]map[model.RunStatus]bool{
		// Empty policy defaults to AlertOnFailure.
		"": {
			model.StatusPending: false,
			model.StatusRunning: false,
			model.StatusSuccess: false,
			model.StatusFailed:  true,
			model.StatusTimeout: true,
		},
		model.AlertOnFailure: {
			model.StatusPending: false,
			model.StatusRunning: false,
			model.StatusSuccess: false,
			model.StatusFailed:  true,
			model.StatusTimeout: true,
		},
		model.AlertOnSuccess: {
			model.StatusPending: false,
			model.StatusRunning: false,
			model.StatusSuccess: true,
			model.StatusFailed:  false,
			model.StatusTimeout: false,
		},
		model.AlertAlways: {
			// Fires on every terminal status only.
			model.StatusPending: false,
			model.StatusRunning: false,
			model.StatusSuccess: true,
			model.StatusFailed:  true,
			model.StatusTimeout: true,
		},
		model.AlertNever: {
			model.StatusPending: false,
			model.StatusRunning: false,
			model.StatusSuccess: false,
			model.StatusFailed:  false,
			model.StatusTimeout: false,
		},
	}

	for policy, perStatus := range want {
		for _, status := range statuses {
			policy, status := policy, status
			name := string(policy) + "/" + string(status)
			if policy == "" {
				name = "default/" + string(status)
			}
			t.Run(name, func(t *testing.T) {
				if got := ShouldAlert(policy, status); got != perStatus[status] {
					t.Errorf("ShouldAlert(%q, %q) = %v, want %v", policy, status, got, perStatus[status])
				}
			})
		}
	}
}

// sampleEvent is a representative terminal event used across notifier tests.
func sampleEvent() model.Event {
	start := time.Date(2026, 6, 13, 10, 0, 0, 0, time.UTC)
	return model.Event{
		Task: model.Task{
			Name:  "backup",
			Type:  model.TaskShell,
			Alert: model.AlertAlways,
		},
		Run: model.Run{
			ID:         7,
			TaskName:   "backup",
			Trigger:    model.TriggerAuto,
			Status:     model.StatusFailed,
			ExitCode:   1,
			StartedAt:  start,
			FinishedAt: start.Add(2 * time.Second),
		},
		LogTail: "boom: something failed",
	}
}

func TestTelegramNotify(t *testing.T) {
	const token = "123:ABC"
	const chatID = "987654"

	var gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", ct)
		}
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &gotBody); err != nil {
			t.Errorf("unmarshal body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	n := NewTelegramNotifier(&model.TelegramConfig{
		BotToken: token,
		ChatID:   chatID,
		APIBase:  srv.URL,
	})

	if err := n.Notify(context.Background(), sampleEvent()); err != nil {
		t.Fatalf("Notify returned error: %v", err)
	}

	if want := "/bot" + token + "/sendMessage"; gotPath != want {
		t.Errorf("request path = %q, want %q", gotPath, want)
	}
	if got, _ := gotBody["chat_id"].(string); got != chatID {
		t.Errorf("chat_id = %q, want %q", got, chatID)
	}
	text, _ := gotBody["text"].(string)
	if text == "" {
		t.Error("text is empty, want a rendered message")
	}
	if !strings.Contains(text, "backup") {
		t.Errorf("text = %q, want it to mention the task name", text)
	}
}

func TestTelegramNotifyNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad request", http.StatusBadRequest)
	}))
	defer srv.Close()

	n := NewTelegramNotifier(&model.TelegramConfig{
		BotToken: "tok",
		ChatID:   "1",
		APIBase:  srv.URL,
	})

	err := n.Notify(context.Background(), sampleEvent())
	if err == nil {
		t.Fatal("Notify = nil, want error on non-2xx status")
	}
	if !strings.Contains(err.Error(), "400") {
		t.Errorf("error %q, want it to mention status 400", err)
	}
}

func TestFeishuNotifyWebhook(t *testing.T) {
	var gotMethod string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", ct)
		}
		raw, _ := io.ReadAll(r.Body)
		if len(raw) == 0 {
			t.Error("empty request body, want a JSON payload")
		}
		if err := json.Unmarshal(raw, &gotBody); err != nil {
			t.Errorf("unmarshal body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"code":0}`))
	}))
	defer srv.Close()

	n := NewFeishuNotifier(&model.FeishuConfig{Webhook: srv.URL})

	if err := n.Notify(context.Background(), sampleEvent()); err != nil {
		t.Fatalf("Notify returned error: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("method = %s, want POST", gotMethod)
	}
	if mt, _ := gotBody["msg_type"].(string); mt != "interactive" {
		t.Errorf("msg_type = %q, want interactive", mt)
	}
	if _, ok := gotBody["card"]; !ok {
		t.Error("payload missing card element")
	}
}

func TestFeishuNotifyWebhookSigned(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := NewFeishuNotifier(&model.FeishuConfig{Webhook: srv.URL, Secret: "s3cr3t"})
	if err := n.Notify(context.Background(), sampleEvent()); err != nil {
		t.Fatalf("Notify returned error: %v", err)
	}

	if _, ok := gotBody["sign"]; !ok {
		t.Error("signed webhook payload missing sign field")
	}
	if _, ok := gotBody["timestamp"]; !ok {
		t.Error("signed webhook payload missing timestamp field")
	}
}

func TestFeishuNotifyNoTarget(t *testing.T) {
	n := NewFeishuNotifier(&model.FeishuConfig{})
	if err := n.Notify(context.Background(), sampleEvent()); err == nil {
		t.Fatal("Notify = nil, want error when neither webhook nor cli_path is set")
	}
}

// recordingNotifier counts Notify calls and can be made to fail.
type recordingNotifier struct {
	name  string
	calls int32
	fail  bool
}

func (r *recordingNotifier) Name() string { return r.name }

func (r *recordingNotifier) Notify(ctx context.Context, ev model.Event) error {
	atomic.AddInt32(&r.calls, 1)
	if r.fail {
		return errors.New("boom")
	}
	return nil
}

func (r *recordingNotifier) Count() int { return int(atomic.LoadInt32(&r.calls)) }

// newTestDispatcher builds a Dispatcher with arbitrary notifiers keyed by their
// Name(), bypassing NewDispatcher's config-driven construction.
func newTestDispatcher(notifiers ...Notifier) *Dispatcher {
	m := make(map[string]Notifier, len(notifiers))
	for _, n := range notifiers {
		m[n.Name()] = n
	}
	return &Dispatcher{notifiers: m}
}

func TestDispatchFiresOnlyWhenPolicyMatches(t *testing.T) {
	tests := []struct {
		name   string
		policy model.AlertPolicy
		status model.RunStatus
		want   int // expected number of Notify calls per notifier
	}{
		{"failure policy on failed run", model.AlertOnFailure, model.StatusFailed, 1},
		{"failure policy on success run", model.AlertOnFailure, model.StatusSuccess, 0},
		{"always policy on success run", model.AlertAlways, model.StatusSuccess, 1},
		{"never policy on failed run", model.AlertNever, model.StatusFailed, 0},
		{"any policy on non-terminal run", model.AlertAlways, model.StatusRunning, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n := &recordingNotifier{name: "rec"}
			d := newTestDispatcher(n)

			ev := sampleEvent()
			ev.Task.Alert = tt.policy
			ev.Run.Status = tt.status
			ev.Task.Notifiers = d.Names()

			d.Dispatch(context.Background(), ev)

			if got := n.Count(); got != tt.want {
				t.Errorf("notifier called %d times, want %d", got, tt.want)
			}
		})
	}
}

func TestDispatchSurvivesFailingNotifier(t *testing.T) {
	failing := &recordingNotifier{name: "failing", fail: true}
	healthy := &recordingNotifier{name: "healthy"}
	d := newTestDispatcher(failing, healthy)

	ev := sampleEvent()
	ev.Task.Alert = model.AlertAlways
	ev.Run.Status = model.StatusFailed
	ev.Task.Notifiers = d.Names()

	// Must not panic and must not propagate any error (no return value).
	d.Dispatch(context.Background(), ev)

	if failing.Count() != 1 {
		t.Errorf("failing notifier called %d times, want 1", failing.Count())
	}
	// A failing notifier must not prevent later notifiers from firing.
	if healthy.Count() != 1 {
		t.Errorf("healthy notifier called %d times, want 1 (failure must not short-circuit)", healthy.Count())
	}
}

// captureLog redirects the standard log writer for the duration of fn and
// returns everything written to it. This is how the dispatcher's
// "delivery failed" logging is observed (Dispatcher.Dispatch uses log.Printf).
func captureLog(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	prevOut := log.Writer()
	prevFlags := log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(prevOut)
		log.SetFlags(prevFlags)
	})
	fn()
	return buf.String()
}

// A distinctive secret so a substring match is unambiguous: if it shows up in
// the captured logs, the redaction failed.
const (
	secretBotToken = "111222333:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	secretHookID   = "ffffffff-1111-2222-3333-444444444444"
)

// failingDelivery describes one delivery-failure scenario shared by the
// Telegram and Feishu secret-leak tests.
type failingDelivery struct {
	name string
	// newNotifier builds the notifier under test, given the base URL it should
	// target (an httptest server URL for the 500 case, a dead URL for the
	// closed-server case).
	newNotifier func(base string) Notifier
	// secret is the value that must never appear in the captured log output.
	secret string
}

// runSecretLeakCase drives one notifier through both a 500 response and a
// closed/unreachable server, dispatching via a Dispatcher so the real logging
// path is exercised, and asserts the secret never reaches the log output.
func runSecretLeakCase(t *testing.T, fd failingDelivery) {
	t.Helper()

	ev := sampleEvent()
	ev.Task.Alert = model.AlertAlways
	ev.Run.Status = model.StatusFailed

	t.Run("server returns 500", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "internal server error", http.StatusInternalServerError)
		}))
		defer srv.Close()

		d := newTestDispatcher(fd.newNotifier(srv.URL))
		ev.Task.Notifiers = d.Names()
		out := captureLog(t, func() { d.Dispatch(context.Background(), ev) })

		if out == "" {
			t.Fatal("expected a delivery-failure log line, got none")
		}
		if strings.Contains(out, fd.secret) {
			t.Errorf("captured log leaked the secret %q:\n%s", fd.secret, out)
		}
	})

	t.Run("server closed (transport error)", func(t *testing.T) {
		// Stand up a server, capture its URL, then close it so the request
		// fails at the transport layer with a *url.Error embedding the full
		// URL (which carries the secret).
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
		base := srv.URL
		srv.Close()

		d := newTestDispatcher(fd.newNotifier(base))
		ev.Task.Notifiers = d.Names()
		out := captureLog(t, func() { d.Dispatch(context.Background(), ev) })

		if out == "" {
			t.Fatal("expected a delivery-failure log line, got none")
		}
		if strings.Contains(out, fd.secret) {
			t.Errorf("captured log leaked the secret %q:\n%s", fd.secret, out)
		}
	})
}

// TestTelegramSecretNotLogged asserts the bot token never appears in the log
// output when delivery fails, neither for a 500 response nor for a transport
// error whose *url.Error embeds the token-bearing URL.
func TestTelegramSecretNotLogged(t *testing.T) {
	runSecretLeakCase(t, failingDelivery{
		name: "telegram",
		newNotifier: func(base string) Notifier {
			return NewTelegramNotifier(&model.TelegramConfig{
				BotToken: secretBotToken,
				ChatID:   "987654",
				APIBase:  base,
			})
		},
		secret: secretBotToken,
	})
}

// TestFeishuSecretNotLogged asserts the full webhook URL (a bearer secret)
// never appears in the log output when delivery fails. The hook id alone is
// the secret, so the test asserts on both the hook id and the full URL.
func TestFeishuSecretNotLogged(t *testing.T) {
	t.Run("hook id", func(t *testing.T) {
		runSecretLeakCase(t, failingDelivery{
			name: "feishu",
			newNotifier: func(base string) Notifier {
				// Append the hook id so the webhook carries a recognisable secret.
				return NewFeishuNotifier(&model.FeishuConfig{
					Webhook: base + "/open-apis/bot/v2/hook/" + secretHookID,
				})
			},
			secret: secretHookID,
		})
	})

	t.Run("full url", func(t *testing.T) {
		// The full webhook URL is itself a bearer secret; assert it never leaks.
		ev := sampleEvent()
		ev.Task.Alert = model.AlertAlways
		ev.Run.Status = model.StatusFailed

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
		webhook := srv.URL + "/open-apis/bot/v2/hook/" + secretHookID
		srv.Close() // force a transport error carrying the full URL

		d := newTestDispatcher(NewFeishuNotifier(&model.FeishuConfig{Webhook: webhook}))
		ev.Task.Notifiers = d.Names()
		out := captureLog(t, func() { d.Dispatch(context.Background(), ev) })

		if out == "" {
			t.Fatal("expected a delivery-failure log line, got none")
		}
		if strings.Contains(out, webhook) {
			t.Errorf("captured log leaked the full webhook URL %q:\n%s", webhook, out)
		}
	})
}

func TestNotifierNames(t *testing.T) {
	d := newTestDispatcher(
		&recordingNotifier{name: "feishu"},
		&recordingNotifier{name: "telegram"},
	)
	got := d.Names()
	want := []string{"feishu", "telegram"}
	if len(got) != len(want) {
		t.Fatalf("Names() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Names()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
