package engine

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/wanggang316/the-loop/internal/model"
)

// newEditableEngine returns an engine wired to a writable config path so the
// registry/task edit methods can persist.
func newEditableEngine(t *testing.T, cfg *model.Config) *Engine {
	t.Helper()
	e, _ := newTestEngine(t, cfg, &blockingExec{release: make(chan struct{})})
	e.SetConfigPath(filepath.Join(t.TempDir(), "config.yaml"))
	return e
}

func TestRunnerCRUD(t *testing.T) {
	e := newEditableEngine(t, &model.Config{})

	created, err := e.CreateRunner(model.AgentRunner{Name: "claude", CmdTmpl: `claude -p "{{.Prompt}}"`})
	if err != nil {
		t.Fatalf("CreateRunner: %v", err)
	}
	if created.Name != "claude" {
		t.Fatalf("created name = %q, want claude", created.Name)
	}

	if list := e.ListRunners(); len(list) != 1 || list[0].Name != "claude" {
		t.Fatalf("ListRunners = %+v, want one named claude", list)
	}

	// Duplicate name is rejected.
	if _, err := e.CreateRunner(model.AgentRunner{Name: "claude", CmdTmpl: "x {{.Prompt}}"}); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("duplicate CreateRunner err = %v, want ErrDuplicate", err)
	}

	// Update rewrites the template.
	if _, err := e.UpdateRunner("claude", model.AgentRunner{Name: "claude", CmdTmpl: "new {{.Prompt}}"}); err != nil {
		t.Fatalf("UpdateRunner: %v", err)
	}
	if got := e.runnerCmdTmpl("claude"); got != "new {{.Prompt}}" {
		t.Fatalf("runnerCmdTmpl = %q, want resolved new template", got)
	}

	if err := e.DeleteRunner("claude"); err != nil {
		t.Fatalf("DeleteRunner: %v", err)
	}
	if list := e.ListRunners(); len(list) != 0 {
		t.Fatalf("ListRunners after delete = %+v, want empty", list)
	}
}

func TestDeleteRunnerInUseRejected(t *testing.T) {
	e := newEditableEngine(t, &model.Config{
		Runners: []model.AgentRunner{{Name: "claude", CmdTmpl: "claude {{.Prompt}}"}},
		Tasks:   []model.Task{{Name: "job", Type: model.TaskAgent, Runner: "claude"}},
	})

	// Deleting a runner still referenced by a task must fail validation, leaving
	// the registry unchanged.
	if err := e.DeleteRunner("claude"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("DeleteRunner in use err = %v, want ErrInvalid", err)
	}
	if list := e.ListRunners(); len(list) != 1 {
		t.Fatalf("runner was removed despite rejection: %+v", list)
	}
}

func TestRunnerEditRequiresConfigPath(t *testing.T) {
	// No SetConfigPath -> edits cannot be persisted.
	e, _ := newTestEngine(t, &model.Config{}, &blockingExec{release: make(chan struct{})})
	if _, err := e.CreateRunner(model.AgentRunner{Name: "x", CmdTmpl: "x {{.Prompt}}"}); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("CreateRunner without config path err = %v, want ErrReadOnly", err)
	}
}

func tgChannel(name string) model.NotifierDef {
	return model.NotifierDef{
		Name:     name,
		Type:     model.NotifierTelegram,
		Telegram: &model.TelegramConfig{BotToken: "tok", ChatID: "42"},
	}
}

func TestNotifierCRUD(t *testing.T) {
	e := newEditableEngine(t, &model.Config{})

	if _, err := e.CreateNotifier(tgChannel("tg")); err != nil {
		t.Fatalf("CreateNotifier: %v", err)
	}
	if list := e.ListNotifiers(); len(list) != 1 || list[0].Name != "tg" {
		t.Fatalf("ListNotifiers = %+v, want one named tg", list)
	}

	// Duplicate name is rejected.
	if _, err := e.CreateNotifier(tgChannel("tg")); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("duplicate CreateNotifier err = %v, want ErrDuplicate", err)
	}

	// Invalid channel (telegram missing chat_id) is rejected by validation.
	bad := model.NotifierDef{Name: "broken", Type: model.NotifierTelegram, Telegram: &model.TelegramConfig{BotToken: "x"}}
	if _, err := e.CreateNotifier(bad); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid CreateNotifier err = %v, want ErrInvalid", err)
	}

	if err := e.DeleteNotifier("tg"); err != nil {
		t.Fatalf("DeleteNotifier: %v", err)
	}
	if list := e.ListNotifiers(); len(list) != 0 {
		t.Fatalf("ListNotifiers after delete = %+v, want empty", list)
	}
}

func TestDeleteNotifierInUseRejected(t *testing.T) {
	e := newEditableEngine(t, &model.Config{
		NotifierChannels: []model.NotifierDef{tgChannel("tg")},
		Tasks:            []model.Task{{Name: "job", Type: model.TaskShell, Command: "echo hi", Notifiers: []string{"tg"}}},
	})

	if err := e.DeleteNotifier("tg"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("DeleteNotifier in use err = %v, want ErrInvalid", err)
	}
	if list := e.ListNotifiers(); len(list) != 1 {
		t.Fatalf("channel was removed despite rejection: %+v", list)
	}
}
