package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wanggang316/the-loop/internal/model"
)

// writeConfig writes content to a config.yaml inside a fresh temp dir and
// returns the file path.
func writeConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func TestLoadDefaults(t *testing.T) {
	// A minimal valid config: defaults must fill in everything else.
	path := writeConfig(t, `
tasks:
  - name: hello
    type: shell
    command: echo hi
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// The API is unauthenticated, so the default bind is loopback-only; a
	// host-less ":8765" would bind all interfaces and expose it to the LAN.
	if cfg.Listen != "127.0.0.1:8765" {
		t.Errorf("Listen = %q, want \"127.0.0.1:8765\"", cfg.Listen)
	}

	// Shell defaults to $SHELL when set, else /bin/sh.
	wantShell := os.Getenv("SHELL")
	if wantShell == "" {
		wantShell = "/bin/sh"
	}
	if cfg.Shell != wantShell {
		t.Errorf("Shell = %q, want %q", cfg.Shell, wantShell)
	}

	// DataDir defaults to the absolute directory containing the config file.
	wantDir := filepath.Dir(path)
	if cfg.DataDir != wantDir {
		t.Errorf("DataDir = %q, want %q", cfg.DataDir, wantDir)
	}
	if !filepath.IsAbs(cfg.DataDir) {
		t.Errorf("DataDir = %q, want absolute path", cfg.DataDir)
	}

	if len(cfg.Tasks) != 1 {
		t.Fatalf("len(Tasks) = %d, want 1", len(cfg.Tasks))
	}
	tk := cfg.Tasks[0]
	if tk.Alert != model.AlertOnFailure {
		t.Errorf("task Alert = %q, want %q", tk.Alert, model.AlertOnFailure)
	}
	if tk.Timeout.Std() != time.Hour {
		t.Errorf("task Timeout = %v, want 1h", tk.Timeout.Std())
	}
}

func TestLoadShellDefaultFallsBackToBinSh(t *testing.T) {
	// With SHELL unset, shell must fall back to /bin/sh.
	t.Setenv("SHELL", "")

	path := writeConfig(t, `
tasks:
  - name: hello
    type: shell
    command: echo hi
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Shell != "/bin/sh" {
		t.Errorf("Shell = %q, want \"/bin/sh\"", cfg.Shell)
	}
}

func TestLoadShellUsesSHELLEnv(t *testing.T) {
	t.Setenv("SHELL", "/usr/bin/zsh")

	path := writeConfig(t, `
tasks:
  - name: hello
    type: shell
    command: echo hi
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Shell != "/usr/bin/zsh" {
		t.Errorf("Shell = %q, want \"/usr/bin/zsh\"", cfg.Shell)
	}
}

func TestLoadExplicitValuesNotOverridden(t *testing.T) {
	// Explicitly-set fields must survive defaulting untouched.
	path := writeConfig(t, `
listen: "127.0.0.1:9999"
shell: /bin/bash
data_dir: /var/lib/the-loop
tasks:
  - name: hello
    type: shell
    command: echo hi
    alert: always
    timeout: 30m
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Listen != "127.0.0.1:9999" {
		t.Errorf("Listen = %q, want \"127.0.0.1:9999\"", cfg.Listen)
	}
	if cfg.Shell != "/bin/bash" {
		t.Errorf("Shell = %q, want \"/bin/bash\"", cfg.Shell)
	}
	if cfg.DataDir != "/var/lib/the-loop" {
		t.Errorf("DataDir = %q, want \"/var/lib/the-loop\"", cfg.DataDir)
	}
	if cfg.Tasks[0].Alert != model.AlertAlways {
		t.Errorf("Alert = %q, want %q", cfg.Tasks[0].Alert, model.AlertAlways)
	}
	if cfg.Tasks[0].Timeout.Std() != 30*time.Minute {
		t.Errorf("Timeout = %v, want 30m", cfg.Tasks[0].Timeout.Std())
	}
}

func TestLoadHomeExpansion(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home dir: %v", err)
	}

	// "~/" prefixes in data_dir, working_dir and feishu cli_path expand to $HOME.
	path := writeConfig(t, `
data_dir: ~/loop-data
tasks:
  - name: hello
    type: shell
    command: echo hi
    working_dir: ~/work
notifiers:
  feishu:
    cli_path: ~/bin/feishu
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	wantData := filepath.Join(home, "loop-data")
	if cfg.DataDir != wantData {
		t.Errorf("DataDir = %q, want %q", cfg.DataDir, wantData)
	}
	wantWork := filepath.Join(home, "work")
	if cfg.Tasks[0].WorkingDir != wantWork {
		t.Errorf("WorkingDir = %q, want %q", cfg.Tasks[0].WorkingDir, wantWork)
	}
	// The legacy notifiers block is migrated into NotifierChannels as "feishu",
	// and its cli_path is home-expanded there.
	if cfg.Notifiers.Feishu != nil {
		t.Errorf("legacy Notifiers.Feishu should be cleared after migration, got %+v", cfg.Notifiers.Feishu)
	}
	var feishu *model.FeishuConfig
	for i := range cfg.NotifierChannels {
		if cfg.NotifierChannels[i].Name == "feishu" {
			feishu = cfg.NotifierChannels[i].Feishu
		}
	}
	if feishu == nil {
		t.Fatal("migrated feishu channel is missing")
	}
	wantCLI := filepath.Join(home, "bin", "feishu")
	if feishu.CLIPath != wantCLI {
		t.Errorf("feishu.CLIPath = %q, want %q", feishu.CLIPath, wantCLI)
	}
}

func TestLoadBareTildeExpanded(t *testing.T) {
	// A bare "~" expands to the user's home directory, consistent with
	// main.go's expandHome; leaving it literal would create a "~" directory.
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("UserHomeDir: %v", err)
	}
	path := writeConfig(t, `
data_dir: "~"
tasks:
  - name: hello
    type: shell
    command: echo hi
    working_dir: "~"
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.DataDir != home {
		t.Errorf("DataDir = %q, want %q", cfg.DataDir, home)
	}
	if cfg.Tasks[0].WorkingDir != home {
		t.Errorf("WorkingDir = %q, want %q", cfg.Tasks[0].WorkingDir, home)
	}
}

func TestLoadAgentTask(t *testing.T) {
	// Agent tasks validate on cmd_tmpl, not command.
	path := writeConfig(t, `
tasks:
  - name: agent-job
    type: agent
    cmd_tmpl: "claude -p {{.Prompt}}"
    prompt: do the thing
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Tasks[0].Type != model.TaskAgent {
		t.Errorf("Type = %q, want %q", cfg.Tasks[0].Type, model.TaskAgent)
	}
}

func TestLoadSeedsDefaultRunnersWhenEmpty(t *testing.T) {
	// A config with no runners gets the built-in defaults seeded.
	path := writeConfig(t, `
tasks:
  - name: hello
    type: shell
    command: echo hi
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Runners) != len(model.DefaultRunners) {
		t.Fatalf("seeded runners = %d, want %d", len(cfg.Runners), len(model.DefaultRunners))
	}
	if cfg.Runners[0].Name != model.DefaultRunners[0].Name {
		t.Errorf("first seeded runner = %q, want %q", cfg.Runners[0].Name, model.DefaultRunners[0].Name)
	}
}

func TestLoadDoesNotSeedWhenRunnersDeclared(t *testing.T) {
	// An explicit runners list is left untouched — defaults are not appended.
	path := writeConfig(t, `
runners:
  - name: mine
    cmd_tmpl: 'claude -p "{{.Prompt}}"'
tasks: []
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Runners) != 1 || cfg.Runners[0].Name != "mine" {
		t.Fatalf("runners = %+v, want only the declared 'mine'", cfg.Runners)
	}
}

func TestLoadFileNotFound(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	if err == nil {
		t.Fatal("Load: expected error for missing file, got nil")
	}
}

func TestLoadInvalidYAML(t *testing.T) {
	path := writeConfig(t, "tasks: [this is : not : valid")
	_, err := Load(path)
	if err == nil {
		t.Fatal("Load: expected parse error, got nil")
	}
}

func TestLoadValidCronAccepted(t *testing.T) {
	path := writeConfig(t, `
tasks:
  - name: scheduled
    type: shell
    command: echo hi
    cron: "0 * * * *"
`)
	if _, err := Load(path); err != nil {
		t.Fatalf("Load: %v", err)
	}
}

func TestValidate(t *testing.T) {
	enabled := false

	tests := []struct {
		name    string
		cfg     *model.Config
		wantErr bool
		// substr, when non-empty, must appear in the aggregated error message.
		substr string
	}{
		{
			name: "valid shell task",
			cfg: &model.Config{Tasks: []model.Task{
				{Name: "a", Type: model.TaskShell, Command: "echo hi"},
			}},
			wantErr: false,
		},
		{
			name: "valid agent task",
			cfg: &model.Config{Tasks: []model.Task{
				{Name: "a", Type: model.TaskAgent, CmdTmpl: "run {{.Prompt}}"},
			}},
			wantErr: false,
		},
		{
			name:    "empty tasks is valid",
			cfg:     &model.Config{},
			wantErr: false,
		},
		{
			name: "duplicate names",
			cfg: &model.Config{Tasks: []model.Task{
				{Name: "dup", Type: model.TaskShell, Command: "echo a"},
				{Name: "dup", Type: model.TaskShell, Command: "echo b"},
			}},
			wantErr: true,
			substr:  "duplicate name",
		},
		{
			name: "missing name",
			cfg: &model.Config{Tasks: []model.Task{
				{Type: model.TaskShell, Command: "echo hi"},
			}},
			wantErr: true,
			substr:  "name is required",
		},
		{
			name: "missing type",
			cfg: &model.Config{Tasks: []model.Task{
				{Name: "a", Command: "echo hi"},
			}},
			wantErr: true,
			substr:  "type is required",
		},
		{
			name: "invalid type",
			cfg: &model.Config{Tasks: []model.Task{
				{Name: "a", Type: model.TaskType("python"), Command: "echo hi"},
			}},
			wantErr: true,
			substr:  "invalid type",
		},
		{
			name: "shell without command",
			cfg: &model.Config{Tasks: []model.Task{
				{Name: "a", Type: model.TaskShell},
			}},
			wantErr: true,
			substr:  "shell task requires command",
		},
		{
			name: "agent without runner or cmd_tmpl",
			cfg: &model.Config{Tasks: []model.Task{
				{Name: "a", Type: model.TaskAgent},
			}},
			wantErr: true,
			substr:  "agent task requires a runner or cmd_tmpl",
		},
		{
			name: "agent with valid runner reference",
			cfg: &model.Config{
				Runners: []model.AgentRunner{{Name: "claude", CmdTmpl: "claude -p {{.Prompt}}"}},
				Tasks:   []model.Task{{Name: "a", Type: model.TaskAgent, Runner: "claude"}},
			},
			wantErr: false,
		},
		{
			name: "agent referencing unknown runner",
			cfg: &model.Config{Tasks: []model.Task{
				{Name: "a", Type: model.TaskAgent, Runner: "ghost"},
			}},
			wantErr: true,
			substr:  `unknown runner "ghost"`,
		},
		{
			name: "runner without cmd_tmpl",
			cfg: &model.Config{
				Runners: []model.AgentRunner{{Name: "blank"}},
			},
			wantErr: true,
			substr:  "cmd_tmpl is required",
		},
		{
			name: "duplicate runner names",
			cfg: &model.Config{Runners: []model.AgentRunner{
				{Name: "dup", CmdTmpl: "a {{.Prompt}}"},
				{Name: "dup", CmdTmpl: "b {{.Prompt}}"},
			}},
			wantErr: true,
			substr:  "duplicate name",
		},
		{
			name: "bad cron",
			cfg: &model.Config{Tasks: []model.Task{
				{Name: "a", Type: model.TaskShell, Command: "echo hi", Cron: "not a cron"},
			}},
			wantErr: true,
			substr:  "invalid cron",
		},
		{
			name: "valid cron accepted",
			cfg: &model.Config{Tasks: []model.Task{
				{Name: "a", Type: model.TaskShell, Command: "echo hi", Cron: "*/5 * * * *"},
			}},
			wantErr: false,
		},
		{
			name: "disabled task still validated",
			cfg: &model.Config{Tasks: []model.Task{
				{Name: "a", Type: model.TaskShell, Enabled: &enabled},
			}},
			wantErr: true,
			substr:  "shell task requires command",
		},
		{
			// A name with a path separator could write run records outside
			// <dataDir>/runs via the store's filepath.Join.
			name: "name with slash rejected",
			cfg: &model.Config{Tasks: []model.Task{
				{Name: "a/b", Type: model.TaskShell, Command: "echo hi"},
			}},
			wantErr: true,
			substr:  "single path segment",
		},
		{
			// A "../" name collapses past <dataDir>/runs (data-dir escape).
			name: "name with dotdot rejected",
			cfg: &model.Config{Tasks: []model.Task{
				{Name: "../evil", Type: model.TaskShell, Command: "echo hi"},
			}},
			wantErr: true,
			substr:  "single path segment",
		},
		{
			// A bare ".." is the canonical traversal segment.
			name: "name equal to dotdot rejected",
			cfg: &model.Config{Tasks: []model.Task{
				{Name: "..", Type: model.TaskShell, Command: "echo hi"},
			}},
			wantErr: true,
			substr:  "single path segment",
		},
		{
			// A backslash path separator must also be rejected.
			name: "name with backslash rejected",
			cfg: &model.Config{Tasks: []model.Task{
				{Name: `a\b`, Type: model.TaskShell, Command: "echo hi"},
			}},
			wantErr: true,
			substr:  "single path segment",
		},
		{
			// A typo'd alert policy must not silently pass: notifications are
			// how the user learns a run failed.
			name: "invalid alert policy rejected",
			cfg: &model.Config{Tasks: []model.Task{
				{Name: "a", Type: model.TaskShell, Command: "echo hi", Alert: model.AlertPolicy("on_faiulre")},
			}},
			wantErr: true,
			substr:  "invalid alert",
		},
		{
			name: "alert on_success accepted",
			cfg: &model.Config{Tasks: []model.Task{
				{Name: "a", Type: model.TaskShell, Command: "echo hi", Alert: model.AlertOnSuccess},
			}},
			wantErr: false,
		},
		{
			name: "alert never accepted",
			cfg: &model.Config{Tasks: []model.Task{
				{Name: "a", Type: model.TaskShell, Command: "echo hi", Alert: model.AlertNever},
			}},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Validate(tt.cfg)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Validate() = nil, want error")
				}
				if tt.substr != "" && !strings.Contains(err.Error(), tt.substr) {
					t.Errorf("Validate() error = %q, want substring %q", err.Error(), tt.substr)
				}
			} else if err != nil {
				t.Errorf("Validate() = %v, want nil", err)
			}
		})
	}
}

func TestValidateAggregatesMultipleErrors(t *testing.T) {
	// errors.Join must surface every distinct problem in one error.
	cfg := &model.Config{Tasks: []model.Task{
		{Name: "", Type: model.TaskType("bogus")},               // missing name + invalid type
		{Name: "x", Type: model.TaskShell, Cron: "bad cron yo"}, // missing command + bad cron
	}}

	err := Validate(cfg)
	if err == nil {
		t.Fatal("Validate: expected error, got nil")
	}
	msg := err.Error()
	for _, want := range []string{"name is required", "invalid type", "shell task requires command", "invalid cron"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q missing substring %q", msg, want)
		}
	}
}

func TestLoadRunsValidate(t *testing.T) {
	// Load must reject a structurally-invalid config (duplicate names here).
	path := writeConfig(t, `
tasks:
  - name: dup
    type: shell
    command: echo a
  - name: dup
    type: shell
    command: echo b
`)
	if _, err := Load(path); err == nil {
		t.Fatal("Load: expected validation error for duplicate names, got nil")
	}
}
