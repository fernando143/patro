package tui

import (
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/fernando143/patro/internal/config"
	"github.com/fernando143/patro/internal/status"
)

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// flattenWords collapses all whitespace (including line breaks introduced by
// lipgloss word-wrap at narrower panel widths) to single spaces, so a
// multi-word phrase can be found with strings.Contains regardless of exactly
// where it wraps.
func flattenWords(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func sampleModel(t *testing.T, w, h int) model {
	t.Helper()
	cfg := &config.Config{
		Inbox:           "/home/user/Videos/obs",
		Library:         "/home/user/knowledge",
		AnalyzerBackend: "kimi",
		Dir:             "/home/user",
	}
	m := model{cfg: cfg, followLog: true, spinner: spinner.New()}
	nm, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	m = nm.(model)

	snap := &status.Snapshot{
		StartedAt:        time.Now().Add(-90 * time.Second),
		Queue:            []string{"team-sync.mkv", "1on1.mkv"},
		Current:          &status.Job{File: "roadmap-review.mkv", Stage: status.StageAnalyzing, StartedAt: time.Now().Add(-12 * time.Second)},
		ProcessedSession: 3,
		FailedSession:    1,
		Failures:         []status.Failure{{File: "corrupt.mkv", Reason: "transcription failed: 401"}},
		Recent:           []status.Recent{{Title: "Q3 Roadmap Review"}, {Title: "Budget sync"}},
	}
	nm, _ = m.Update(dataMsg(dashboardData{
		snap:           snap,
		processedTotal: 42,
		service:        serviceActive,
		log: []logLine{
			{Time: "19:17:56", Level: "INFO", Message: "Watching /home/user/Videos/obs ..."},
			{Time: "19:18:01", Level: "INFO", Message: "Processing roadmap-review.mkv ..."},
			{Time: "19:18:30", Level: "WARNING", Message: "File vanished before stabilizing: temp.mkv"},
			{Time: "19:19:02", Level: "ERROR", Message: "Failed to process corrupt.mkv: 401"},
		},
	}))
	return nm.(model)
}

func TestViewWidthWithinBounds(t *testing.T) {
	for _, size := range sizeMatrix {
		m := sampleModel(t, size.w, size.h)
		assertNoOverflow(t, "dashboard/live", m.View(), size.w, size.h)

		stale := m.data
		stale.statusStale = true
		nm, _ := m.Update(dataMsg(stale))
		assertNoOverflow(t, "dashboard/stale", nm.(model).View(), size.w, size.h)

		missing := m.data
		missing.snap = nil
		missing.statusMissing = true
		missing.service = serviceActive
		missing.inboxBacklog = 3
		nm2, _ := m.Update(dataMsg(missing))
		assertNoOverflow(t, "dashboard/missing-status", nm2.(model).View(), size.w, size.h)
	}

	// Dump an ANSI-stripped preview at a representative size for manual inspection.
	if dir := os.Getenv("TUI_PREVIEW_DIR"); dir != "" {
		m := sampleModel(t, 100, 40)
		_ = os.WriteFile(dir+"/dashboard-preview.txt", []byte(ansiRe.ReplaceAllString(m.View(), "")), 0o644)
	}
}

func TestViewDoesNotPanicSmallTerminal(t *testing.T) {
	m := sampleModel(t, 60, 24)
	_ = m.View()
}

func TestViewMissingStatusAlert(t *testing.T) {
	m := sampleModel(t, 100, 40)
	d := m.data
	d.snap = nil
	d.statusMissing = true
	d.service = serviceActive
	d.inboxBacklog = 3
	nm, _ := m.Update(dataMsg(d))
	m = nm.(model)

	out := ansiRe.ReplaceAllString(m.View(), "")
	flat := flattenWords(out)
	if !strings.Contains(flat, "no publica estado") {
		t.Error("missing-status alert not rendered for an active service")
	}
	if !strings.Contains(out, "en inbox") {
		t.Error("queue card did not fall back to the inbox backlog")
	}
	// The EN CURSO card now shares its column with MANTENIMIENTO
	// (renderJobAndMaintenance), so this phrase may wrap at narrower widths.
	if !strings.Contains(flat, "estado en vivo no disponible") {
		t.Error("in-flight panel does not flag the missing live status")
	}
}

func TestViewStaleStatusAlert(t *testing.T) {
	m := sampleModel(t, 100, 40)
	d := m.data
	d.statusStale = true
	d.snap.Current = nil
	d.snap.Queue = nil
	nm, _ := m.Update(dataMsg(d))
	m = nm.(model)

	out := ansiRe.ReplaceAllString(m.View(), "")
	flat := flattenWords(out)
	if !strings.Contains(flat, "sesión anterior") {
		t.Error("stale-status alert not rendered")
	}
	if !strings.Contains(out, "uptime —") {
		t.Error("uptime not blanked for a stale snapshot")
	}
	if !strings.Contains(flat, "estado en vivo no disponible") {
		t.Error("in-flight panel did not switch to the no-live-status message")
	}
	if strings.Contains(flat, "etapa analyzing") {
		t.Error("phantom in-flight job rendered from a stale snapshot")
	}
}

// resizeLog must early-return when the model is not ready — its viewport is
// the zero value until the first WindowSizeMsg, and testRoot's dash (used by
// TestRootRoutesDashboardMsgsWhileAway) is exactly that: a model with no
// size at all. Computing the log's dimensions before that point would
// operate on data that is not there yet.
func TestResizeLogEarlyReturnsWhenNotReady(t *testing.T) {
	m := model{cfg: &config.Config{AnalyzerBackend: "kimi"}}
	if m.ready {
		t.Fatal("test setup: zero-value model must not be ready")
	}
	m.resizeLog()
	if m.log.Width != 0 || m.log.Height != 0 {
		t.Errorf("resizeLog mutated an unready model's viewport: width=%d height=%d, want both 0",
			m.log.Width, m.log.Height)
	}
}

func TestViewNotReadyShowsLoadingPlaceholder(t *testing.T) {
	m := model{cfg: &config.Config{AnalyzerBackend: "kimi"}}
	if got := m.View(); got != "cargando dashboard…" {
		t.Errorf("View() = %q, want the loading placeholder before the first WindowSizeMsg", got)
	}
}

func TestSynthwaveHuhThemeBuilds(t *testing.T) {
	theme := SynthwaveHuhTheme()
	if theme == nil {
		t.Fatal("SynthwaveHuhTheme returned nil")
	}
	// Focused title should carry our neon magenta foreground.
	if got := theme.Focused.Title.GetForeground(); got != colorMagenta {
		t.Errorf("focused title foreground = %v, want %v", got, colorMagenta)
	}
}
