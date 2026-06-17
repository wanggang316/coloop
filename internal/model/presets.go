package model

// RunnerPreset is a built-in starting point for an agent runner. Presets are
// not config entries: the UI offers them when creating a runner to pre-fill the
// name and command template, which the user then edits and saves as their own
// runner. They are the single source of truth for the suggested command lines.
type RunnerPreset struct {
	Label       string `json:"label"`       // human label shown in the picker
	Name        string `json:"name"`        // suggested runner name
	CmdTmpl     string `json:"cmd_tmpl"`    // suggested command template
	Description string `json:"description"` // one-line guidance, incl. how to make it more/less autonomous
}

// RunnerPresets are the built-in agent command templates. Each renders the same
// data as any runner ({{.Prompt}}, {{.TaskName}}, {{.WorkingDir}}, {{.Env}}).
// Both stream structured output so the live log shows progress instead of going
// silent until the run ends.
var RunnerPresets = []RunnerPreset{
	{
		Label:       "Claude Code",
		Name:        "claude-code",
		CmdTmpl:     `claude -p "{{.Prompt}}" --output-format stream-json --verbose --permission-mode acceptEdits`,
		Description: "Anthropic Claude Code in headless print mode. acceptEdits auto-applies file edits; for fully unattended tasks that run shell/git, swap in --dangerously-skip-permissions.",
	},
	{
		Label:       "Codex",
		Name:        "codex",
		CmdTmpl:     `codex exec --json --full-auto "{{.Prompt}}"`,
		Description: "OpenAI Codex CLI, non-interactive exec. --full-auto runs sandboxed with automatic approvals; tighten with --sandbox/--ask-for-approval or loosen as needed.",
	},
}
