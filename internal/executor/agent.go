package executor

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"text/template"

	"github.com/wanggang316/coloop/internal/model"
)

// AgentExecutor renders a task's CmdTmpl into a shell command and runs it.
//
// The template is evaluated with tmplData so a template like
// `claude -p "{{.Prompt}}"` works. For convenience the prompt and task name are
// also exported into the environment as PROMPT and TASK_NAME, so a template
// like `claude -p "$PROMPT"` works as well.
type AgentExecutor struct {
	shell string
}

// tmplData is the data made available to the command template.
type tmplData struct {
	Prompt     string
	TaskName   string
	WorkingDir string
	Env        map[string]string
}

// NewAgent builds an AgentExecutor. An empty shell defaults to /bin/sh.
func NewAgent(shell string) *AgentExecutor {
	if shell == "" {
		shell = defaultShell
	}
	return &AgentExecutor{shell: shell}
}

// Run renders task.CmdTmpl and executes it via `shell -c`.
func (e *AgentExecutor) Run(ctx context.Context, task model.Task, w io.Writer) (Result, error) {
	if task.CmdTmpl == "" {
		return Result{}, fmt.Errorf("agent task %q: empty cmd_tmpl", task.Name)
	}

	tmpl, err := template.New(task.Name).Parse(task.CmdTmpl)
	if err != nil {
		return Result{}, fmt.Errorf("agent task %q: parse cmd_tmpl: %w", task.Name, err)
	}

	var sb strings.Builder
	data := tmplData{
		Prompt:     task.Prompt,
		TaskName:   task.Name,
		WorkingDir: task.WorkingDir,
		Env:        task.Env,
	}
	if err := tmpl.Execute(&sb, data); err != nil {
		return Result{}, fmt.Errorf("agent task %q: render cmd_tmpl: %w", task.Name, err)
	}

	cmd := exec.CommandContext(ctx, e.shell, "-c", sb.String())
	extraEnv := []string{
		"PROMPT=" + task.Prompt,
		"TASK_NAME=" + task.Name,
	}
	return runCmd(ctx, cmd, task, w, extraEnv...)
}
