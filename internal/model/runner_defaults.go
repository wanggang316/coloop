package model

// DefaultRunners are the built-in agent command templates seeded into a config
// that declares no runners, so the common agents are ready to use (and editable
// or deletable) out of the box. Each renders the same data as any runner
// ({{.Prompt}}, {{.TaskName}}, {{.WorkingDir}}, {{.Env}}) and streams structured
// output so the live log shows progress instead of going silent until the end.
var DefaultRunners = []AgentRunner{
	// acceptEdits auto-applies file edits; for unattended tasks that run
	// shell/git, edit this to use --dangerously-skip-permissions.
	{Name: "claude-code", CmdTmpl: `claude -p "{{.Prompt}}" --output-format stream-json --verbose --permission-mode acceptEdits`},
	// --full-auto runs sandboxed with automatic approvals.
	{Name: "codex", CmdTmpl: `codex exec --json --full-auto "{{.Prompt}}"`},
}
