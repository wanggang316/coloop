// Package model holds the core domain types shared across the-loop.
// It has no external dependencies and is the frozen contract every other
// package builds against.
package model

import (
	"encoding/json"
	"fmt"
	"time"
)

// TaskType distinguishes how a task is executed.
type TaskType string

const (
	TaskShell TaskType = "shell" // run a local script / command
	TaskAgent TaskType = "agent" // invoke an agent via a command template
)

// AlertPolicy decides which run outcomes trigger a notification.
type AlertPolicy string

const (
	AlertOnFailure AlertPolicy = "on_failure" // failed or timeout (default)
	AlertOnSuccess AlertPolicy = "on_success" // success only
	AlertAlways    AlertPolicy = "always"     // every terminal run
	AlertNever     AlertPolicy = "never"      // never notify
)

// RunStatus is the lifecycle state of a single execution.
type RunStatus string

const (
	StatusPending RunStatus = "pending"
	StatusRunning RunStatus = "running"
	StatusSuccess RunStatus = "success"
	StatusFailed  RunStatus = "failed"
	StatusTimeout RunStatus = "timeout"
)

// Terminal reports whether the status is a final state.
func (s RunStatus) Terminal() bool {
	switch s {
	case StatusSuccess, StatusFailed, StatusTimeout:
		return true
	default:
		return false
	}
}

// TriggerKind records what initiated a run.
type TriggerKind string

const (
	TriggerAuto   TriggerKind = "auto"   // fired by the cron scheduler
	TriggerManual TriggerKind = "manual" // triggered by a user (UI/CLI)
)

// Duration is a time.Duration that (un)marshals from human strings like "1h30m".
type Duration time.Duration

// Std returns the underlying time.Duration.
func (d Duration) Std() time.Duration { return time.Duration(d) }

// MarshalYAML renders the duration as a human string.
func (d Duration) MarshalYAML() (any, error) { return time.Duration(d).String(), nil }

// UnmarshalYAML parses strings such as "1h", "30m", "90s".
func (d *Duration) UnmarshalYAML(unmarshal func(any) error) error {
	var s string
	if err := unmarshal(&s); err != nil {
		return err
	}
	if s == "" {
		*d = 0
		return nil
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", s, err)
	}
	*d = Duration(v)
	return nil
}

// MarshalJSON renders the duration as a human string for API responses.
func (d Duration) MarshalJSON() ([]byte, error) {
	return []byte(`"` + time.Duration(d).String() + `"`), nil
}

// UnmarshalJSON accepts either a duration string ("1h30m") or a raw number of
// nanoseconds, so values produced by MarshalJSON round-trip cleanly.
func (d *Duration) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		if s == "" {
			*d = 0
			return nil
		}
		v, err := time.ParseDuration(s)
		if err != nil {
			return fmt.Errorf("invalid duration %q: %w", s, err)
		}
		*d = Duration(v)
		return nil
	}
	var n int64
	if err := json.Unmarshal(b, &n); err != nil {
		return err
	}
	*d = Duration(n)
	return nil
}

// Task is a task definition, declared in config.yaml.
type Task struct {
	Name       string            `yaml:"name" json:"name"`
	Type       TaskType          `yaml:"type" json:"type"`
	Cron       string            `yaml:"cron" json:"cron"`               // empty => manual only
	WorkingDir string            `yaml:"working_dir" json:"working_dir"` // cwd for the command
	Command    string            `yaml:"command" json:"command"`         // shell: script / command
	Prompt     string            `yaml:"prompt" json:"prompt"`           // agent: prompt text
	CmdTmpl    string            `yaml:"cmd_tmpl" json:"cmd_tmpl"`       // agent: inline command template (legacy; prefer Runner)
	Runner     string            `yaml:"runner" json:"runner"`           // agent: name of a shared runner (see Config.Runners)
	Env        map[string]string `yaml:"env" json:"env"`                 // extra environment variables
	Timeout    Duration          `yaml:"timeout" json:"timeout"`
	Alert      AlertPolicy       `yaml:"alert" json:"alert"`
	Notifiers  []string          `yaml:"notifiers" json:"notifiers"` // names of notifier channels to alert (empty => none)
	Enabled    *bool             `yaml:"enabled" json:"enabled"`     // nil => enabled
}

// IsEnabled defaults to true when unset.
func (t Task) IsEnabled() bool { return t.Enabled == nil || *t.Enabled }

// AgentRunner is a reusable agent command template, referenced by agent tasks
// via Task.Runner so the command line is defined once and selected by name.
// The template is rendered with the same data as an inline cmd_tmpl
// ({{.Prompt}}, {{.TaskName}}, {{.WorkingDir}}, {{.Env}}).
type AgentRunner struct {
	Name    string `yaml:"name" json:"name"`
	CmdTmpl string `yaml:"cmd_tmpl" json:"cmd_tmpl"`
}

// Run is a single execution record persisted under runs/<task>/<id>/.
type Run struct {
	ID         int64       `json:"id"`
	TaskName   string      `json:"task"`
	Trigger    TriggerKind `json:"trigger"`
	Status     RunStatus   `json:"status"`
	ExitCode   int         `json:"exit_code"`
	StartedAt  time.Time   `json:"started_at"`
	FinishedAt time.Time   `json:"finished_at,omitempty"`
	LogPath    string      `json:"log_path,omitempty"`
}

// Elapsed is the wall-clock duration of a finished run; zero if unfinished.
func (r Run) Elapsed() time.Duration {
	if r.FinishedAt.IsZero() || r.StartedAt.IsZero() {
		return 0
	}
	return r.FinishedAt.Sub(r.StartedAt)
}

// Event is what a Notifier receives when a run reaches a terminal state.
type Event struct {
	Task    Task
	Run     Run
	LogTail string
}

// Config is the top-level configuration loaded from config.yaml.
type Config struct {
	Listen    string         `yaml:"listen"`    // HTTP listen address, e.g. ":8765"
	Shell     string         `yaml:"shell"`     // default shell for shell tasks
	DataDir   string         `yaml:"data_dir"`  // base dir for runs/ (default: dir of config)
	Tasks     []Task         `yaml:"tasks"`     // task definitions
	Runners   []AgentRunner  `yaml:"runners"`   // reusable agent command templates, referenced by Task.Runner

	// NotifierChannels is the named alert-channel registry tasks select from via
	// Task.Notifiers. Notifiers is the legacy single-channel block; on load it is
	// migrated into NotifierChannels (named "feishu"/"telegram") and cleared.
	NotifierChannels []NotifierDef  `yaml:"notifier_channels"`
	Notifiers        NotifierConfig `yaml:"notifiers,omitempty"`

	// Run-record retention (applied per task after each run finishes). Both
	// default to off so existing installs never silently lose history.
	MaxRuns   int      `yaml:"max_runs"`    // keep only the newest N runs per task (0 = unlimited)
	MaxRunAge Duration `yaml:"max_run_age"` // delete runs older than this, e.g. "720h" (0 = unlimited)
}

// NotifierType identifies a channel's delivery backend.
type NotifierType string

const (
	NotifierFeishu   NotifierType = "feishu"
	NotifierTelegram NotifierType = "telegram"
)

// NotifierDef is a named alert channel, selected by tasks via Task.Notifiers.
// Exactly one of Feishu/Telegram is populated, matching Type.
type NotifierDef struct {
	Name     string          `yaml:"name" json:"name"`
	Type     NotifierType    `yaml:"type" json:"type"`
	Feishu   *FeishuConfig   `yaml:"feishu,omitempty" json:"feishu,omitempty"`
	Telegram *TelegramConfig `yaml:"telegram,omitempty" json:"telegram,omitempty"`
}

// NotifierConfig groups optional alert channel configuration (legacy; migrated
// into Config.NotifierChannels on load).
type NotifierConfig struct {
	Feishu   *FeishuConfig   `yaml:"feishu,omitempty"`
	Telegram *TelegramConfig `yaml:"telegram,omitempty"`
}

// FeishuConfig configures the Feishu notifier (webhook or CLI).
type FeishuConfig struct {
	Webhook string `yaml:"webhook" json:"webhook"`   // custom bot webhook URL
	CLIPath string `yaml:"cli_path" json:"cli_path"` // path to a feishu CLI binary (alternative)
	Secret  string `yaml:"secret" json:"secret"`     // webhook signing secret (optional)
}

// TelegramConfig configures the Telegram bot notifier.
type TelegramConfig struct {
	BotToken string `yaml:"bot_token" json:"bot_token"`
	ChatID   string `yaml:"chat_id" json:"chat_id"`
	APIBase  string `yaml:"api_base" json:"api_base"` // override for testing; default https://api.telegram.org
}
