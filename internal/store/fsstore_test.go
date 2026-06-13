package store

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/wanggang316/the-loop/internal/model"
)

// newStore builds an FSStore rooted at a fresh temp dir.
func newStore(t *testing.T) *FSStore {
	t.Helper()
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

// writeLog opens the run's log, writes data, and closes the writer.
func writeLog(t *testing.T, s *FSStore, task string, runID int64, data string) {
	t.Helper()
	w, err := s.LogWriter(task, runID)
	if err != nil {
		t.Fatalf("LogWriter: %v", err)
	}
	if _, err := io.WriteString(w, data); err != nil {
		w.Close()
		t.Fatalf("write log: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close log: %v", err)
	}
}

// TestRoundTrip exercises the full lifecycle: Create -> log write -> Finish ->
// Get / Open / Tail, verifying the persisted record at each step.
func TestRoundTrip(t *testing.T) {
	s := newStore(t)
	const task = "backup"

	run, err := s.Create(task, model.TriggerManual)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if run.ID != 1 {
		t.Errorf("first run id = %d, want 1", run.ID)
	}
	if run.Status != model.StatusPending {
		t.Errorf("status = %q, want %q", run.Status, model.StatusPending)
	}
	if run.Trigger != model.TriggerManual {
		t.Errorf("trigger = %q, want %q", run.Trigger, model.TriggerManual)
	}
	if run.TaskName != task {
		t.Errorf("task = %q, want %q", run.TaskName, task)
	}
	if run.StartedAt.IsZero() {
		t.Error("StartedAt is zero")
	}
	if run.LogPath == "" {
		t.Error("LogPath is empty")
	}

	const logBody = "line one\nline two\nline three\n"
	writeLog(t, s, task, run.ID, logBody)

	if err := s.Finish(task, run.ID, model.StatusSuccess, 0); err != nil {
		t.Fatalf("Finish: %v", err)
	}

	// Get reflects the finished state.
	got, err := s.Get(task, run.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != model.StatusSuccess {
		t.Errorf("status = %q, want %q", got.Status, model.StatusSuccess)
	}
	if got.ExitCode != 0 {
		t.Errorf("exit code = %d, want 0", got.ExitCode)
	}
	if got.FinishedAt.IsZero() {
		t.Error("FinishedAt is zero after Finish")
	}
	if !got.Status.Terminal() {
		t.Error("finished run status is not terminal")
	}

	// Open returns the full log verbatim.
	rc, err := s.Open(task, run.ID)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if string(data) != logBody {
		t.Errorf("log = %q, want %q", data, logBody)
	}

	// Tail returns the last n lines, no trailing newline.
	tail, err := s.Tail(task, run.ID, 2)
	if err != nil {
		t.Fatalf("Tail: %v", err)
	}
	if want := "line two\nline three"; tail != want {
		t.Errorf("Tail(2) = %q, want %q", tail, want)
	}

	// Asking for more lines than exist returns all of them.
	tailAll, err := s.Tail(task, run.ID, 100)
	if err != nil {
		t.Fatalf("Tail(100): %v", err)
	}
	if want := "line one\nline two\nline three"; tailAll != want {
		t.Errorf("Tail(100) = %q, want %q", tailAll, want)
	}

	// Tail with n <= 0 yields an empty string.
	if tz, err := s.Tail(task, run.ID, 0); err != nil || tz != "" {
		t.Errorf("Tail(0) = %q, %v; want \"\", nil", tz, err)
	}
}

// TestFailureRun verifies a non-zero exit is persisted as failed.
func TestFailureRun(t *testing.T) {
	s := newStore(t)
	const task = "deploy"

	run, err := s.Create(task, model.TriggerAuto)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := s.Finish(task, run.ID, model.StatusFailed, 17); err != nil {
		t.Fatalf("Finish: %v", err)
	}
	got, err := s.Get(task, run.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != model.StatusFailed {
		t.Errorf("status = %q, want %q", got.Status, model.StatusFailed)
	}
	if got.ExitCode != 17 {
		t.Errorf("exit code = %d, want 17", got.ExitCode)
	}
}

// TestMonotonicIDsAndListOrder creates several runs and checks ids are
// monotonically increasing while List reports them newest-first.
func TestMonotonicIDsAndListOrder(t *testing.T) {
	s := newStore(t)
	const task = "report"
	const n = 5

	var prev int64
	for i := 0; i < n; i++ {
		run, err := s.Create(task, model.TriggerAuto)
		if err != nil {
			t.Fatalf("Create #%d: %v", i, err)
		}
		if run.ID <= prev {
			t.Fatalf("ids not monotonic: got %d after %d", run.ID, prev)
		}
		prev = run.ID
		if err := s.Finish(task, run.ID, model.StatusSuccess, 0); err != nil {
			t.Fatalf("Finish #%d: %v", i, err)
		}
	}

	runs, err := s.List(task, 0) // 0 => all
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(runs) != n {
		t.Fatalf("List returned %d runs, want %d", len(runs), n)
	}
	// Newest-first: strictly descending ids, top id == n.
	for i := 1; i < len(runs); i++ {
		if runs[i-1].ID <= runs[i].ID {
			t.Errorf("List not newest-first at %d: %d then %d", i, runs[i-1].ID, runs[i].ID)
		}
	}
	if runs[0].ID != int64(n) {
		t.Errorf("newest id = %d, want %d", runs[0].ID, n)
	}

	// A positive limit truncates from the newest end.
	limited, err := s.List(task, 2)
	if err != nil {
		t.Fatalf("List(2): %v", err)
	}
	if len(limited) != 2 {
		t.Fatalf("List(2) returned %d runs, want 2", len(limited))
	}
	if limited[0].ID != int64(n) || limited[1].ID != int64(n-1) {
		t.Errorf("List(2) ids = %d,%d; want %d,%d", limited[0].ID, limited[1].ID, n, n-1)
	}
}

// TestConcurrentCreatesUniqueIDs spawns many goroutines that Create runs for the
// same task concurrently and asserts every id is unique and the set is exactly
// 1..N (no gaps, no collisions).
func TestConcurrentCreatesUniqueIDs(t *testing.T) {
	s := newStore(t)
	const task = "concurrent"
	const n = 64

	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		ids  = make([]int64, 0, n)
		fail error
	)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			run, err := s.Create(task, model.TriggerAuto)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if fail == nil {
					fail = err
				}
				return
			}
			ids = append(ids, run.ID)
		}()
	}
	wg.Wait()

	if fail != nil {
		t.Fatalf("concurrent Create failed: %v", fail)
	}
	if len(ids) != n {
		t.Fatalf("got %d ids, want %d", len(ids), n)
	}

	seen := make(map[int64]bool, n)
	for _, id := range ids {
		if seen[id] {
			t.Fatalf("duplicate run id %d from concurrent Create", id)
		}
		seen[id] = true
	}
	// Ids must form a contiguous 1..n range.
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for i, id := range ids {
		if id != int64(i+1) {
			t.Fatalf("ids not contiguous: position %d has id %d", i, id)
		}
	}

	// The store also sees exactly n runs.
	runs, err := s.List(task, 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(runs) != n {
		t.Errorf("List returned %d runs, want %d", len(runs), n)
	}
}

// TestPerTaskIDIsolation verifies ids are allocated per task, not globally.
func TestPerTaskIDIsolation(t *testing.T) {
	s := newStore(t)

	a1, err := s.Create("alpha", model.TriggerAuto)
	if err != nil {
		t.Fatalf("Create alpha: %v", err)
	}
	b1, err := s.Create("beta", model.TriggerAuto)
	if err != nil {
		t.Fatalf("Create beta: %v", err)
	}
	if a1.ID != 1 || b1.ID != 1 {
		t.Errorf("per-task ids should each start at 1, got alpha=%d beta=%d", a1.ID, b1.ID)
	}
	a2, err := s.Create("alpha", model.TriggerAuto)
	if err != nil {
		t.Fatalf("Create alpha #2: %v", err)
	}
	if a2.ID != 2 {
		t.Errorf("alpha second id = %d, want 2", a2.ID)
	}
}

// TestMissingRunErrors verifies the not-found contract for Get/Open and that
// Tail degrades to an empty string for a missing log.
func TestMissingRunErrors(t *testing.T) {
	s := newStore(t)
	const task = "ghost"

	if _, err := s.Get(task, 999); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Get missing: err = %v, want os.ErrNotExist", err)
	}
	if _, err := s.Open(task, 999); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Open missing: err = %v, want os.ErrNotExist", err)
	}

	// A created run with no log yet: Tail is empty, Open errors not-found.
	run, err := s.Create(task, model.TriggerAuto)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if tail, err := s.Tail(task, run.ID, 5); err != nil || tail != "" {
		t.Errorf("Tail on missing log = %q, %v; want \"\", nil", tail, err)
	}
}

// TestListEmptyTask confirms listing an unknown task yields no runs, no error.
func TestListEmptyTask(t *testing.T) {
	s := newStore(t)
	runs, err := s.List("never-created", 0)
	if err != nil {
		t.Fatalf("List unknown task: %v", err)
	}
	if len(runs) != 0 {
		t.Errorf("List unknown task returned %d runs, want 0", len(runs))
	}
}

// TestLogWriterAppends confirms successive LogWriter opens append rather than
// truncate, matching streaming-execution semantics.
func TestLogWriterAppends(t *testing.T) {
	s := newStore(t)
	const task = "stream"

	run, err := s.Create(task, model.TriggerAuto)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	writeLog(t, s, task, run.ID, "first\n")
	writeLog(t, s, task, run.ID, "second\n")

	rc, err := s.Open(task, run.ID)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got := string(data); got != "first\nsecond\n" {
		t.Errorf("log = %q, want %q", got, "first\nsecond\n")
	}
	if !strings.Contains(string(data), "first") {
		t.Error("first write was truncated by second open")
	}
}

// TestTailOverlongLine guards against the regression where Tail used a
// bufio.Scanner capped at 1 MiB and returned an empty string (token too long)
// whenever any single log line exceeded that cap, dropping the entire tail
// including the recent short lines. A single >1 MiB line is realistic for a
// task daemon running arbitrary commands (a curl dump, a base64/JSON blob).
// Reading from the end in fixed-size chunks makes line length irrelevant.
func TestTailOverlongLine(t *testing.T) {
	s := newStore(t)
	const task = "huge"

	run, err := s.Create(task, model.TriggerAuto)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// One blob line of 2 MiB (well over the old 1 MiB cap), then two short
	// trailing lines that must survive.
	blob := strings.Repeat("x", 2*1024*1024)
	body := "head\n" + blob + "\nsecond-last\nlast\n"
	writeLog(t, s, task, run.ID, body)

	// The trailing short lines must come back intact, with no error.
	tail, err := s.Tail(task, run.ID, 2)
	if err != nil {
		t.Fatalf("Tail(2) on overlong-line log: %v", err)
	}
	if want := "second-last\nlast"; tail != want {
		t.Errorf("Tail(2) = %q, want %q", abbrev(tail), want)
	}

	// Tailing across the over-long line returns it verbatim alongside the
	// neighbours, still without error.
	tail3, err := s.Tail(task, run.ID, 3)
	if err != nil {
		t.Fatalf("Tail(3) on overlong-line log: %v", err)
	}
	if want := blob + "\nsecond-last\nlast"; tail3 != want {
		t.Errorf("Tail(3) did not preserve the over-long line (got %d bytes, want %d)",
			len(tail3), len(want))
	}
}

// TestTailLineExactlyAtChunkBoundary exercises a line whose length is a
// multiple of the backward-read chunk size, to make sure the chunked reader
// stitches lines correctly across read boundaries rather than only when a
// single read happens to contain a newline.
func TestTailLineExactlyAtChunkBoundary(t *testing.T) {
	s := newStore(t)
	const task = "boundary"

	run, err := s.Create(task, model.TriggerAuto)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	mid := strings.Repeat("y", 3*tailChunk) // spans several backward chunks
	body := "a\n" + mid + "\nz\n"
	writeLog(t, s, task, run.ID, body)

	tail, err := s.Tail(task, run.ID, 2)
	if err != nil {
		t.Fatalf("Tail: %v", err)
	}
	if want := mid + "\nz"; tail != want {
		t.Errorf("Tail(2) across chunk boundary corrupted the long line (got %d bytes, want %d)",
			len(tail), len(want))
	}
}

// TestInvalidTaskNameRejected verifies that task names which are not a safe
// single path segment are rejected by every Store method, and crucially that a
// traversal name like "../../evil" writes nothing outside <dataDir>/runs. This
// is the path-traversal / data-dir-escape defense the store must enforce
// regardless of caller.
func TestInvalidTaskNameRejected(t *testing.T) {
	root := t.TempDir()
	s, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	bad := []string{
		"a/b",            // nested path segment
		"..",             // parent dir
		".",              // current dir
		"../evil",        // single traversal
		"../../../tmp/x", // deep traversal escaping the data dir
		`back\slash`,     // windows-style separator
		"nul\x00byte",    // smuggled NUL
		"",               // empty
	}

	for _, name := range bad {
		t.Run(quote(name), func(t *testing.T) {
			// Create must refuse and produce no run.
			if _, err := s.Create(name, model.TriggerManual); err == nil {
				t.Fatalf("Create(%q) succeeded, want rejection", name)
			}
			// Every read/write entry point must reject too, so the raw name
			// never reaches filepath.Join.
			if _, err := s.LogWriter(name, 1); err == nil {
				t.Errorf("LogWriter(%q) succeeded, want rejection", name)
			}
			if _, err := s.Get(name, 1); err == nil {
				t.Errorf("Get(%q) succeeded, want rejection", name)
			}
			if _, err := s.List(name, 0); err == nil {
				t.Errorf("List(%q) succeeded, want rejection", name)
			}
			if _, err := s.Open(name, 1); err == nil {
				t.Errorf("Open(%q) succeeded, want rejection", name)
			}
			if _, err := s.Tail(name, 1, 10); err == nil {
				t.Errorf("Tail(%q) succeeded, want rejection", name)
			}
			if err := s.Finish(name, 1, model.StatusSuccess, 0); err == nil {
				t.Errorf("Finish(%q) succeeded, want rejection", name)
			}
		})
	}

	// Nothing must have been written outside <root>/runs. Walk the parent of
	// root (a private temp dir) and assert no stray "evil"/"x" artifacts and no
	// entries directly under root other than the expected "runs" dir.
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("ReadDir(root): %v", err)
	}
	for _, e := range entries {
		if e.Name() != "runs" {
			t.Errorf("unexpected entry %q directly under data dir; traversal may have written outside runs/", e.Name())
		}
	}

	// The runs dir itself must contain no task subdirectories (every Create was
	// rejected), so no traversal name materialized a directory there either.
	runsEntries, err := os.ReadDir(filepath.Join(root, "runs"))
	if err != nil {
		t.Fatalf("ReadDir(runs): %v", err)
	}
	if len(runsEntries) != 0 {
		var names []string
		for _, e := range runsEntries {
			names = append(names, e.Name())
		}
		t.Errorf("runs dir is not empty after all-rejected creates: %v", names)
	}

	// A legitimate name still works, proving the guard is not over-broad.
	if _, err := s.Create("ok-task_1", model.TriggerManual); err != nil {
		t.Fatalf("Create with a valid name failed: %v", err)
	}
}

// abbrev shortens very long strings for readable failure messages.
func abbrev(s string) string {
	const max = 80
	if len(s) <= max {
		return s
	}
	return s[:max] + "...(" + strconv.Itoa(len(s)) + " bytes)"
}

// quote produces a subtest-name-safe rendering of a task name, including
// non-printable bytes, so the empty/NUL cases get distinct names.
func quote(s string) string {
	if s == "" {
		return "empty"
	}
	return strconv.Quote(s)
}
