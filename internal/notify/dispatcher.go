package notify

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/wanggang316/coloop/internal/model"
)

// dispatchTimeout bounds a single notifier delivery.
const dispatchTimeout = 10 * time.Second

// Dispatcher routes a run event to the notifier channels a task selects by name
// (Task.Notifiers). Its channel set is rebuildable, so config edits take effect
// without restarting; SetChannels may run concurrently with Dispatch.
type Dispatcher struct {
	mu        sync.RWMutex
	notifiers map[string]Notifier // channel name -> notifier
}

// NewDispatcher builds a dispatcher from the named channel definitions.
func NewDispatcher(defs []model.NotifierDef) *Dispatcher {
	d := &Dispatcher{notifiers: map[string]Notifier{}}
	d.SetChannels(defs)
	return d
}

// SetChannels replaces the channel set with one built from defs. A definition
// whose type/credentials are unusable is skipped (validation should have caught
// it earlier).
func (d *Dispatcher) SetChannels(defs []model.NotifierDef) {
	m := make(map[string]Notifier, len(defs))
	for _, def := range defs {
		if n := buildNotifier(def); n != nil {
			m[def.Name] = n
		}
	}
	d.mu.Lock()
	d.notifiers = m
	d.mu.Unlock()
}

// buildNotifier constructs the notifier for a channel definition, or nil when
// the credentials for its type are missing.
func buildNotifier(def model.NotifierDef) Notifier {
	switch def.Type {
	case model.NotifierFeishu:
		if def.Feishu != nil {
			return NewFeishuNotifier(def.Feishu)
		}
	case model.NotifierTelegram:
		if def.Telegram != nil {
			return NewTelegramNotifier(def.Telegram)
		}
	}
	return nil
}

// Names lists the configured channel names, sorted for stable output.
func (d *Dispatcher) Names() []string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	names := make([]string, 0, len(d.notifiers))
	for name := range d.notifiers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Dispatch delivers the event to each channel the task selected, when the task's
// alert policy fires for the run status. A task that selects no channels (or
// only unknown ones) notifies nobody. Delivery errors are logged but never
// returned: notification is best-effort and must not affect the caller.
func (d *Dispatcher) Dispatch(ctx context.Context, ev model.Event) {
	if !ShouldAlert(ev.Task.Alert, ev.Run.Status) {
		return
	}

	// Resolve the task's selected channels under the lock, then deliver without
	// holding it so a slow notifier can't block a concurrent SetChannels.
	d.mu.RLock()
	selected := make([]Notifier, 0, len(ev.Task.Notifiers))
	for _, name := range ev.Task.Notifiers {
		if n := d.notifiers[name]; n != nil {
			selected = append(selected, n)
		}
	}
	d.mu.RUnlock()

	for _, n := range selected {
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
