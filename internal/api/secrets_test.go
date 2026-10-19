package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/wanggang316/coloop/internal/config"
	"github.com/wanggang316/coloop/internal/engine"
	"github.com/wanggang316/coloop/internal/executor"
	"github.com/wanggang316/coloop/internal/model"
	"github.com/wanggang316/coloop/internal/notify"
	"github.com/wanggang316/coloop/internal/store"
)

func TestSecretTaskAPIRunAndPersistence(t *testing.T) {
	dir := t.TempDir()
	secretFile := filepath.Join(dir, "secrets.env")
	const token = "api-private-value"
	if err := os.WriteFile(secretFile, []byte("API_TOKEN="+token+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := &model.Config{Secrets: model.SecretSource{File: secretFile}}
	st, err := store.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	eng := engine.New(cfg, st, notify.NewDispatcher(nil), executor.NewShell(""), executor.NewAgent(""))
	configPath := filepath.Join(dir, "config.yaml")
	eng.SetConfigPath(configPath)
	srv := httptest.NewServer(New(eng, fstest.MapFS{}, nil))
	defer srv.Close()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		eng.Stop(ctx)
	}()
	call := func(method, path, body string) string {
		t.Helper()
		req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode >= 300 {
			t.Fatalf("%s %s: %d %s", method, path, resp.StatusCode, data)
		}
		if bytes.Contains(data, []byte(token)) {
			t.Fatal("API returned resolved credential")
		}
		return string(data)
	}
	body := `{"name":"probe","type":"shell","command":"test -n \"$TOKEN\" && printf %s \"$TOKEN\"","env":{"TOKEN":{"secret":"API_TOKEN"},"MODE":"prod"}}`
	created := call("POST", "/api/tasks", body)
	if !strings.Contains(created, `"secret":"API_TOKEN"`) {
		t.Fatal("create lost reference")
	}
	call("PUT", "/api/tasks/probe", body)
	raw := call("POST", "/api/tasks/probe/run", `{}`)
	var triggered model.TriggerResponse
	if err := json.Unmarshal([]byte(raw), &triggered); err != nil {
		t.Fatal(err)
	}
	waitForSecretRun(t, func() bool { r, err := eng.GetRun("probe", triggered.RunID); return err == nil && r.Status.Terminal() })
	r, err := eng.GetRun("probe", triggered.RunID)
	if err != nil || r.Status != model.StatusSuccess {
		t.Fatalf("run: %+v %v", r, err)
	}
	detail := call("GET", "/api/tasks/probe", "")
	if !strings.Contains(detail, `"secret":"API_TOKEN"`) {
		t.Fatal("run changed task reference")
	}
	logReader, err := eng.OpenLog("probe", triggered.RunID)
	if err != nil {
		t.Fatal(err)
	}
	defer logReader.Close()
	log, err := io.ReadAll(logReader)
	if err != nil {
		t.Fatal(err)
	}
	if string(log) != "[REDACTED]" {
		t.Fatalf("log: %q", log)
	}
	disk, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(disk, []byte(token)) {
		t.Fatal("credential was persisted")
	}
	reloaded, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Secrets.File != secretFile || reloaded.Tasks[0].Env["TOKEN"].Secret != "API_TOKEN" {
		t.Fatal("saved config lost secret source or reference")
	}
}

func waitForSecretRun(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met within deadline")
}
