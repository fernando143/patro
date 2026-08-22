// Package ledger records what reconciliation decided.
//
// Every merge and every safe-fail is appended here with its score and
// thresholds, so a human can audit why two topics were joined — or why one
// was left standing alone — long after the run. The file is derived and
// deletable: losing it costs the audit trail, never the knowledge library.
//
// This lived inside internal/domain/knowledge, which made the knowledge domain
// responsible for its own JSON file format on disk.
package ledger

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Entry is one record in the derived, deletable reconciliation
// ledger. It mirrors the markdown annotation written alongside every merge,
// plus flagged safe-fail outcomes, so a later TUI can surface "work
// pending" (design D4/D8).
type Entry struct {
	Slug         string    `json:"slug"`
	Name         string    `json:"name"`
	ProposedSlug string    `json:"proposed_slug"`
	Score        float64   `json:"score"`
	Merged       bool      `json:"merged"`
	Flagged      bool      `json:"flagged"`
	Timestamp    time.Time `json:"timestamp"`
}

type file struct {
	Entries []Entry `json:"entries"`
}

// ledgerMu serializes ledger read-modify-write cycles across reconcilers
// sharing the same path within one process.
var ledgerMu sync.Mutex

// writeLedger appends one entry to r.LedgerPath, best-effort: a failure is
// logged, never propagated, since losing the audit trail must not lose the
// meeting itself.

// ReadLedger reads every entry recorded in path, the derived, deletable
// ledger written by writeLedger/appendLedger (design D4). A missing file
// returns an empty, non-nil slice and a nil error — nothing has ever been
// merged or flagged is not an error condition, matching appendLedger's own
// create-on-first-write semantics. It is the reader half of Unit 7's
// "patro reconcile", which re-attempts reconciliation for every flagged
// entry (Library.ReconcileFlagged).
func Read(path string) ([]Entry, error) {
	ledgerMu.Lock()
	defer ledgerMu.Unlock()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []Entry{}, nil
		}
		return nil, err
	}
	var lf file
	if err := json.Unmarshal(data, &lf); err != nil {
		return nil, fmt.Errorf("ledger: parsing ledger %s: %w", path, err)
	}
	if lf.Entries == nil {
		lf.Entries = []Entry{}
	}
	return lf.Entries, nil
}

// CountFlagged returns the number of distinct slugs whose most recent ledger
// record is flagged, i.e. the number of topics genuinely awaiting
// reconciliation right now. It dedupes to the latest record per slug — an
// older flagged record superseded by a later merge or reflag does not count
// — matching ReconcileFlagged's own dedupe, so callers (patro reconcile's
// Maintenance.Total, the TUI dashboard's flagged-count card) agree with what
// a reconcile pass will actually process.
func CountFlagged(entries []Entry) int {
	latest := map[string]bool{}
	for _, e := range entries {
		latest[e.Slug] = e.Flagged
	}
	total := 0
	for _, flagged := range latest {
		if flagged {
			total++
		}
	}
	return total
}

// appendLedger reads path (if present), appends entry, and writes the
// result back atomically (temp file + rename), mirroring internal/adapter/vectors'
// flush pattern.
func Append(path string, entry Entry) error {
	ledgerMu.Lock()
	defer ledgerMu.Unlock()

	var lf file
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &lf) // best-effort; a corrupt ledger starts fresh
	} else if !os.IsNotExist(err) {
		return err
	}
	lf.Entries = append(lf.Entries, entry)

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".reconciliation-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	enc := json.NewEncoder(tmp)
	enc.SetIndent("", "  ")
	if err := enc.Encode(lf); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}
