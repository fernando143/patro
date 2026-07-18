package watcher

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fernando143/patro/internal/config"
)

func TestWaitUntilStableStaticFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "static.mkv")
	if err := os.WriteFile(path, []byte("fake video"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !WaitUntilStable(path, 3, 10*time.Millisecond) {
		t.Error("WaitUntilStable() = false for a static file, want true")
	}
}

func TestWaitUntilStableGrowingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "growing.mkv")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	result := make(chan bool, 1)
	go func() { result <- WaitUntilStable(path, 2, 50*time.Millisecond) }()

	// Grow the file steadily (every 5 ms, far below the 50 ms probe
	// interval): the probe must not stabilize while growth continues.
	for range 80 {
		f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.WriteString("more"); err != nil {
			f.Close()
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
		select {
		case got := <-result:
			t.Fatalf("WaitUntilStable() = %v while the file was still growing, want no result", got)
		case <-time.After(5 * time.Millisecond):
		}
	}

	// Once growth stops, the file stabilizes.
	select {
	case got := <-result:
		if !got {
			t.Error("WaitUntilStable() = false after growth stopped, want true")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("WaitUntilStable() did not return after growth stopped")
	}
}

func TestWaitUntilStableDisappearingFile(t *testing.T) {
	dir := t.TempDir()

	// Already gone before the initial stat.
	missing := filepath.Join(dir, "missing.mkv")
	if WaitUntilStable(missing, 2, 10*time.Millisecond) {
		t.Error("WaitUntilStable() = true for a missing file, want false")
	}

	// Removed mid-wait.
	path := filepath.Join(dir, "gone.mkv")
	if err := os.WriteFile(path, []byte("fake video"), 0o644); err != nil {
		t.Fatal(err)
	}
	result := make(chan bool, 1)
	go func() { result <- WaitUntilStable(path, 5, 20*time.Millisecond) }()
	time.Sleep(30 * time.Millisecond) // let the initial stat and one probe pass
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-result:
		if got {
			t.Error("WaitUntilStable() = true for a file removed mid-wait, want false")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("WaitUntilStable() did not return after the file was removed")
	}
}

// newTestWatcher returns a Watcher on inbox with fast stability settings and
// a processFn that reports through the returned channel.
func newTestWatcher(inbox string) (*Watcher, chan string) {
	cfg := &config.Config{
		Inbox:           inbox,
		VideoExtensions: []string{".mkv", ".mp4"},
		StabilityChecks: 1,
	}
	processed := make(chan string, 10)
	w := New(cfg, func(path string) { processed <- path })
	w.interval = 20 * time.Millisecond // Config only stores whole seconds.
	return w, processed
}

func TestWatcherProcessesNewVideos(t *testing.T) {
	inbox := t.TempDir()
	w, processed := newTestWatcher(inbox)

	ctx, cancel := context.WithCancel(context.Background())
	runErr := make(chan error, 1)
	go func() { runErr <- w.Run(ctx) }()
	defer func() {
		cancel()
		select {
		case err := <-runErr:
			if err != nil {
				t.Errorf("Run() = %v, want nil", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("Run() did not return after cancel")
		}
	}()

	// A new video file is processed exactly once.
	video := filepath.Join(inbox, "meeting.mkv")
	if err := os.WriteFile(video, []byte("fake video"), 0o644); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-processed:
		if got != video {
			t.Errorf("processed path = %q, want %q", got, video)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("processFn was not called for the .mkv file")
	}

	// A non-video file must not be processed; the window also proves the
	// video was not delivered twice.
	if err := os.WriteFile(filepath.Join(inbox, "notes.txt"), []byte("notes"), 0o644); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-processed:
		t.Fatalf("processFn called unexpectedly for %q", got)
	case <-time.After(500 * time.Millisecond):
	}
}

func TestWatcherScansExistingVideos(t *testing.T) {
	inbox := t.TempDir()
	video := filepath.Join(inbox, "already-here.mkv")
	if err := os.WriteFile(video, []byte("fake video"), 0o644); err != nil {
		t.Fatal(err)
	}

	w, processed := newTestWatcher(inbox)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = w.Run(ctx) }()

	select {
	case got := <-processed:
		if got != video {
			t.Errorf("processed path = %q, want %q", got, video)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("processFn was not called for the pre-existing .mkv file")
	}
}

func TestWatcherRecoversProcessPanic(t *testing.T) {
	inbox := t.TempDir()
	cfg := &config.Config{
		Inbox:           inbox,
		VideoExtensions: []string{".mkv"},
		StabilityChecks: 1,
	}

	firstCall := make(chan struct{})
	processed := make(chan string, 1)
	w := New(cfg, func(path string) {
		select {
		case <-firstCall:
			// Second call: delivered normally.
			processed <- path
		default:
			close(firstCall)
			panic("boom")
		}
	})
	w.interval = 20 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = w.Run(ctx) }()

	first := filepath.Join(inbox, "panics.mkv")
	if err := os.WriteFile(first, []byte("boom"), 0o644); err != nil {
		t.Fatal(err)
	}
	select {
	case <-firstCall:
	case <-time.After(10 * time.Second):
		t.Fatal("processFn was not called for the first file")
	}

	second := filepath.Join(inbox, "works.mkv")
	if err := os.WriteFile(second, []byte("fine"), 0o644); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-processed:
		if got != second {
			t.Errorf("processed path = %q, want %q", got, second)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("worker did not survive the panic in processFn")
	}
}

func TestRunSetupFailure(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("not a dir"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Inbox:           filepath.Join(blocker, "inbox"),
		VideoExtensions: []string{".mkv"},
	}
	w := New(cfg, func(string) {})
	if err := w.Run(context.Background()); err == nil {
		t.Error("Run() = nil with an uncreatable inbox, want a setup error")
	}
}
