package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/wanggang316/the-loop/internal/model"
)

// defaultTelegramAPIBase is the public Bot API host, used when the config does
// not override it.
const defaultTelegramAPIBase = "https://api.telegram.org"

// TelegramNotifier delivers run events to a Telegram chat via the Bot API.
type TelegramNotifier struct {
	cfg     *model.TelegramConfig
	apiBase string
	client  *http.Client
}

// NewTelegramNotifier builds a notifier from the Telegram config.
func NewTelegramNotifier(cfg *model.TelegramConfig) *TelegramNotifier {
	base := cfg.APIBase
	if base == "" {
		base = defaultTelegramAPIBase
	}
	return &TelegramNotifier{
		cfg:     cfg,
		apiBase: strings.TrimRight(base, "/"),
		client:  &http.Client{Timeout: 10 * time.Second},
	}
}

// Name identifies the channel.
func (t *TelegramNotifier) Name() string { return "telegram" }

// redactSecret replaces every occurrence of secret in s with a placeholder so
// that bot tokens or webhook URLs embedded in *url.Error strings never reach
// the logs. An empty secret leaves s untouched.
func redactSecret(s, secret string) string {
	if secret == "" {
		return s
	}
	return strings.ReplaceAll(s, secret, "***")
}

// Notify delivers the event to Telegram via sendMessage.
func (t *TelegramNotifier) Notify(ctx context.Context, ev model.Event) error {
	url := fmt.Sprintf("%s/bot%s/sendMessage", t.apiBase, t.cfg.BotToken)

	payload := map[string]any{
		"chat_id":    t.cfg.ChatID,
		"text":       messageText(ev),
		"parse_mode": "Markdown",
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("telegram: marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		// err may embed the request URL (with the bot token); redact it.
		return fmt.Errorf("telegram: new request: %s", redactSecret(err.Error(), t.cfg.BotToken))
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.client.Do(req)
	if err != nil {
		// A transport *url.Error embeds the full URL, including the bot token,
		// which must never reach the logs. Redact the token before returning.
		return fmt.Errorf("telegram: post failed: %s", redactSecret(err.Error(), t.cfg.BotToken))
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("telegram: status %d: %s", resp.StatusCode, strings.TrimSpace(string(snippet)))
	}
	return nil
}
