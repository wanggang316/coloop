package model

import (
	"strings"
	"testing"
	"text/template"
)

// TestDefaultRunnersWellFormed guards the seeded defaults: unique names, and a
// command template that parses and renders (so we never seed a runner with an
// unclosed action).
func TestDefaultRunnersWellFormed(t *testing.T) {
	if len(DefaultRunners) == 0 {
		t.Fatal("no default runners defined")
	}
	seen := map[string]bool{}
	for _, r := range DefaultRunners {
		if strings.TrimSpace(r.Name) == "" || strings.TrimSpace(r.CmdTmpl) == "" {
			t.Errorf("default runner %+v: name and cmd_tmpl are required", r)
		}
		if seen[r.Name] {
			t.Errorf("duplicate default runner name %q", r.Name)
		}
		seen[r.Name] = true

		tmpl, err := template.New(r.Name).Parse(r.CmdTmpl)
		if err != nil {
			t.Errorf("default runner %q: cmd_tmpl does not parse: %v", r.Name, err)
			continue
		}
		var sb strings.Builder
		data := struct {
			Prompt, TaskName, WorkingDir string
			Env                          map[string]string
		}{Prompt: "do it", TaskName: r.Name, WorkingDir: "/tmp"}
		if err := tmpl.Execute(&sb, data); err != nil {
			t.Errorf("default runner %q: cmd_tmpl does not render: %v", r.Name, err)
		}
		if !strings.Contains(sb.String(), "do it") {
			t.Errorf("default runner %q: rendered command %q omits the prompt", r.Name, sb.String())
		}
	}
}
