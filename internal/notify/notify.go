// Package notify delivers run events to alert channels (Feishu, Telegram).
package notify

import (
	"context"

	"github.com/wanggang316/coloop/internal/model"
)

// Notifier delivers a run event to one channel.
type Notifier interface {
	// Name identifies the channel, used for logging.
	Name() string
	// Notify delivers the event; a non-nil error means delivery failed.
	Notify(ctx context.Context, ev model.Event) error
}

// ShouldAlert reports whether a policy fires for a given terminal status.
// An empty policy is treated as the default, AlertOnFailure.
func ShouldAlert(policy model.AlertPolicy, status model.RunStatus) bool {
	if policy == "" {
		policy = model.AlertOnFailure
	}
	switch policy {
	case model.AlertAlways:
		return status.Terminal()
	case model.AlertOnSuccess:
		return status == model.StatusSuccess
	case model.AlertOnFailure:
		return status == model.StatusFailed || status == model.StatusTimeout
	default: // AlertNever
		return false
	}
}
