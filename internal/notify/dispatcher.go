package notify

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/wanggang316/the-loop/internal/model"
)

// dispatchTimeout bounds a single notifier delivery.
const dispatchTimeout = 10 * time.Second

// Dispatcher fans a run event out to every enabled notifier.
type Dispatcher struct {
	notifiers []Notifier
}

// NewDispatcher builds the dispatcher from the notifier configuration,
// enabling a channel only when its config block is present.
func NewDispatcher(cfg model.NotifierConfig) *Dispatcher {
	var notifiers []Notifier
	if cfg.Feishu != nil {
		notifiers = append(notifiers, NewFeishuNotifier(cfg.Feishu))
	}
	if cfg.Telegram != nil {
		notifiers = append(notifiers, NewTelegramNotifier(cfg.Telegram))
	}
	return &Dispatcher{notifiers: notifiers}
}

// Names lists the enabled notifier channels.
func (d *Dispatcher) Names() []string {
	names := make([]string, len(d.notifiers))
	for i, n := range d.notifiers {
		names[i] = n.Name()
	}
	return names
}

// Dispatch delivers the event to each notifier whose alert policy fires for the
// run status. Delivery errors are logged but never returned: notification is
// best-effort and must not affect the caller.
func (d *Dispatcher) Dispatch(ctx context.Context, ev model.Event) {
	for _, n := range d.notifiers {
		if !ShouldAlert(ev.Task.Alert, ev.Run.Status) {
			continue
		}
		callCtx, cancel := context.WithTimeout(ctx, dispatchTimeout)
		if err := n.Notify(callCtx, ev); err != nil {
			log.Printf("notify: %s delivery failed for task %q: %v", n.Name(), ev.Task.Name, err)
		}
		cancel()
	}
}

// statusEmoji returns a small visual cue for a run status.
func statusEmoji(status model.RunStatus) string {
	switch status {
	case model.StatusSuccess:
		return "✅" // check mark
	case model.StatusTimeout:
		return "⏱" // stopwatch
	case model.StatusFailed:
		return "❌" // cross mark
	default:
		return "ℹ" // information
	}
}

// messageText renders a human-readable summary of a run event, shared by all
// channels. It includes the task name, status, exit code, elapsed time and a
// short log tail.
func messageText(ev model.Event) string {
	var b strings.Builder

	fmt.Fprintf(&b, "%s *%s* — %s\n", statusEmoji(ev.Run.Status), ev.Task.Name, ev.Run.Status)
	fmt.Fprintf(&b, "exit: %d\n", ev.Run.ExitCode)
	fmt.Fprintf(&b, "elapsed: %s\n", ev.Run.Elapsed().Round(time.Millisecond))
	if ev.Run.Trigger != "" {
		fmt.Fprintf(&b, "trigger: %s\n", ev.Run.Trigger)
	}

	if tail := strings.TrimSpace(ev.LogTail); tail != "" {
		fmt.Fprintf(&b, "\nlog:\n```\n%s\n```", tail)
	}

	return b.String()
}
