package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/wanggang316/coloop/internal/model"
)

// TestAddPostsTask checks that `add` turns its flags into a POST /api/tasks with
// the right Task body, including env, timeout, alert and notifiers parsing.
func TestAddPostsTask(t *testing.T) {
	var method, path string
	var body []byte
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path
		body, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write(body) // echo so the client can decode `created`
	}))
	defer ts.Close()

	code := Dispatch("add", []string{
		"--addr", ts.URL,
		"--name", "backup", "--type", "shell",
		"--cron", "0 3 * * *", "--command", "./b.sh",
		"--env", "ENV=prod", "--env", "TZ=UTC",
		"--timeout", "30m", "--alert", "always",
		"--notifiers", "tg, feishu",
	}, "")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if method != http.MethodPost || path != "/api/tasks" {
		t.Fatalf("request = %s %s, want POST /api/tasks", method, path)
	}

	var got model.Task
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	want := model.Task{
		Name: "backup", Type: model.TaskShell,
		Cron: "0 3 * * *", Command: "./b.sh",
		Env:       model.Env{"ENV": {Value: "prod"}, "TZ": {Value: "UTC"}},
		Timeout:   model.Duration(30 * 60 * 1e9),
		Alert:     model.AlertAlways,
		Notifiers: []string{"tg", "feishu"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("posted task =\n %+v\nwant\n %+v", got, want)
	}
}

// TestEditMergesOntoCurrent checks that `edit` fetches the current task, applies
// only the flags given, and PUTs the merged result (untouched fields preserved).
func TestEditMergesOntoCurrent(t *testing.T) {
	current := model.Task{
		Name: "backup", Type: model.TaskShell,
		Cron: "0 3 * * *", Command: "./b.sh", Alert: model.AlertOnFailure,
	}
	var sawGet bool
	var put model.Task
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			sawGet = true
			_ = json.NewEncoder(w).Encode(model.TaskDetail{Task: current})
		case http.MethodPut:
			if err := json.NewDecoder(r.Body).Decode(&put); err != nil {
				t.Errorf("decode PUT body: %v", err)
			}
			_ = json.NewEncoder(w).Encode(put)
		default:
			t.Errorf("unexpected method %s", r.Method)
		}
	}))
	defer ts.Close()

	code := Dispatch("edit", []string{"backup", "--addr", ts.URL, "--cron", "0 4 * * *"}, "")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if !sawGet {
		t.Error("edit should GET the current task before PUTting")
	}
	if put.Cron != "0 4 * * *" {
		t.Errorf("cron = %q, want updated", put.Cron)
	}
	if put.Command != "./b.sh" || put.Alert != model.AlertOnFailure || put.Type != model.TaskShell {
		t.Errorf("edit dropped untouched fields: %+v", put)
	}
}

// TestRmDeletes checks that `rm` issues DELETE on the task path.
func TestRmDeletes(t *testing.T) {
	var method, path string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	defer ts.Close()

	if code := Dispatch("rm", []string{"backup", "--addr", ts.URL}, ""); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if method != http.MethodDelete || path != "/api/tasks/backup" {
		t.Errorf("request = %s %s, want DELETE /api/tasks/backup", method, path)
	}
}

// TestAddValidation covers the local guards that fail before any network call.
func TestAddValidation(t *testing.T) {
	cases := map[string][]string{
		"missing type": {"--name", "x"},
		"missing name": {"--type", "shell"},
		"bad timeout":  {"--name", "x", "--type", "shell", "--timeout", "nope"},
		"bad env pair": {"--name", "x", "--type", "shell", "--env", "no-equals"},
	}
	for name, args := range cases {
		if code := Dispatch("add", args, ""); code == 0 {
			t.Errorf("%s: expected non-zero exit", name)
		}
	}
}

func TestSplitList(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", []string{}},
		{"a", []string{"a"}},
		{" a , b ,, c ", []string{"a", "b", "c"}},
	}
	for _, c := range cases {
		if got := splitList(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("splitList(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestKVFlag(t *testing.T) {
	var k kvFlag
	if err := k.Set("A=1"); err != nil {
		t.Fatal(err)
	}
	if err := k.Set("B=x=y"); err != nil { // value may contain '='
		t.Fatal(err)
	}
	if err := k.Set("noeq"); err == nil {
		t.Error("expected error for KEY=VALUE without '='")
	}
	if k.m["A"] != "1" || k.m["B"] != "x=y" {
		t.Errorf("map = %v", k.m)
	}
}

func TestTakeName(t *testing.T) {
	if n, rest := takeName([]string{"backup", "--cron", "x"}); n != "backup" || len(rest) != 2 {
		t.Errorf("takeName positional = %q, rest %v", n, rest)
	}
	if n, rest := takeName([]string{"--cron", "x"}); n != "" || len(rest) != 2 {
		t.Errorf("takeName flag-first = %q, rest %v", n, rest)
	}
	if n, _ := takeName(nil); n != "" {
		t.Errorf("takeName(nil) = %q", n)
	}
}

func TestSecretEnvFlagAndEditPreservation(t *testing.T) {
	current := model.Task{Name: "probe", Type: model.TaskShell, Command: "true", Env: model.Env{"OLD": {Secret: "OLD_KEY"}}}
	var put model.Task
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			json.NewEncoder(w).Encode(model.TaskDetail{Task: current})
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&put); err != nil {
			t.Error(err)
		}
		json.NewEncoder(w).Encode(put)
	}))
	defer ts.Close()
	if code := Dispatch("edit", []string{"probe", "--addr", ts.URL, "--env-secret", "TOKEN=API_TOKEN", "--env", "MODE=prod"}, ""); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if put.Env["OLD"].Secret != "OLD_KEY" || put.Env["TOKEN"].Secret != "API_TOKEN" || put.Env["MODE"].Value != "prod" {
		t.Fatalf("lost env values: %+v", put.Env)
	}
}
