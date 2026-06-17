package model

import (
	"strings"
	"testing"
	"text/template"
)

// TestRunnerPresetsWellFormed guards the shipped presets: each must have the
// fields the UI relies on, a unique name, and a command template that parses
// and renders (so we never ship a runner template with an unclosed action).
func TestRunnerPresetsWellFormed(t *testing.T) {
	if len(RunnerPresets) == 0 {
		t.Fatal("no runner presets defined")
	}
	seen := map[string]bool{}
	for _, p := range RunnerPresets {
		if strings.TrimSpace(p.Label) == "" || strings.TrimSpace(p.Name) == "" || strings.TrimSpace(p.CmdTmpl) == "" {
			t.Errorf("preset %+v: label, name and cmd_tmpl are all required", p)
		}
		if seen[p.Name] {
			t.Errorf("duplicate preset name %q", p.Name)
		}
		seen[p.Name] = true

		tmpl, err := template.New(p.Name).Parse(p.CmdTmpl)
		if err != nil {
			t.Errorf("preset %q: cmd_tmpl does not parse: %v", p.Name, err)
			continue
		}
		var sb strings.Builder
		data := struct {
			Prompt, TaskName, WorkingDir string
			Env                          map[string]string
		}{Prompt: "do it", TaskName: p.Name, WorkingDir: "/tmp"}
		if err := tmpl.Execute(&sb, data); err != nil {
			t.Errorf("preset %q: cmd_tmpl does not render: %v", p.Name, err)
		}
		if !strings.Contains(sb.String(), "do it") {
			t.Errorf("preset %q: rendered command %q does not include the prompt", p.Name, sb.String())
		}
	}
}
