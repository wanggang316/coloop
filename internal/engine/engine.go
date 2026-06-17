// Package engine is the headless core of the-loop. It ties the cron scheduler,
// task executors, run store and notifier dispatch together, and exposes a small
// concrete API that the HTTP server and CLI consume.
//
// The engine owns no transport concerns; it deals only in domain types from
// internal/model and the interfaces of the store, executor and notify packages.
package engine

import (
	"context"
	"errors"
	"io"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/wanggang316/the-loop/internal/executor"
	"github.com/wanggang316/the-loop/internal/model"
	"github.com/wanggang316/the-loop/internal/notify"
	"github.com/wanggang316/the-loop/internal/store"
)

// ErrNotFound is returned when a task or run does not exist.
var ErrNotFound = errors.New("not found")

// ErrShuttingDown is returned by Trigger once Stop has begun, so no new run
// goroutine is launched after the WaitGroup is being waited on.
var ErrShuttingDown = errors.New("engine shutting down")

// defaultTimeout bounds a run whose task declares no timeout.
const defaultTimeout = time.Hour

// detailRunCap is the maximum number of recent runs returned by GetTask.
const detailRunCap = 50

// Engine is the concrete headless core. It is safe for concurrent use.
type Engine struct {
	store     store.Store
	disp      *notify.Dispatcher
	shellExec executor.Executor
	agentExec executor.Executor

	cfgMu   sync.RWMutex
	cfg     *model.Config
	cfgPath string // config file path; enables persisting task edits (see tasks.go)

	cron    *cron.Cron
	entries map[string]cron.EntryID // task name -> scheduled cron entry

	runMu    sync.Mutex
	running  map[string]int64        // task name -> in-flight run id (single-flight)
	casts    map[string]*broadcaster // "name/id" -> live log broadcaster
	stopping bool                    // set under runMu once Stop begins; blocks new runs

	wg sync.WaitGroup // tracks in-flight run goroutines for Stop
}

// New builds an Engine. The cron scheduler is created but not started; call
// Start to schedule the enabled tasks.
func New(cfg *model.Config, st store.Store, disp *notify.Dispatcher, shellExec, agentExec executor.Executor) *Engine {
	return &Engine{
		store:     st,
		disp:      disp,
		shellExec: shellExec,
		agentExec: agentExec,
		cfg:       cfg,
		cron:      cron.New(),
		entries:   make(map[string]cron.EntryID),
		running:   make(map[string]int64),
		casts:     make(map[string]*broadcaster),
	}
}

// Start schedules every enabled task that declares a cron expression and starts
// the scheduler. Auto-fired runs go through Trigger, sharing the single-flight
// and notification path with manual runs.
func (e *Engine) Start() {
	e.cfgMu.Lock()
	e.schedule(e.cfg)
	e.cfgMu.Unlock()
	e.cron.Start()
}

// schedule (re)builds cron entries from cfg. Callers must hold cfgMu.
func (e *Engine) schedule(cfg *model.Config) {
	for name, id := range e.entries {
		e.cron.Remove(id)
		delete(e.entries, name)
	}
	for i := range cfg.Tasks {
		t := cfg.Tasks[i]
		if !t.IsEnabled() || t.Cron == "" {
			continue
		}
		name := t.Name
		id, err := e.cron.AddFunc(t.Cron, func() { _, _ = e.Trigger(name, model.TriggerAuto) })
		if err != nil {
			// A bad cron expression must not abort scheduling of the rest.
			continue
		}
		e.entries[name] = id
	}
}

// Stop halts the scheduler and waits, best-effort, for in-flight runs to finish
// or for ctx to expire.
func (e *Engine) Stop(ctx context.Context) error {
	stopCtx := e.cron.Stop() // stops scheduling; returns a ctx done once jobs return

	// Refuse any new run (including manual Trigger) from here on, so no wg.Add can
	// race the wg.Wait below. Setting the flag under runMu establishes a
	// happens-before with Trigger's checked Add: every Add that will ever occur is
	// already reflected in the counter before Wait observes it.
	e.runMu.Lock()
	e.stopping = true
	e.runMu.Unlock()

	done := make(chan struct{})
	go func() {
		e.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-stopCtx.Done():
		// Scheduler drained its own callbacks; still wait for run goroutines.
		select {
		case <-done:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// Reload swaps the active configuration under lock and reschedules cron entries.
func (e *Engine) Reload(cfg *model.Config) error {
	e.cfgMu.Lock()
	e.cfg = cfg
	e.schedule(cfg)
	e.cfgMu.Unlock()
	return nil
}

// Trigger starts a run for the named task and returns its run id immediately;
// execution proceeds on a background goroutine. If a run is already in flight
// for the task, its id is returned and no second run is started (single-flight).
func (e *Engine) Trigger(name string, tk model.TriggerKind) (int64, error) {
	task, err := e.findTask(name)
	if err != nil {
		return 0, err
	}

	e.runMu.Lock()
	if e.stopping {
		e.runMu.Unlock()
		return 0, ErrShuttingDown
	}
	if id, ok := e.running[name]; ok {
		e.runMu.Unlock()
		return id, nil
	}

	run, err := e.store.Create(name, tk)
	if err != nil {
		e.runMu.Unlock()
		return 0, err
	}
	e.running[name] = run.ID
	b := newBroadcaster()
	e.casts[castKey(name, run.ID)] = b
	// Add to the WaitGroup under runMu, paired with the stopping check above, so
	// the Add cannot race Stop's wg.Wait: once Stop has set stopping (also under
	// runMu) no further Add can happen.
	e.wg.Add(1)
	e.runMu.Unlock()

	go e.execute(task, run, b)
	return run.ID, nil
}

// execute runs the task to completion, persisting output and the terminal
// status, dispatching notifications, and driving the live broadcaster.
func (e *Engine) execute(task model.Task, run model.Run, b *broadcaster) {
	defer e.wg.Done()

	status, exit := e.runOnce(task, run, b)

	if err := e.store.Finish(task.Name, run.ID, status, exit); err != nil {
		// Persisting the outcome failed; the run is still terminal in memory.
		// Subscribers and notifiers should still see the result.
		_ = err
	}

	// Enforce run-record retention (best-effort; must not affect the outcome).
	e.cfgMu.RLock()
	keep, maxAge := e.cfg.MaxRuns, e.cfg.MaxRunAge.Std()
	e.cfgMu.RUnlock()
	if keep > 0 || maxAge > 0 {
		_, _ = e.store.Prune(task.Name, keep, maxAge)
	}

	// Build the notification event from the freshest run record available.
	finished, err := e.store.Get(task.Name, run.ID)
	if err != nil {
		finished = run
		finished.Status = status
		finished.ExitCode = exit
		finished.FinishedAt = time.Now()
	}
	tail, _ := e.store.Tail(task.Name, run.ID, 40)
	ev := model.Event{Task: task, Run: finished, LogTail: tail}

	dispatchCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	e.disp.Dispatch(dispatchCtx, ev)
	cancel()

	// Signal terminal to live subscribers and retire the single-flight entry.
	b.close(status)
	e.runMu.Lock()
	delete(e.running, task.Name)
	delete(e.casts, castKey(task.Name, run.ID))
	e.runMu.Unlock()
}

// runOnce selects the executor, wires the log sinks and runs the task within a
// timeout-bounded context, returning the terminal status and exit code.
func (e *Engine) runOnce(task model.Task, run model.Run, b *broadcaster) (model.RunStatus, int) {
	lw, err := e.store.LogWriter(task.Name, run.ID)
	if err != nil {
		return model.StatusFailed, -1
	}
	defer lw.Close()

	// Tee combined output into the on-disk log and the live line publisher.
	live := newLineWriter(b.publish)
	w := io.MultiWriter(lw, live)

	timeout := task.Timeout.Std()
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	exec := e.executorFor(task)
	res, runErr := exec.Run(ctx, task, w)
	live.flush() // emit any trailing partial line before terminating the stream

	if runErr != nil {
		// Internal failure to even start/execute the task.
		return model.StatusFailed, -1
	}
	return res.Status, res.ExitCode
}

// executorFor picks the executor matching the task type, defaulting to shell.
func (e *Engine) executorFor(task model.Task) executor.Executor {
	if task.Type == model.TaskAgent {
		return e.agentExec
	}
	return e.shellExec
}

// Running reports whether a run is currently in flight for the task.
func (e *Engine) Running(name string) bool {
	e.runMu.Lock()
	defer e.runMu.Unlock()
	_, ok := e.running[name]
	return ok
}

// ListTasks returns a summary view of every configured task, enriched with its
// last run, next scheduled time and running state.
func (e *Engine) ListTasks() []model.TaskView {
	e.cfgMu.RLock()
	tasks := append([]model.Task(nil), e.cfg.Tasks...)
	e.cfgMu.RUnlock()

	views := make([]model.TaskView, 0, len(tasks))
	for _, t := range tasks {
		v := model.TaskView{
			Task:    t,
			NextRun: e.nextRun(t.Name),
			Running: e.Running(t.Name),
		}
		if runs, err := e.store.List(t.Name, 1); err == nil && len(runs) > 0 {
			last := runs[0]
			v.LastRun = &last
		}
		views = append(views, v)
	}
	return views
}

// GetTask returns a task with its recent run history (newest first, capped),
// next scheduled time and running state.
func (e *Engine) GetTask(name string) (model.TaskDetail, error) {
	task, err := e.findTask(name)
	if err != nil {
		return model.TaskDetail{}, err
	}
	runs, err := e.store.List(name, detailRunCap)
	if err != nil {
		return model.TaskDetail{}, err
	}
	return model.TaskDetail{
		Task:    task,
		Runs:    runs,
		NextRun: e.nextRun(name),
		Running: e.Running(name),
	}, nil
}

// ListRuns returns the most recent runs for a task, newest first.
func (e *Engine) ListRuns(name string, limit int) ([]model.Run, error) {
	if _, err := e.findTask(name); err != nil {
		return nil, err
	}
	return e.store.List(name, limit)
}

// GetRun returns a single run record for a task.
func (e *Engine) GetRun(name string, id int64) (model.Run, error) {
	if _, err := e.findTask(name); err != nil {
		return model.Run{}, err
	}
	run, err := e.store.Get(name, id)
	if err != nil {
		return model.Run{}, mapNotFound(err)
	}
	return run, nil
}

// OpenLog returns a reader over a run's full output log.
func (e *Engine) OpenLog(name string, id int64) (io.ReadCloser, error) {
	if _, err := e.findTask(name); err != nil {
		return nil, err
	}
	rc, err := e.store.Open(name, id)
	if err != nil {
		return nil, mapNotFound(err)
	}
	return rc, nil
}

// findTask resolves a task by name from the active config.
func (e *Engine) findTask(name string) (model.Task, error) {
	e.cfgMu.RLock()
	defer e.cfgMu.RUnlock()
	for i := range e.cfg.Tasks {
		if e.cfg.Tasks[i].Name == name {
			return e.cfg.Tasks[i], nil
		}
	}
	return model.Task{}, ErrNotFound
}

// nextRun reports the next scheduled time for a task, or the zero time if it is
// not scheduled.
func (e *Engine) nextRun(name string) time.Time {
	// Hold cfgMu across the cron lookup so a concurrent Reload/schedule (which
	// removes and rebuilds entries under cfgMu) cannot retire this entry id
	// between reading the map and querying cron, which would yield a zero Entry
	// and a stale/empty NextRun. cron.Entry takes its own internal lock and does
	// not call back into the engine, so holding cfgMu here is safe.
	e.cfgMu.RLock()
	defer e.cfgMu.RUnlock()
	id, ok := e.entries[name]
	if !ok {
		return time.Time{}
	}
	return e.cron.Entry(id).Next
}

// broadcasterFor returns the live broadcaster for a run, or nil if the run is
// not currently in flight.
func (e *Engine) broadcasterFor(name string, id int64) *broadcaster {
	e.runMu.Lock()
	defer e.runMu.Unlock()
	return e.casts[castKey(name, id)]
}

// castKey is the map key identifying a run's broadcaster.
func castKey(name string, id int64) string {
	return name + "/" + strconv.FormatInt(id, 10)
}

// mapNotFound translates the store's not-exist sentinel into ErrNotFound,
// leaving other errors untouched.
func mapNotFound(err error) error {
	if errors.Is(err, os.ErrNotExist) {
		return ErrNotFound
	}
	return err
}
