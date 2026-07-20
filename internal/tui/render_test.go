package tui

import (
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/fernando143/patro/internal/config"
	"github.com/fernando143/patro/internal/status"
)

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

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
	const w = 100
	m := sampleModel(t, w, 40)
	out := m.View()
	if strings.TrimSpace(out) == "" {
		t.Fatal("empty view")
	}
	for i, line := range strings.Split(out, "\n") {
		if got := lipgloss.Width(line); got > w {
			t.Errorf("line %d width %d exceeds %d: %q", i, got, w, ansiRe.ReplaceAllString(line, ""))
		}
	}

	// Dump an ANSI-stripped preview for manual inspection.
	if dir := os.Getenv("TUI_PREVIEW_DIR"); dir != "" {
		_ = os.WriteFile(dir+"/dashboard-preview.txt", []byte(ansiRe.ReplaceAllString(out, "")), 0o644)
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
	if !strings.Contains(out, "no publica estado") {
		t.Error("missing-status alert not rendered for an active service")
	}
	if !strings.Contains(out, "en inbox") {
		t.Error("queue card did not fall back to the inbox backlog")
	}
	if !strings.Contains(out, "estado en vivo no disponible") {
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
	if !strings.Contains(out, "sesión anterior") {
		t.Error("stale-status alert not rendered")
	}
	if !strings.Contains(out, "uptime —") {
		t.Error("uptime not blanked for a stale snapshot")
	}
	if !strings.Contains(out, "estado en vivo no disponible") {
		t.Error("in-flight panel did not switch to the no-live-status message")
	}
	if strings.Contains(out, "etapa analyzing") {
		t.Error("phantom in-flight job rendered from a stale snapshot")
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
