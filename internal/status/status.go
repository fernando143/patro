// Package status publishes the live processing state of a running patro
// service so an external dashboard can observe it.
//
// The serve command owns a *Tracker and calls its methods as files move
// through the queue and pipeline; each mutation is flushed atomically to
// <stateDir>/status.json. The dashboard reads that file with Read. Every
// Tracker method is safe on a nil receiver, so the one-shot process command
// (which has no tracker) can share the same code paths by passing nil.
package status

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// FileName is the status file written under the state directory.
const FileName = "status.json"

// maxFailures and maxRecent cap the slices kept in the snapshot so the file
// stays small regardless of how long the service runs.
const (
	maxFailures = 20
	maxRecent   = 10
)

// Stage names a step of the processing pipeline for the in-flight file.
type Stage string

const (
	StageTranscribing Stage = "transcribing"
	StageAnalyzing    Stage = "analyzing"
	StageWriting      Stage = "writing"
)

// Job is the file currently being processed and its pipeline stage.
type Job struct {
	File      string    `json:"file"`
	Stage     Stage     `json:"stage"`
	StartedAt time.Time `json:"started_at"`
}

// Failure records a file that failed processing and why.
type Failure struct {
	File   string    `json:"file"`
	Reason string    `json:"reason"`
	At     time.Time `json:"at"`
}

// Recent records a successfully processed meeting.
type Recent struct {
	File  string    `json:"file"`
	Title string    `json:"title"`
	At    time.Time `json:"at"`
}

// MaintenancePhase names a step of background maintenance work (index
// rebuild or flagged-topic reconciliation — design D10/D12), distinct from
// Stage, which names a step of the in-flight video pipeline.
type MaintenancePhase string

const (
	PhaseRebuildingIndex MaintenancePhase = "rebuilding-index"
	PhaseReconciling     MaintenancePhase = "reconciling"
)

// Maintenance reports the progress of a background maintenance run. It is a
// sibling of Current, not a reuse of Job/Stage: a rebuild has no File, is
// never queued, and — per design D10 — can run at the same time as an
// in-flight video job, so the two must be representable simultaneously
// rather than sharing one slot.
type Maintenance struct {
	Phase     MaintenancePhase `json:"phase"`
	Done      int              `json:"done"`
	Total     int              `json:"total"`
	StartedAt time.Time        `json:"started_at"`
}

// Snapshot is the serializable live state written to status.json.
type Snapshot struct {
	PID              int          `json:"pid"`
	StartedAt        time.Time    `json:"started_at"`
	UpdatedAt        time.Time    `json:"updated_at"`
	Queue            []string     `json:"queue"`
	Current          *Job         `json:"current"`
	ProcessedSession int          `json:"processed_session"`
	FailedSession    int          `json:"failed_session"`
	Failures         []Failure    `json:"failures"`
	Recent           []Recent     `json:"recent"`
	Maintenance      *Maintenance `json:"maintenance,omitempty"`
}

// maintFlushInterval and maintFlushPercentStep gate MaintenanceProgress's
// flush: a write happens once at least one of the two thresholds is
// crossed, whichever comes first (design D12).
const (
	maintFlushInterval    = 250 * time.Millisecond
	maintFlushPercentStep = 0.01
)

// Tracker owns the live snapshot and persists every change to path.
type Tracker struct {
	mu   sync.Mutex
	path string
	snap Snapshot
	now  func() time.Time // swappable in tests

	// maintFlushAt/maintFlushDone track the last flushed MaintenanceProgress
	// state, so successive calls can decide whether the throttle thresholds
	// have been crossed since that write.
	maintFlushAt   time.Time
	maintFlushDone int

	// flushCount counts flushLocked invocations. It exists purely so tests
	// can assert the throttle actually reduces disk writes; production code
	// never reads it.
	flushCount int
}

// NewTracker creates a tracker writing to <stateDir>/status.json and flushes
// the initial (empty, started-now) snapshot. A flush error is returned but
// the tracker is still usable.
func NewTracker(stateDir string) (*Tracker, error) {
	t := &Tracker{
		path: filepath.Join(stateDir, FileName),
		now:  time.Now,
	}
	t.snap.PID = os.Getpid()
	t.snap.StartedAt = t.now()
	t.snap.Queue = []string{}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t, t.flushLocked()
}

// Enqueue records that a stable file is waiting to be processed.
func (t *Tracker) Enqueue(file string) {
	if t == nil {
		return
	}
	base := filepath.Base(file)
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, q := range t.snap.Queue {
		if q == base {
			return
		}
	}
	t.snap.Queue = append(t.snap.Queue, base)
	_ = t.flushLocked()
}

// Dequeue removes file from the waiting queue. The worker calls it when it
// pulls a file, before deciding whether to process or skip it.
func (t *Tracker) Dequeue(file string) {
	if t == nil {
		return
	}
	base := filepath.Base(file)
	t.mu.Lock()
	defer t.mu.Unlock()
	t.snap.Queue = removeString(t.snap.Queue, base)
	_ = t.flushLocked()
}

// Start marks file as the in-flight job and sets the initial pipeline stage.
func (t *Tracker) Start(file string) {
	if t == nil {
		return
	}
	base := filepath.Base(file)
	t.mu.Lock()
	defer t.mu.Unlock()
	t.snap.Current = &Job{File: base, Stage: StageTranscribing, StartedAt: t.now()}
	_ = t.flushLocked()
}

// Stage updates the in-flight job's pipeline stage. It is a no-op when no
// job is in flight.
func (t *Tracker) Stage(s Stage) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.snap.Current != nil {
		t.snap.Current.Stage = s
		_ = t.flushLocked()
	}
}

// Done clears the in-flight job and records a successful meeting.
func (t *Tracker) Done(file, title string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.snap.Current = nil
	t.snap.ProcessedSession++
	t.snap.Recent = append([]Recent{{
		File:  filepath.Base(file),
		Title: title,
		At:    t.now(),
	}}, t.snap.Recent...)
	if len(t.snap.Recent) > maxRecent {
		t.snap.Recent = t.snap.Recent[:maxRecent]
	}
	_ = t.flushLocked()
}

// Fail clears the in-flight job and records a failure with its reason.
func (t *Tracker) Fail(file, reason string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.snap.Current = nil
	t.snap.FailedSession++
	t.snap.Failures = append([]Failure{{
		File:   filepath.Base(file),
		Reason: reason,
		At:     t.now(),
	}}, t.snap.Failures...)
	if len(t.snap.Failures) > maxFailures {
		t.snap.Failures = t.snap.Failures[:maxFailures]
	}
	_ = t.flushLocked()
}

// MaintenanceStart begins a new maintenance run (replacing any prior one)
// and flushes unconditionally, matching Start/Done's own unconditional
// flush (design D12).
func (t *Tracker) MaintenanceStart(phase MaintenancePhase, total int) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	t.snap.Maintenance = &Maintenance{
		Phase:     phase,
		Total:     total,
		StartedAt: now,
	}
	t.maintFlushAt = now
	t.maintFlushDone = 0
	_ = t.flushLocked()
}

// MaintenanceProgress updates the in-flight maintenance run's done count. It
// is a no-op when no run is active (MaintenanceStart was never called, or
// MaintenanceDone already cleared it).
//
// The write is throttled (design D12): a flush only happens once the
// done/total ratio has moved by at least 1 percentage point since the last
// flush, or at least 250ms have elapsed, whichever threshold is reached
// first. The in-memory state (and any subsequent Read once a flush does
// happen) always reflects the latest call regardless of throttling — only
// the disk write is skipped, never the update itself.
func (t *Tracker) MaintenanceProgress(done int) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.snap.Maintenance == nil {
		return
	}
	t.snap.Maintenance.Done = done

	now := t.now()
	shouldFlush := now.Sub(t.maintFlushAt) >= maintFlushInterval
	if !shouldFlush {
		if total := t.snap.Maintenance.Total; total > 0 {
			prevPct := float64(t.maintFlushDone) / float64(total)
			curPct := float64(done) / float64(total)
			diff := curPct - prevPct
			if diff < 0 {
				diff = -diff
			}
			shouldFlush = diff >= maintFlushPercentStep
		}
	}
	if !shouldFlush {
		return
	}

	t.maintFlushAt = now
	t.maintFlushDone = done
	_ = t.flushLocked()
}

// MaintenanceDone clears the maintenance state and flushes unconditionally,
// matching Done/Fail's own unconditional flush.
func (t *Tracker) MaintenanceDone() {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.snap.Maintenance = nil
	_ = t.flushLocked()
}

// flushLocked writes the snapshot atomically (temp file + rename). The
// caller must hold t.mu.
func (t *Tracker) flushLocked() error {
	t.flushCount++
	t.snap.UpdatedAt = t.now()
	if err := os.MkdirAll(filepath.Dir(t.path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(t.path), ".status-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	enc := json.NewEncoder(tmp)
	enc.SetIndent("", "  ")
	if err := enc.Encode(t.snap); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, t.path); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

// Read loads the status snapshot from <stateDir>/status.json. A missing file
// returns (nil, nil) so callers can distinguish "service never ran" from a
// real error.
func Read(stateDir string) (*Snapshot, error) {
	raw, err := os.ReadFile(filepath.Join(stateDir, FileName))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var snap Snapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		return nil, err
	}
	return &snap, nil
}

// removeString returns s without the first occurrence of v.
func removeString(s []string, v string) []string {
	for i, x := range s {
		if x == v {
			return append(s[:i:i], s[i+1:]...)
		}
	}
	return s
}
