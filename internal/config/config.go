// Package config loads, validates and watches coloop's config.yaml,
// turning it into a *model.Config the rest of the daemon builds on.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
	"gopkg.in/yaml.v3"

	"github.com/wanggang316/coloop/internal/model"
)

// Load reads the YAML config at path, applies defaults and expands "~/" in
// path-like fields, then validates the result.
func Load(path string) (*model.Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %q: %w", path, err)
	}

	var cfg model.Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config %q: %w", path, err)
	}

	applyDefaults(&cfg, path)

	// Seed the built-in runners when the config declares none, so the common
	// agents show up in the Runners list ready to use (and editable/deletable).
	// Done only here, on load — not in applyDefaults — so deleting a runner via
	// the API (which re-runs ApplyDefaults) is never undone by re-seeding.
	if len(cfg.Runners) == 0 {
		cfg.Runners = append([]model.AgentRunner(nil), model.DefaultRunners...)
	}

	if err := Validate(&cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// applyDefaults fills in implicit values and normalises path-like fields.
func applyDefaults(cfg *model.Config, path string) {
	if cfg.Listen == "" {
		// Default to loopback only: the API is unauthenticated, so a
		// host-less ":8765" (all interfaces) must not be the default.
		cfg.Listen = "127.0.0.1:8765"
	}
	if cfg.Shell == "" {
		if sh := os.Getenv("SHELL"); sh != "" {
			cfg.Shell = sh
		} else {
			cfg.Shell = "/bin/sh"
		}
	}

	if cfg.DataDir == "" {
		if abs, err := filepath.Abs(path); err == nil {
			cfg.DataDir = filepath.Dir(abs)
		} else {
			cfg.DataDir = filepath.Dir(path)
		}
	}
	cfg.DataDir = expandHome(cfg.DataDir)

	for i := range cfg.Tasks {
		t := &cfg.Tasks[i]
		t.WorkingDir = expandHome(t.WorkingDir)
		if t.Alert == "" {
			t.Alert = model.AlertOnFailure
		}
		if t.Timeout == 0 {
			t.Timeout = model.Duration(time.Hour)
		}
	}

	migrateLegacyNotifiers(cfg)
	for i := range cfg.NotifierChannels {
		if f := cfg.NotifierChannels[i].Feishu; f != nil {
			f.CLIPath = expandHome(f.CLIPath)
		}
	}
}

// migrateLegacyNotifiers folds the legacy single-channel `notifiers` block into
// the named NotifierChannels registry (as "feishu"/"telegram") and clears it, so
// the rest of the daemon only deals with named channels. Migration is skipped
// for a channel whose name is already taken, and is idempotent.
func migrateLegacyNotifiers(cfg *model.Config) {
	taken := make(map[string]struct{}, len(cfg.NotifierChannels))
	for i := range cfg.NotifierChannels {
		taken[cfg.NotifierChannels[i].Name] = struct{}{}
	}
	add := func(name string, def model.NotifierDef) {
		if _, dup := taken[name]; dup {
			return
		}
		cfg.NotifierChannels = append(cfg.NotifierChannels, def)
		taken[name] = struct{}{}
	}
	if f := cfg.Notifiers.Feishu; f != nil {
		add("feishu", model.NotifierDef{Name: "feishu", Type: model.NotifierFeishu, Feishu: f})
	}
	if tg := cfg.Notifiers.Telegram; tg != nil {
		add("telegram", model.NotifierDef{Name: "telegram", Type: model.NotifierTelegram, Telegram: tg})
	}
	cfg.Notifiers = model.NotifierConfig{}
}

// expandHome rewrites a bare "~" or a leading "~/" to the current user's home
// directory. Other forms are returned unchanged.
func expandHome(p string) string {
	if p != "~" && !strings.HasPrefix(p, "~/") {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	if p == "~" {
		return home
	}
	return filepath.Join(home, p[2:])
}

// Validate checks structural invariants of a fully-defaulted config and
// returns a single aggregated error describing every problem found.
func Validate(cfg *model.Config) error {
	var errs []error

	// Validate the runner registry first and collect its names, so task
	// references below can be checked against it.
	runners := make(map[string]struct{}, len(cfg.Runners))
	for i := range cfg.Runners {
		r := &cfg.Runners[i]
		label := r.Name
		if label == "" {
			label = fmt.Sprintf("runners[%d]", i)
		}
		if strings.TrimSpace(r.Name) == "" {
			errs = append(errs, fmt.Errorf("%s: name is required", label))
		} else if _, dup := runners[r.Name]; dup {
			errs = append(errs, fmt.Errorf("runner %q: duplicate name", r.Name))
		} else {
			runners[r.Name] = struct{}{}
		}
		if strings.TrimSpace(r.CmdTmpl) == "" {
			errs = append(errs, fmt.Errorf("runner %q: cmd_tmpl is required", label))
		}
	}

	// Validate the notifier-channel registry and collect its names for task
	// reference checks below.
	channels := make(map[string]struct{}, len(cfg.NotifierChannels))
	for i := range cfg.NotifierChannels {
		c := &cfg.NotifierChannels[i]
		label := c.Name
		if label == "" {
			label = fmt.Sprintf("notifier_channels[%d]", i)
		}
		if strings.TrimSpace(c.Name) == "" {
			errs = append(errs, fmt.Errorf("%s: name is required", label))
		} else if _, dup := channels[c.Name]; dup {
			errs = append(errs, fmt.Errorf("channel %q: duplicate name", c.Name))
		} else {
			channels[c.Name] = struct{}{}
		}
		switch c.Type {
		case model.NotifierFeishu:
			if c.Feishu == nil || (strings.TrimSpace(c.Feishu.Webhook) == "" && strings.TrimSpace(c.Feishu.CLIPath) == "") {
				errs = append(errs, fmt.Errorf("channel %q: feishu requires a webhook or cli_path", label))
			}
		case model.NotifierTelegram:
			if c.Telegram == nil || strings.TrimSpace(c.Telegram.BotToken) == "" || strings.TrimSpace(c.Telegram.ChatID) == "" {
				errs = append(errs, fmt.Errorf("channel %q: telegram requires bot_token and chat_id", label))
			}
		case "":
			errs = append(errs, fmt.Errorf("channel %q: type is required (feishu or telegram)", label))
		default:
			errs = append(errs, fmt.Errorf("channel %q: invalid type %q (want feishu or telegram)", label, c.Type))
		}
	}

	seen := make(map[string]struct{}, len(cfg.Tasks))
	for i := range cfg.Tasks {
		t := &cfg.Tasks[i]

		label := t.Name
		if label == "" {
			label = fmt.Sprintf("task[%d]", i)
		}

		if strings.TrimSpace(t.Name) == "" {
			errs = append(errs, fmt.Errorf("%s: name is required", label))
		} else if !safeTaskName(t.Name) {
			// The store uses the name as a path segment under <dataDir>/runs;
			// reject anything that could escape it.
			errs = append(errs, fmt.Errorf("task %q: name must be a single path segment (no %q, %q or %q)", t.Name, "/", `\`, ".."))
		} else if _, dup := seen[t.Name]; dup {
			errs = append(errs, fmt.Errorf("task %q: duplicate name", t.Name))
		} else {
			seen[t.Name] = struct{}{}
		}

		switch t.Type {
		case model.TaskShell:
			if t.Command == "" {
				errs = append(errs, fmt.Errorf("task %q: shell task requires command", label))
			}
		case model.TaskAgent:
			if t.Runner == "" && t.CmdTmpl == "" {
				errs = append(errs, fmt.Errorf("task %q: agent task requires a runner or cmd_tmpl", label))
			}
			if t.Runner != "" {
				if _, ok := runners[t.Runner]; !ok {
					errs = append(errs, fmt.Errorf("task %q: unknown runner %q", label, t.Runner))
				}
			}
		case "":
			errs = append(errs, fmt.Errorf("task %q: type is required (shell or agent)", label))
		default:
			errs = append(errs, fmt.Errorf("task %q: invalid type %q (want shell or agent)", label, t.Type))
		}

		if t.Cron != "" {
			if _, err := cron.ParseStandard(t.Cron); err != nil {
				errs = append(errs, fmt.Errorf("task %q: invalid cron %q: %w", label, t.Cron, err))
			}
		}

		switch t.Alert {
		case "", model.AlertOnFailure, model.AlertOnSuccess, model.AlertAlways, model.AlertNever:
		default:
			errs = append(errs, fmt.Errorf("task %q: invalid alert %q (want on_failure, on_success, always or never)", label, t.Alert))
		}

		for _, ch := range t.Notifiers {
			if _, ok := channels[ch]; !ok {
				errs = append(errs, fmt.Errorf("task %q: unknown notifier channel %q", label, ch))
			}
		}
	}

	return errors.Join(errs...)
}

// safeTaskName reports whether name is a single, path-safe segment that cannot
// escape <dataDir>/runs when used as a directory component by the store.
func safeTaskName(name string) bool {
	if name == "." || name == ".." || strings.Contains(name, "..") {
		return false
	}
	if strings.ContainsAny(name, `/\`) {
		return false
	}
	return name == filepath.Base(name)
}

// ApplyDefaults fills implicit values and normalises path-like fields. It is
// exported for callers that assemble a config in memory (e.g. the engine's task
// editor) and need the same normalisation Load applies before validation.
func ApplyDefaults(cfg *model.Config, path string) { applyDefaults(cfg, path) }

// Save writes cfg back to path as YAML, atomically (temp file in the same
// directory, then rename). It serialises via Marshal, so hand-written comments
// and original field ordering are NOT preserved: once tasks are edited through
// the API the daemon owns the file's formatting. The temp file is created with
// 0600 because the config may carry notifier secrets.
func Save(path string, cfg *model.Config) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config-*.yaml.tmp")
	if err != nil {
		return fmt.Errorf("create temp config: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename below succeeds
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp config: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replace config: %w", err)
	}
	return nil
}
