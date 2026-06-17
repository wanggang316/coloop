// Package engine: registry.go adds in-place editing of the shared registries
// that tasks reference by name — agent runners (command templates) and, in a
// later step, notifier channels. Each edit mutates a copy of the active config,
// validates and persists it, then swaps it in, all under cfgMu (via
// commitLocked) so concurrent reads never observe a half-applied change.
package engine

import "github.com/wanggang316/the-loop/internal/model"

// ListRunners returns a copy of the configured agent runners.
func (e *Engine) ListRunners() []model.AgentRunner {
	e.cfgMu.RLock()
	defer e.cfgMu.RUnlock()
	return append([]model.AgentRunner(nil), e.cfg.Runners...)
}

// CreateRunner adds r, persists the config. The name must be free.
func (e *Engine) CreateRunner(r model.AgentRunner) (model.AgentRunner, error) {
	e.cfgMu.Lock()
	defer e.cfgMu.Unlock()

	if e.runnerIndexLocked(r.Name) >= 0 {
		return model.AgentRunner{}, ErrDuplicate
	}
	next := cloneConfig(e.cfg)
	next.Runners = append(next.Runners, r)
	if err := e.commitLocked(next); err != nil {
		return model.AgentRunner{}, err
	}
	return e.cfg.Runners[len(e.cfg.Runners)-1], nil
}

// UpdateRunner replaces the runner named name with r. If r.Name differs the
// runner is renamed and the new name must be free. A rename does not rewrite
// tasks that reference the old name; validation rejects the change if it would
// leave a task pointing at a missing runner.
func (e *Engine) UpdateRunner(name string, r model.AgentRunner) (model.AgentRunner, error) {
	e.cfgMu.Lock()
	defer e.cfgMu.Unlock()

	i := e.runnerIndexLocked(name)
	if i < 0 {
		return model.AgentRunner{}, ErrNotFound
	}
	if r.Name != name && e.runnerIndexLocked(r.Name) >= 0 {
		return model.AgentRunner{}, ErrDuplicate
	}
	next := cloneConfig(e.cfg)
	next.Runners[i] = r
	if err := e.commitLocked(next); err != nil {
		return model.AgentRunner{}, err
	}
	return e.cfg.Runners[i], nil
}

// DeleteRunner removes the named runner. Validation rejects the deletion while
// any task still references it.
func (e *Engine) DeleteRunner(name string) error {
	e.cfgMu.Lock()
	defer e.cfgMu.Unlock()

	i := e.runnerIndexLocked(name)
	if i < 0 {
		return ErrNotFound
	}
	next := cloneConfig(e.cfg)
	next.Runners = append(next.Runners[:i], next.Runners[i+1:]...)
	return e.commitLocked(next)
}

// runnerIndexLocked returns the index of the runner named name, or -1. Caller holds cfgMu.
func (e *Engine) runnerIndexLocked(name string) int {
	for i := range e.cfg.Runners {
		if e.cfg.Runners[i].Name == name {
			return i
		}
	}
	return -1
}
