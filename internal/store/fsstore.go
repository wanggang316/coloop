package store

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wanggang316/the-loop/internal/model"
)

// FSStore is a filesystem-backed implementation of Store. A single daemon owns
// all writes; a mutex serializes run-id allocation so concurrent Creates for
// the same task never collide on a directory.
type FSStore struct {
	dataDir string

	mu sync.Mutex // guards run-id allocation + dir creation in Create
}

// New returns an FSStore rooted at dataDir, ensuring <dataDir>/runs exists.
func New(dataDir string) (*FSStore, error) {
	abs, err := filepath.Abs(dataDir)
	if err != nil {
		return nil, fmt.Errorf("store: resolve data dir: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(abs, "runs"), 0o755); err != nil {
		return nil, fmt.Errorf("store: create runs dir: %w", err)
	}
	return &FSStore{dataDir: abs}, nil
}

// validTaskName rejects names that are not a safe single path segment, so a
// task name can never escape <dataDir>/runs (e.g. "a/b", "..", "../evil") or
// smuggle a NUL byte. This is defense in depth: config.Validate is the primary
// gatekeeper, but the store must not write outside its root regardless of
// caller.
func validTaskName(taskName string) error {
	if taskName == "" || taskName == "." || taskName == ".." ||
		strings.ContainsAny(taskName, "/\\\x00") || taskName != filepath.Base(taskName) {
		return fmt.Errorf("store: invalid task name %q", taskName)
	}
	return nil
}

// runsDir is <dataDir>/runs/<task>.
func (s *FSStore) runsDir(taskName string) string {
	return filepath.Join(s.dataDir, "runs", taskName)
}

// runDir is <dataDir>/runs/<task>/<id padded %06d>.
func (s *FSStore) runDir(taskName string, runID int64) string {
	return filepath.Join(s.runsDir(taskName), fmt.Sprintf("%06d", runID))
}

func (s *FSStore) metaPath(taskName string, runID int64) string {
	return filepath.Join(s.runDir(taskName, runID), "meta.json")
}

func (s *FSStore) logPath(taskName string, runID int64) string {
	return filepath.Join(s.runDir(taskName, runID), "output.log")
}

// Create allocates a monotonic per-task run id and writes a pending meta.json.
func (s *FSStore) Create(taskName string, trigger model.TriggerKind) (model.Run, error) {
	if err := validTaskName(taskName); err != nil {
		return model.Run{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	taskDir := s.runsDir(taskName)
	if err := os.MkdirAll(taskDir, 0o755); err != nil {
		return model.Run{}, fmt.Errorf("store: create task dir: %w", err)
	}

	id, err := s.maxRunID(taskDir)
	if err != nil {
		return model.Run{}, err
	}
	id++

	dir := s.runDir(taskName, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return model.Run{}, fmt.Errorf("store: create run dir: %w", err)
	}

	run := model.Run{
		ID:        id,
		TaskName:  taskName,
		Trigger:   trigger,
		Status:    model.StatusPending,
		StartedAt: time.Now(),
		LogPath:   s.logPath(taskName, id),
	}
	if err := writeMeta(s.metaPath(taskName, id), run); err != nil {
		return model.Run{}, err
	}
	return run, nil
}

// maxRunID scans existing run dirs and returns the highest numeric id, or 0.
func (s *FSStore) maxRunID(taskDir string) (int64, error) {
	entries, err := os.ReadDir(taskDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("store: scan run dirs: %w", err)
	}
	var max int64
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		n, err := strconv.ParseInt(e.Name(), 10, 64)
		if err != nil {
			continue // ignore non-numeric entries
		}
		if n > max {
			max = n
		}
	}
	return max, nil
}

// LogWriter opens the run's output log for appending.
func (s *FSStore) LogWriter(taskName string, runID int64) (io.WriteCloser, error) {
	if err := validTaskName(taskName); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(s.logPath(taskName, runID), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("store: open log for write: %w", err)
	}
	return f, nil
}

// Finish records the terminal status, exit code and finish time, atomically
// rewrites meta.json, then appends a summary line to history.jsonl.
func (s *FSStore) Finish(taskName string, runID int64, status model.RunStatus, exitCode int) error {
	run, err := s.Get(taskName, runID)
	if err != nil {
		return err
	}

	run.Status = status
	run.ExitCode = exitCode
	run.FinishedAt = time.Now()

	if err := writeMeta(s.metaPath(taskName, runID), run); err != nil {
		return err
	}

	// Best-effort history index; correctness does not depend on it.
	s.appendHistory(taskName, run)
	return nil
}

// appendHistory appends one JSON line to <task>/history.jsonl. Errors are
// swallowed: this index is a convenience, not a source of truth.
func (s *FSStore) appendHistory(taskName string, run model.Run) {
	f, err := os.OpenFile(filepath.Join(s.runsDir(taskName), "history.jsonl"),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	defer f.Close()

	line, err := json.Marshal(run)
	if err != nil {
		return
	}
	line = append(line, '\n')
	_, _ = f.Write(line)
}

// Get reads and unmarshals the run's meta.json. Returns os.ErrNotExist if the
// run does not exist.
func (s *FSStore) Get(taskName string, runID int64) (model.Run, error) {
	if err := validTaskName(taskName); err != nil {
		return model.Run{}, err
	}
	data, err := os.ReadFile(s.metaPath(taskName, runID))
	if err != nil {
		if os.IsNotExist(err) {
			return model.Run{}, os.ErrNotExist
		}
		return model.Run{}, fmt.Errorf("store: read meta: %w", err)
	}
	var run model.Run
	if err := json.Unmarshal(data, &run); err != nil {
		return model.Run{}, fmt.Errorf("store: unmarshal meta: %w", err)
	}
	return run, nil
}

// List returns the most recent runs for a task, newest first. A limit <= 0
// means all.
func (s *FSStore) List(taskName string, limit int) ([]model.Run, error) {
	if err := validTaskName(taskName); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(s.runsDir(taskName))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("store: list runs: %w", err)
	}

	runs := make([]model.Run, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		id, err := strconv.ParseInt(e.Name(), 10, 64)
		if err != nil {
			continue
		}
		run, err := s.Get(taskName, id)
		if err != nil {
			continue // skip unreadable / partial runs
		}
		runs = append(runs, run)
	}

	sort.Slice(runs, func(i, j int) bool { return runs[i].ID > runs[j].ID })

	if limit > 0 && len(runs) > limit {
		runs = runs[:limit]
	}
	return runs, nil
}

// Open returns a reader over the run's full output log.
func (s *FSStore) Open(taskName string, runID int64) (io.ReadCloser, error) {
	if err := validTaskName(taskName); err != nil {
		return nil, err
	}
	f, err := os.Open(s.logPath(taskName, runID))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, os.ErrNotExist
		}
		return nil, fmt.Errorf("store: open log: %w", err)
	}
	return f, nil
}

// tailChunk is the read size used when scanning the log backwards. Work stays
// bounded to O(bytes actually needed) rather than O(filesize).
const tailChunk = 64 * 1024

// Tail returns up to n trailing lines of the run's output log. It reads from
// the end of the file in fixed-size chunks, so line length is irrelevant (no
// over-long-line failure) and a multi-GB log is never scanned from the start.
func (s *FSStore) Tail(taskName string, runID int64, n int) (string, error) {
	if err := validTaskName(taskName); err != nil {
		return "", err
	}
	if n <= 0 {
		return "", nil
	}
	f, err := os.Open(s.logPath(taskName, runID))
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("store: open log for tail: %w", err)
	}
	defer f.Close()

	size, err := f.Seek(0, io.SeekEnd)
	if err != nil {
		return "", fmt.Errorf("store: seek log: %w", err)
	}

	// Read backwards from EOF, prepending chunks, until we have collected more
	// than n newlines or reached the start of the file. The trailing newline of
	// the file does not start a new line, so we count an extra one.
	var (
		buf      []byte
		pos      = size
		newlines int
	)
	for pos > 0 && newlines <= n {
		readSize := int64(tailChunk)
		if readSize > pos {
			readSize = pos
		}
		pos -= readSize

		chunk := make([]byte, readSize)
		if _, err := f.ReadAt(chunk, pos); err != nil && err != io.EOF {
			return "", fmt.Errorf("store: read log for tail: %w", err)
		}
		buf = append(chunk, buf...)
		newlines += bytes.Count(chunk, []byte{'\n'})
	}

	// Drop a single trailing newline so it does not yield an empty final line.
	if len(buf) > 0 && buf[len(buf)-1] == '\n' {
		buf = buf[:len(buf)-1]
	}
	lines := strings.Split(string(buf), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n"), nil
}

// writeMeta marshals run and writes it atomically (temp file then rename).
func writeMeta(path string, run model.Run) error {
	data, err := json.MarshalIndent(run, "", "  ")
	if err != nil {
		return fmt.Errorf("store: marshal meta: %w", err)
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "meta-*.tmp")
	if err != nil {
		return fmt.Errorf("store: create temp meta: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("store: write temp meta: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("store: close temp meta: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("store: rename meta: %w", err)
	}
	return nil
}

// Prune enforces retention for a task: it keeps the newest `keep` runs (when
// keep > 0) and removes runs whose StartedAt is older than `maxAge` (when
// maxAge > 0). A run is removed only if it is terminal, so an in-flight run is
// never deleted out from under the engine. The per-task single-flight guarantee
// means Prune for a given task never races another run of that task, so no
// extra locking is needed here (RemoveAll of old run dirs cannot affect the
// max-id scan, which only ever reads the highest id).
func (s *FSStore) Prune(taskName string, keep int, maxAge time.Duration) (int, error) {
	if err := validTaskName(taskName); err != nil {
		return 0, err
	}
	if keep <= 0 && maxAge <= 0 {
		return 0, nil
	}

	runs, err := s.List(taskName, 0) // newest first
	if err != nil {
		return 0, err
	}

	var cutoff time.Time
	if maxAge > 0 {
		cutoff = time.Now().Add(-maxAge)
	}

	removed := 0
	survivors := make([]model.Run, 0, len(runs))
	for i, r := range runs {
		overCount := keep > 0 && i >= keep
		tooOld := maxAge > 0 && r.StartedAt.Before(cutoff)
		if r.Status.Terminal() && (overCount || tooOld) {
			if err := os.RemoveAll(s.runDir(taskName, r.ID)); err == nil {
				removed++
				continue
			}
			// On removal failure, keep it as a survivor rather than losing track.
		}
		survivors = append(survivors, r)
	}

	if removed > 0 {
		s.rewriteHistory(taskName, survivors)
	}
	return removed, nil
}

// rewriteHistory replaces history.jsonl with the survivors' summary lines,
// oldest-first to match the append order Finish uses. Best-effort: the index is
// a convenience, not a source of truth (List reads run dirs directly).
func (s *FSStore) rewriteHistory(taskName string, survivors []model.Run) {
	var buf bytes.Buffer
	for i := len(survivors) - 1; i >= 0; i-- { // survivors are newest-first
		line, err := json.Marshal(survivors[i])
		if err != nil {
			continue
		}
		buf.Write(line)
		buf.WriteByte('\n')
	}

	dir := s.runsDir(taskName)
	tmp, err := os.CreateTemp(dir, "history-*.tmp")
	if err != nil {
		return
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(buf.Bytes()); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return
	}
	_ = os.Rename(tmpName, filepath.Join(dir, "history.jsonl"))
}
