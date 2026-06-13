package config

import (
	"io"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// watchDebounce coalesces bursts of filesystem events into a single onChange.
const watchDebounce = 300 * time.Millisecond

// Watch observes the config file and invokes onChange shortly after it is
// written. It watches the parent directory rather than the file itself so the
// watch survives the rename/replace pattern editors use on save. The returned
// io.Closer stops the watcher and releases its goroutine.
func Watch(path string, onChange func()) (io.Closer, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	dir := filepath.Dir(abs)
	target := filepath.Base(abs)

	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	if err := w.Add(dir); err != nil {
		_ = w.Close()
		return nil, err
	}

	c := &watcher{w: w, done: make(chan struct{})}
	c.wg.Add(1)
	go c.loop(target, onChange)
	return c, nil
}

type watcher struct {
	w    *fsnotify.Watcher
	done chan struct{}
	wg   sync.WaitGroup
	once sync.Once
}

func (c *watcher) loop(target string, onChange func()) {
	defer c.wg.Done()

	// timer fires once a debounce window elapses with no further events.
	var timer *time.Timer
	var timerC <-chan time.Time
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()

	for {
		select {
		case <-c.done:
			return

		case ev, ok := <-c.w.Events:
			if !ok {
				return
			}
			// Only react to events touching our file. Create/Write/Rename all
			// signal a save; Remove is ignored since the replacement Create
			// (or a follow-up Write) will arrive next.
			if filepath.Base(ev.Name) != target {
				continue
			}
			if !ev.Op.Has(fsnotify.Write) && !ev.Op.Has(fsnotify.Create) && !ev.Op.Has(fsnotify.Rename) {
				continue
			}
			if timer == nil {
				timer = time.NewTimer(watchDebounce)
				timerC = timer.C
			} else {
				timer.Reset(watchDebounce)
			}

		case <-timerC:
			onChange()

		case _, ok := <-c.w.Errors:
			if !ok {
				return
			}
			// Transient watch errors are non-fatal; keep watching.
		}
	}
}

// Close stops the watcher; it is safe to call more than once.
func (c *watcher) Close() error {
	var err error
	c.once.Do(func() {
		close(c.done)
		err = c.w.Close()
	})
	c.wg.Wait()
	return err
}
