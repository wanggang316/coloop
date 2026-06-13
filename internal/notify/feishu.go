package notify

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/wanggang316/the-loop/internal/model"
)

// FeishuNotifier delivers run events to Feishu via a custom bot webhook or a
// local CLI binary. A webhook takes precedence; the CLI path is a best-effort
// fallback when no webhook is configured.
type FeishuNotifier struct {
	cfg    *model.FeishuConfig
	client *http.Client
}

// NewFeishuNotifier builds a notifier from the Feishu config.
func NewFeishuNotifier(cfg *model.FeishuConfig) *FeishuNotifier {
	return &FeishuNotifier{
		cfg:    cfg,
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

// Name identifies the channel.
func (f *FeishuNotifier) Name() string { return "feishu" }

// Notify delivers the event to Feishu.
func (f *FeishuNotifier) Notify(ctx context.Context, ev model.Event) error {
	if f.cfg.Webhook != "" {
		return f.notifyWebhook(ctx, ev)
	}
	if f.cfg.CLIPath != "" {
		return f.notifyCLI(ctx, ev)
	}
	return fmt.Errorf("feishu: no webhook or cli_path configured")
}

func (f *FeishuNotifier) notifyWebhook(ctx context.Context, ev model.Event) error {
	payload := f.buildCard(ev)
	if f.cfg.Secret != "" {
		ts := strconv.FormatInt(time.Now().Unix(), 10)
		sign, err := feishuSign(ts, f.cfg.Secret)
		if err != nil {
			return fmt.Errorf("feishu: sign: %w", err)
		}
		payload["timestamp"] = ts
		payload["sign"] = sign
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("feishu: marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, f.cfg.Webhook, bytes.NewReader(body))
	if err != nil {
		// err may embed the webhook URL (a bearer secret); redact it.
		return fmt.Errorf("feishu: new request: %s", redactSecret(err.Error(), f.cfg.Webhook))
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := f.client.Do(req)
	if err != nil {
		// A transport *url.Error embeds the full webhook URL, which is itself a
		// bearer secret and must never reach the logs. Redact it.
		return fmt.Errorf("feishu: post failed: %s", redactSecret(err.Error(), f.cfg.Webhook))
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("feishu: webhook status %d: %s", resp.StatusCode, strings.TrimSpace(string(snippet)))
	}
	return nil
}

// buildCard builds an interactive card payload with a title, the task name,
// status, duration and a short log tail.
func (f *FeishuNotifier) buildCard(ev model.Event) map[string]any {
	title := fmt.Sprintf("%s task: %s", statusEmoji(ev.Run.Status), ev.Task.Name)
	content := messageText(ev)

	return map[string]any{
		"msg_type": "interactive",
		"card": map[string]any{
			"header": map[string]any{
				"title": map[string]any{
					"tag":     "plain_text",
					"content": title,
				},
				"template": cardTemplate(ev.Run.Status),
			},
			"elements": []any{
				map[string]any{
					"tag": "div",
					"text": map[string]any{
						"tag":     "lark_md",
						"content": content,
					},
				},
			},
		},
	}
}

func (f *FeishuNotifier) notifyCLI(ctx context.Context, ev model.Event) error {
	cmd := exec.CommandContext(ctx, f.cfg.CLIPath, messageText(ev))
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("feishu: cli %q: %w: %s", f.cfg.CLIPath, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// feishuSign computes the Feishu webhook signature:
// base64(hmac-sha256(key=timestamp+"\n"+secret, msg="")).
func feishuSign(timestamp, secret string) (string, error) {
	key := timestamp + "\n" + secret
	h := hmac.New(sha256.New, []byte(key))
	// The message is intentionally empty; the secret lives in the key.
	if _, err := h.Write([]byte("")); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(h.Sum(nil)), nil
}

// cardTemplate maps a status to a Feishu header colour template.
func cardTemplate(status model.RunStatus) string {
	switch status {
	case model.StatusSuccess:
		return "green"
	case model.StatusTimeout:
		return "orange"
	case model.StatusFailed:
		return "red"
	default:
		return "grey"
	}
}
