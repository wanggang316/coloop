package model

// DefaultRunners are the built-in agent command templates seeded into a config
// that declares no runners, so the common agents are ready to use (and editable
// or deletable) out of the box. Each renders the same data as any runner
// ({{.Prompt}}, {{.TaskName}}, {{.WorkingDir}}, {{.Env}}) and streams structured
// output so the live log shows progress instead of going silent until the end.
var DefaultRunners = []AgentRunner{
	// coloop runs agents unattended, so there is nobody to answer permission
	// prompts: --dangerously-skip-permissions lets the agent run shell/git/network
	// steps (the common case) instead of silently stalling on approval. Swap in
	// --permission-mode acceptEdits if you only trust it to edit files.
	{Name: "claude-code", CmdTmpl: `claude -p "{{.Prompt}}" --output-format stream-json --verbose --dangerously-skip-permissions`},
	// --full-auto runs sandboxed with automatic approvals.
	{Name: "codex", CmdTmpl: `codex exec --json --full-auto "{{.Prompt}}"`},
}
