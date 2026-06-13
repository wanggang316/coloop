package engine

import (
	"bufio"
	"bytes"
	"context"
	"sync"

	"github.com/wanggang316/the-loop/internal/model"
)

// LogEvent is one item streamed by StreamLog: either a log line or, as the very
// last item, a terminal marker carrying the run's final status.
type LogEvent struct {
	Line   string          `json:"line,omitempty"`
	Done   bool            `json:"done,omitempty"`
	Status model.RunStatus `json:"status,omitempty"`
}

// subChanBuf bounds how far a slow subscriber may lag before lines are dropped.
const subChanBuf = 256

// broadcaster fans out the live log lines of a single in-flight run to any
// number of subscribers, then signals termination exactly once.
//
// The broadcaster retains the full line history for the run. A subscriber
// attaches atomically with a snapshot of that history (see subscribe), so
// StreamLog can replay the snapshot and then forward live lines without any
// separate file read — guaranteeing each line is delivered exactly once (no
// gap, no duplication) while the run is live.
type broadcaster struct {
	mu      sync.Mutex
	subs    map[chan LogEvent]struct{}
	history []string // every line published so far, in order
	done    bool
	status  model.RunStatus
}

func newBroadcaster() *broadcaster {
	return &broadcaster{subs: make(map[chan LogEvent]struct{})}
}

// publish records a line in the run's history and delivers it to every current
// subscriber. A subscriber whose buffer is full has the line dropped rather than
// blocking the run. The history append and the fan-out happen under the same
// lock as subscribe, so a newly attached subscriber sees each line either in its
// history snapshot or on its channel, never both and never neither.
func (b *broadcaster) publish(line string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.done {
		return
	}
	b.history = append(b.history, line)
	for ch := range b.subs {
		select {
		case ch <- LogEvent{Line: line}:
		default:
		}
	}
}

// close marks the run terminal, notifies every subscriber with a final Done
// event and closes their channels. Idempotent.
func (b *broadcaster) close(status model.RunStatus) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.done {
		return
	}
	b.done = true
	b.status = status
	for ch := range b.subs {
		// Best-effort terminal marker, then close so the reader's range ends.
		select {
		case ch <- LogEvent{Done: true, Status: status}:
		default:
		}
		close(ch)
	}
	b.subs = nil
}

// subscribe attaches a new subscriber and returns, atomically with the attach, a
// snapshot of the history published so far. The caller replays history first,
// then forwards lines arriving on ch; because the append and the channel send in
// publish share this lock, every line lands in exactly one of the two (history
// snapshot or channel), so replay+live yields each line once.
//
// The returned live bool reports whether the run was still live; if it had
// already terminated, terminal is the final status and ch is nil (history is
// still returned so a just-terminated run can be replayed from memory).
func (b *broadcaster) subscribe() (ch chan LogEvent, history []string, live bool, terminal model.RunStatus) {
	b.mu.Lock()
	defer b.mu.Unlock()
	snapshot := append([]string(nil), b.history...)
	if b.done {
		return nil, snapshot, false, b.status
	}
	c := make(chan LogEvent, subChanBuf)
	b.subs[c] = struct{}{}
	return c, snapshot, true, ""
}

// unsubscribe detaches a subscriber that is leaving early (e.g. ctx cancelled),
// draining and closing its channel under the lock to avoid a double close with
// a concurrent broadcaster.close.
func (b *broadcaster) unsubscribe(ch chan LogEvent) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.subs[ch]; !ok {
		return // already removed (run terminated and closed it)
	}
	delete(b.subs, ch)
	close(ch)
}

// lineWriter splits the byte stream from an executor into newline-delimited
// lines and forwards each completed line to publish. It is wrapped together with
// the store's log writer by a tee so the on-disk log and live stream stay in
// step.
//
// lineWriter is used by a single executor goroutine, so it needs no locking of
// its own buffer.
type lineWriter struct {
	publish func(string)
	buf     bytes.Buffer
}

func newLineWriter(publish func(string)) *lineWriter {
	return &lineWriter{publish: publish}
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.buf.Write(p)
	for {
		data := w.buf.Bytes()
		i := bytes.IndexByte(data, '\n')
		if i < 0 {
			break
		}
		line := string(data[:i])
		w.buf.Next(i + 1)
		w.publish(line)
	}
	return len(p), nil
}

// flush emits any trailing bytes that were not newline-terminated as a final
// line. Called once the executor has finished writing.
func (w *lineWriter) flush() {
	if w.buf.Len() > 0 {
		w.publish(w.buf.String())
		w.buf.Reset()
	}
}

// StreamLog replays the run's existing log lines, then streams live lines until
// the run terminates, then sends a final LogEvent{Done:true,Status} and closes
// the channel. If the run is already terminal it just replays and signals Done.
//
// While the run is live, replay is served from the broadcaster's in-memory
// history captured atomically with the live subscription, so there is no
// separate file read racing the writer: each line is emitted exactly once. For a
// terminal run with no live broadcaster, replay reads the on-disk log.
//
// The returned channel is always closed after the terminal event. ctx
// cancellation stops streaming promptly and closes the channel without leaking
// goroutines.
func (e *Engine) StreamLog(ctx context.Context, name string, id int64) (<-chan LogEvent, error) {
	if _, err := e.findTask(name); err != nil {
		return nil, err
	}

	// Attach to the live broadcaster (if any), capturing its history snapshot and
	// live channel atomically. If there is no live broadcaster the run is already
	// terminal (or never streamed); we replay the file and fall back to the
	// stored status.
	b := e.broadcasterFor(name, id)
	var (
		sub      chan LogEvent
		history  []string
		live     bool
		terminal model.RunStatus
	)
	if b != nil {
		sub, history, live, terminal = b.subscribe()
	}

	// Resolve the run so we can know its status when not live.
	run, err := e.store.Get(name, id)
	if err != nil {
		if sub != nil {
			b.unsubscribe(sub)
		}
		return nil, mapNotFound(err)
	}
	if !live && terminal == "" {
		terminal = run.Status
	}

	out := make(chan LogEvent, subChanBuf)
	go e.streamLoop(ctx, out, name, id, b, sub, history, live, terminal)
	return out, nil
}

// streamLoop performs replay + live forwarding for StreamLog on its own
// goroutine.
func (e *Engine) streamLoop(ctx context.Context, out chan LogEvent, name string, id int64, b *broadcaster, sub chan LogEvent, history []string, live bool, terminal model.RunStatus) {
	defer close(out)

	// Replay existing lines. When a broadcaster is present we replay its in-memory
	// history snapshot (no file read, so it cannot race the live writer); the live
	// channel carries only lines published after the snapshot. When there is no
	// broadcaster the run is terminal, so the on-disk log is complete and stable.
	var replayDone bool
	if b != nil {
		replayDone = replayHistory(ctx, out, history)
	} else {
		replayDone = e.replayLog(ctx, out, name, id)
	}
	if replayDone {
		// ctx cancelled mid-replay: detach and stop.
		if live {
			b.unsubscribe(sub)
		}
		return
	}

	if !live {
		// Already terminal: just signal Done with the stored status.
		sendEvent(ctx, out, LogEvent{Done: true, Status: terminal})
		return
	}

	// Forward live lines until the broadcaster closes sub (terminal) or ctx ends.
	for {
		select {
		case <-ctx.Done():
			b.unsubscribe(sub)
			return
		case ev, ok := <-sub:
			if !ok {
				// Broadcaster closed: the run terminated. The final Done event was
				// the last value drained from sub if buffered; emit a Done anyway
				// using the recorded status to guarantee the contract.
				status := e.finalStatus(name, id)
				sendEvent(ctx, out, LogEvent{Done: true, Status: status})
				return
			}
			if ev.Done {
				if !sendEvent(ctx, out, ev) {
					b.unsubscribe(sub)
				}
				// Drain until close, then return.
				for range sub {
				}
				return
			}
			if !sendEvent(ctx, out, ev) {
				b.unsubscribe(sub)
				return
			}
		}
	}
}

// replayHistory streams an in-memory line snapshot to out. It returns true if
// ctx was cancelled before replay completed.
func replayHistory(ctx context.Context, out chan LogEvent, history []string) bool {
	for _, line := range history {
		if !sendEvent(ctx, out, LogEvent{Line: line}) {
			return true
		}
	}
	return false
}

// replayLog streams the stored log lines to out. It returns true if ctx was
// cancelled before replay completed.
func (e *Engine) replayLog(ctx context.Context, out chan LogEvent, name string, id int64) bool {
	rc, err := e.store.Open(name, id)
	if err != nil {
		return false // no log yet is fine; nothing to replay
	}
	defer rc.Close()

	sc := bufio.NewScanner(rc)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		if !sendEvent(ctx, out, LogEvent{Line: sc.Text()}) {
			return true
		}
	}
	return false
}

// finalStatus reads the run's terminal status from the store, falling back to
// StatusFailed if it cannot be read (a terminated run should always be readable).
func (e *Engine) finalStatus(name string, id int64) model.RunStatus {
	if run, err := e.store.Get(name, id); err == nil && run.Status.Terminal() {
		return run.Status
	}
	return model.StatusFailed
}

// sendEvent writes ev to out, honouring ctx cancellation. It reports whether the
// send succeeded.
func sendEvent(ctx context.Context, out chan LogEvent, ev LogEvent) bool {
	select {
	case <-ctx.Done():
		return false
	case out <- ev:
		return true
	}
}
