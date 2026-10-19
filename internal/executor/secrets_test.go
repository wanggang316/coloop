package executor

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wanggang316/coloop/internal/model"
)

func TestSecretEnvironmentAtEachRun(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secrets.env")
	t.Setenv("SELECTED", "stale-parent-value")
	t.Setenv("UNUSED_SECRET", "")
	var task model.Task
	if err := json.Unmarshal([]byte(`{"name":"probe","type":"shell","command":"test -z \"$UNUSED_SECRET\" && test \"$MODE\" = production && printf %s \"$SELECTED\"","env":{"SELECTED":{"secret":"API_TOKEN"},"MODE":"production"}}`), &task); err != nil {
		t.Fatal(err)
	}
	// A new run must read a rotated credential without a daemon reload.
	for _, token := range []string{"first-value", "rotated-value"} {
		if err := os.WriteFile(path, []byte("export API_TOKEN='"+token+"'\nUNUSED_SECRET=not-for-this-task\n"), 0600); err != nil {
			t.Fatal(err)
		}
		task.Command = `test "$SELECTED" = "` + token + `" && ` + `test -z "$UNUSED_SECRET" && test "$MODE" = production && printf %s "$SELECTED"`
		var out bytes.Buffer
		res, err := NewShell("").Run(context.Background(), Invocation{Task: task, SecretsFile: path}, &out)
		if err != nil || res.Status != model.StatusSuccess {
			t.Fatalf("run: %+v %v output=%s", res, err, out.String())
		}
		// Selected credentials must not appear in captured logs.
		if strings.Contains(out.String(), token) || !strings.Contains(out.String(), "[REDACTED]") {
			t.Fatalf("unredacted output: %q", out.String())
		}
	}
}

func TestMissingOrInvalidSecretsDoNotStartTask(t *testing.T) {
	for name, contents := range map[string]string{
		"missing": "OTHER=ok\n", "empty": "API_TOKEN=\n", "invalid": "API_TOKEN='DO_NOT_LEAK\n",
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "secrets.env")
			if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
				t.Fatal(err)
			}
			task := model.Task{Name: "probe", Command: "touch started", WorkingDir: dir, Env: model.Env{"TOKEN": {Secret: "API_TOKEN"}}}
			var out bytes.Buffer
			_, err := NewShell("").Run(context.Background(), Invocation{Task: task, SecretsFile: path}, &out)
			if err == nil {
				t.Fatal("expected error")
			}
			if _, err := os.Stat(filepath.Join(dir, "started")); !os.IsNotExist(err) {
				t.Fatal("command ran despite invalid secret")
			}
			if strings.Contains(out.String(), "DO_NOT_LEAK") || strings.Contains(err.Error(), "DO_NOT_LEAK") {
				t.Fatal("parser leaked file contents")
			}
		})
	}
}

func TestSecretFileDoesNotExecuteShell(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secrets.env")
	if err := os.WriteFile(path, []byte("API_TOKEN='$(touch injected)'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	task := model.Task{Name: "probe", Command: `test -n "$TOKEN"`, WorkingDir: dir, Env: model.Env{"TOKEN": {Secret: "API_TOKEN"}}}
	res, err := NewShell("").Run(context.Background(), Invocation{Task: task, SecretsFile: path}, &bytes.Buffer{})
	if err != nil || res.Status != model.StatusSuccess {
		t.Fatalf("run: %+v %v", res, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "injected")); !os.IsNotExist(err) {
		t.Fatal("secret was executed as shell")
	}
}

func TestAgentReceivesSecretOnlyThroughEnvironment(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secrets.env")
	if err := os.WriteFile(path, []byte("API_TOKEN=private-agent-value\n"), 0600); err != nil {
		t.Fatal(err)
	}
	task := model.Task{Name: "agent", Type: model.TaskAgent, CmdTmpl: `test -n "$TOKEN" && printf '%s' "$TOKEN"; printf '|{{.Env.TOKEN}}|{{.Env.MODE}}'`, Env: model.Env{"TOKEN": {Secret: "API_TOKEN"}, "MODE": {Value: "prod"}}}
	var out bytes.Buffer
	res, err := NewAgent("").Run(context.Background(), Invocation{Task: task, SecretsFile: path}, &out)
	if err != nil || res.Status != model.StatusSuccess {
		t.Fatalf("run: %+v %v", res, err)
	}
	if out.String() != "[REDACTED]|<no value>|prod" {
		t.Fatalf("output %q", out.String())
	}
}

func TestTaskWithoutReferencesDoesNotReadSecretsFile(t *testing.T) {
	res, err := NewShell("").Run(context.Background(), Invocation{Task: model.Task{Name: "public", Command: "true"}, SecretsFile: "/missing/secrets.env"}, &bytes.Buffer{})
	if err != nil || res.Status != model.StatusSuccess {
		t.Fatalf("run: %+v %v", res, err)
	}
}

func TestSecretLogMaskingAcrossWrites(t *testing.T) {
	// Exercise the io.Writer contract at every possible split, including overlap
	// between selected credentials and a trailing partial non-secret match.
	for split := 0; split <= len("before-token-value-after"); split++ {
		var out bytes.Buffer
		w := newSecretWriter(&out, []string{"token", "token-value"})
		text := []byte("before-token-value-after")
		if _, err := w.Write(text[:split]); err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(text[split:]); err != nil {
			t.Fatal(err)
		}
		if err := w.Flush(); err != nil {
			t.Fatal(err)
		}
		if out.String() != "before-[REDACTED]-after" {
			t.Fatalf("split %d: %q", split, out.String())
		}
	}
	var out bytes.Buffer
	w := newSecretWriter(&out, []string{"secret"})
	w.Write([]byte("not-a-sec"))
	w.Flush()
	if out.String() != "not-a-sec" {
		t.Fatalf("lost trailing output: %q", out.String())
	}
}
