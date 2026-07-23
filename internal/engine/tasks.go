// Package engine: tasks.go adds in-place task editing (create / update /
// delete). Each edit mutates a copy of the active config, normalises and
// validates it, persists it back to config.yaml, then swaps it in and
// reschedules — all atomically under cfgMu so a concurrent Reload, Trigger or
// ListTasks never observes a half-applied change.
package engine

import (
	"errors"
	"fmt"

	"github.com/wanggang316/coloop/internal/config"
	"github.com/wanggang316/coloop/internal/model"
)

// ErrDuplicate is returned when a create/rename targets an existing task name.
var ErrDuplicate = errors.New("task already exists")

// ErrInvalid wraps a config-validation failure caused by a task edit, so the
// API layer can map it to 400 Bad Request.
var ErrInvalid = errors.New("invalid task")

// ErrReadOnly is returned when a task edit is attempted but no config path has
// been set, so the change cannot be persisted.
var ErrReadOnly = errors.New("task editing unavailable: no config file")

// SetConfigPath records the config file path so task edits can be persisted.
// Call once at startup before serving.
func (e *Engine) SetConfigPath(path string) {
	e.cfgMu.Lock()
	e.cfgPath = path
	e.cfgMu.Unlock()
}

// CreateTask adds t, persists the config and reschedules. The name must be free.
func (e *Engine) CreateTask(t model.Task) (model.Task, error) {
	e.cfgMu.Lock()
	defer e.cfgMu.Unlock()

	if e.indexOfLocked(t.Name) >= 0 {
		return model.Task{}, ErrDuplicate
	}
	next := cloneConfig(e.cfg)
	next.Tasks = append(next.Tasks, t)
	if err := e.commitLocked(next); err != nil {
		return model.Task{}, err
	}
	return e.cfg.Tasks[len(e.cfg.Tasks)-1], nil
}

// UpdateTask replaces the task named name with t. If t.Name differs the task is
// renamed, and the new name must be free.
func (e *Engine) UpdateTask(name string, t model.Task) (model.Task, error) {
	e.cfgMu.Lock()
	defer e.cfgMu.Unlock()

	i := e.indexOfLocked(name)
	if i < 0 {
		return model.Task{}, ErrNotFound
	}
	if t.Name != name && e.indexOfLocked(t.Name) >= 0 {
		return model.Task{}, ErrDuplicate
	}
	next := cloneConfig(e.cfg)
	next.Tasks[i] = t
	if err := e.commitLocked(next); err != nil {
		return model.Task{}, err
	}
	return e.cfg.Tasks[i], nil
}

// DeleteTask removes the named task, persists and reschedules. The task's run
// history on disk is left untouched.
func (e *Engine) DeleteTask(name string) error {
	e.cfgMu.Lock()
	defer e.cfgMu.Unlock()

	i := e.indexOfLocked(name)
	if i < 0 {
		return ErrNotFound
	}
	next := cloneConfig(e.cfg)
	next.Tasks = append(next.Tasks[:i], next.Tasks[i+1:]...)
	return e.commitLocked(next)
}

// indexOfLocked returns the index of the task named name, or -1. Caller holds cfgMu.
func (e *Engine) indexOfLocked(name string) int {
	for i := range e.cfg.Tasks {
		if e.cfg.Tasks[i].Name == name {
			return i
		}
	}
	return -1
}

// commitLocked normalises and validates next, persists it to disk, then swaps it
// in and reschedules cron. On any error nothing is changed. Caller holds cfgMu.
func (e *Engine) commitLocked(next *model.Config) error {
	if e.cfgPath == "" {
		return ErrReadOnly
	}
	config.ApplyDefaults(next, e.cfgPath)
	if err := config.Validate(next); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if err := config.Save(e.cfgPath, next); err != nil {
		return err
	}
	e.cfg = next
	e.schedule(next)
	e.disp.SetChannels(next.NotifierChannels)
	return nil
}

// cloneConfig copies cfg with independent Tasks, Runners and NotifierChannels
// slices, so an in-progress edit never mutates a slice currently shared with
// readers (ListTasks/findTask, runnerCmdTmpl, ListNotifiers).
func cloneConfig(cfg *model.Config) *model.Config {
	cp := *cfg
	cp.Tasks = append([]model.Task(nil), cfg.Tasks...)
	cp.Runners = append([]model.AgentRunner(nil), cfg.Runners...)
	cp.NotifierChannels = append([]model.NotifierDef(nil), cfg.NotifierChannels...)
	return &cp
}
