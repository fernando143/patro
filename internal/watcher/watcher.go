// Package watcher watches the inbox folder for new video recordings.
//
// OBS writes recordings progressively, so a file is only enqueued once its
// size stays identical across Config.StabilityChecks consecutive probes
// spaced Config.StabilityIntervalSeconds apart. Stable files land in a queue
// consumed by a single worker goroutine that runs processFn sequentially.
// On startup the inbox is scanned for existing videos, which take the same
// path.
package watcher

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/fernando143/patro/internal/config"
	"github.com/fernando143/patro/internal/logging"
	"github.com/fernando143/patro/internal/status"
)

// WaitUntilStable reports whether the file size stayed identical across
// checks consecutive probes spaced interval apart. checks identical
// consecutive reads (each interval after the previous one) mean OBS finished
// writing. It returns false if the file disappears mid-wait.
func WaitUntilStable(path string, checks int, interval time.Duration) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	lastSize := info.Size()
	stable := 0
	for stable < checks {
		time.Sleep(interval)
		info, err := os.Stat(path)
		if err != nil {
			return false
		}
		if size := info.Size(); size == lastSize {
			stable++
		} else {
			stable = 0
			lastSize = size
		}
	}
	return true
}

// Watcher watches the inbox directory and feeds stable video files to
// processFn one at a time.
type Watcher struct {
	cfg       *config.Config
	processFn func(path string)

	// Tracker publishes queue/processing state for the dashboard. It is
	// optional and safe to leave nil (all its methods are nil-safe).
	Tracker *status.Tracker

	// interval overrides the stability-probe interval when > 0; Config only
	// stores whole seconds, which is too coarse for tests.
	interval time.Duration

	queue      chan string
	pendingMu  sync.Mutex
	pending    map[string]struct{}
	submitters sync.WaitGroup
	workerWG   sync.WaitGroup
}

// New returns a Watcher that calls processFn for each stable video file
// appearing in cfg.Inbox.
func New(cfg *config.Config, processFn func(path string)) *Watcher {
	return &Watcher{
		cfg:       cfg,
		processFn: processFn,
		queue:     make(chan string),
		pending:   map[string]struct{}{},
	}
}

// Run watches cfg.Inbox until ctx is cancelled.
//
// The inbox is created if missing. Per-file failures (vanished files,
// processFn errors or panics) are logged, never returned; only setup
// failures produce a non-nil error. On cancellation the fsnotify watch is
// removed and closed, then Run waits for in-flight stability checks and
// queued files to finish before returning nil, so a blocked processFn
// delays shutdown.
func (w *Watcher) Run(ctx context.Context) error {
	if err := os.MkdirAll(w.cfg.Inbox, 0o755); err != nil {
		return fmt.Errorf("watcher: cannot create inbox %s: %w", w.cfg.Inbox, err)
	}
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("watcher: %w", err)
	}
	if err := fsw.Add(w.cfg.Inbox); err != nil {
		fsw.Close()
		return fmt.Errorf("watcher: cannot watch %s: %w", w.cfg.Inbox, err)
	}

	w.workerWG.Add(1)
	go w.worker()

	logging.Infof("Watching %s for %s files ...", w.cfg.Inbox, strings.Join(w.cfg.VideoExtensions, ", "))
	w.scanExisting()

loop:
	for {
		select {
		case <-ctx.Done():
			break loop
		case event, ok := <-fsw.Events:
			if !ok {
				break loop
			}
			w.handleEvent(event)
		case err, ok := <-fsw.Errors:
			if !ok {
				break loop
			}
			logging.Errorf("Filesystem watcher error: %v", err)
		}
	}

	logging.Infof("Shutting down ...")
	fsw.Close()
	w.stop()
	return nil
}

// stop waits for in-flight stability checks (the worker keeps draining the
// queue meanwhile), then closes the queue and waits for the worker to finish
// the remaining files.
func (w *Watcher) stop() {
	w.submitters.Wait()
	close(w.queue)
	w.workerWG.Wait()
}

// handleEvent reacts to a new inbox entry. A file moved INTO the directory
// surfaces as Create, so Create alone covers Python's on_created + on_moved.
func (w *Watcher) handleEvent(event fsnotify.Event) {
	if !event.Has(fsnotify.Create) {
		return
	}
	if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
		return
	}
	if w.cfg.IsVideo(event.Name) {
		w.submitAsync(event.Name)
	}
}

// scanExisting submits video files already sitting in the inbox. os.ReadDir
// returns entries sorted by name, matching Python's sorted() scan.
func (w *Watcher) scanExisting() {
	entries, err := os.ReadDir(w.cfg.Inbox)
	if err != nil {
		logging.Errorf("Cannot scan inbox %s: %v", w.cfg.Inbox, err)
		return
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		path := filepath.Join(w.cfg.Inbox, entry.Name())
		if w.cfg.IsVideo(path) {
			w.submitAsync(path)
		}
	}
}

// submitAsync runs the stability check in its own goroutine so fsnotify
// events never block; a path with a check already in flight is dropped.
func (w *Watcher) submitAsync(path string) {
	w.pendingMu.Lock()
	if _, dup := w.pending[path]; dup {
		w.pendingMu.Unlock()
		return
	}
	w.pending[path] = struct{}{}
	w.pendingMu.Unlock()

	w.submitters.Add(1)
	go func() {
		defer w.submitters.Done()
		defer func() {
			w.pendingMu.Lock()
			delete(w.pending, path)
			w.pendingMu.Unlock()
		}()

		name := filepath.Base(path)
		logging.Infof("New recording detected: %s (waiting for it to finish writing)", name)
		if WaitUntilStable(path, w.cfg.StabilityChecks, w.stabilityInterval()) {
			logging.Infof("File stable, enqueueing: %s", name)
			w.Tracker.Enqueue(path)
			w.queue <- path
		} else {
			logging.Warnf("File vanished before stabilizing: %s", name)
		}
	}()
}

// worker consumes stable files sequentially, recovering a panic per file
// (parity with Python's try/except around process_fn).
func (w *Watcher) worker() {
	defer w.workerWG.Done()
	for path := range w.queue {
		w.Tracker.Dequeue(path)
		func() {
			defer func() {
				if r := recover(); r != nil {
					logging.Errorf("Failed to process %s: %v", path, r)
					w.Tracker.Fail(path, fmt.Sprintf("panic: %v", r))
				}
			}()
			w.processFn(path)
		}()
	}
}

// stabilityInterval returns the probe interval: the override when set,
// otherwise the configured whole seconds.
func (w *Watcher) stabilityInterval() time.Duration {
	if w.interval > 0 {
		return w.interval
	}
	return time.Duration(w.cfg.StabilityIntervalSeconds) * time.Second
}
