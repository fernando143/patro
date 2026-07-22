package tui

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/fernando143/patro/internal/status"
)

// View renders the whole dashboard.
func (m model) View() string {
	if !m.ready || m.width < 20 {
		return "cargando dashboard…"
	}
	sections := []string{
		m.renderHeader(),
		m.renderStats(),
		m.renderRow1(),
		m.renderRow2(),
		m.renderLog(),
		m.renderHelp(),
	}
	return lipgloss.JoinVertical(lipgloss.Left, sections...)
}

// renderHeader draws the neon banner with service health and uptime.
func (m model) renderHeader() string {
	inner := m.width - 2
	left := styleBanner.Render(bannerText)

	service := m.serviceLabel()
	uptime := styleDim.Render("uptime ") + styleAccent.Render(m.uptime())
	right := service + styleDim.Render("  ·  ") + uptime

	// Text area is inner-2 because of the horizontal padding.
	gap := (inner - 2) - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}
	line := left + strings.Repeat(" ", gap) + right
	sub := styleSubtitle.Render("live status dashboard  ▓▒░")
	return panelStyle.BorderForeground(colorMagenta).Width(inner).Render(line + "\n" + sub)
}

// renderStats draws the four counter cards.
func (m model) renderStats() string {
	total := m.width / 4
	inner := total - 2
	if inner < 6 {
		inner = 6
	}

	processing := "—"
	if job := m.currentJob(); job != nil {
		processing = string(job.Stage)
	}
	queue := 0
	processedSession := 0
	failed := 0
	if s := m.data.snap; s != nil {
		queue = len(s.Queue)
		processedSession = s.ProcessedSession
		failed = s.FailedSession
	}
	queueValue, queueSub := fmt.Sprint(queue), "esperando"
	if !m.hasLiveStatus() {
		// No live snapshot: derive the pending count from the inbox itself so
		// the card still moves in real time.
		queueValue, queueSub = fmt.Sprint(m.data.inboxBacklog), "en inbox"
	}

	cards := []string{
		statCard("PROCESANDO", processing, m.spinnerOrDash(), colorCyan, inner),
		statCard("EN COLA", queueValue, queueSub, colorPurple, inner),
		statCard("PROCESADOS", fmt.Sprint(m.data.processedTotal), fmt.Sprintf("%d esta sesión", processedSession), colorGreen, inner),
		statCard("FALLADOS", fmt.Sprint(failed), "esta sesión", colorRed, inner),
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, cards...)
}

// statCard renders a single counter card.
func statCard(title, value, sub string, color lipgloss.Color, inner int) string {
	body := statValue.Foreground(color).Render(value) + "\n" + styleDim.Render(sub)
	return panelBox(title, color, inner, body)
}

// renderRow1 draws the in-flight job panel and the config/alerts panel.
func (m model) renderRow1() string {
	leftTotal := m.width / 2
	rightTotal := m.width - leftTotal
	leftInner := leftTotal - 2
	rightInner := rightTotal - 2

	// In-flight job.
	var jobBody string
	if job := m.currentJob(); job != nil {
		elapsed := formatDuration(time.Since(job.StartedAt))
		jobBody = m.spinner.View() + " " + styleAccent.Render(job.File) + "\n" +
			styleDim.Render("etapa ") + lipgloss.NewStyle().Foreground(colorCyan).Render(string(job.Stage)) +
			styleDim.Render("  ·  "+elapsed)
	} else if !m.hasLiveStatus() {
		jobBody = styleDim.Render("estado en vivo no disponible")
	} else {
		jobBody = styleDim.Render("en reposo — sin videos en proceso")
	}
	left := panelBox("EN CURSO", colorSunset, leftInner, padLines(jobBody, 2))

	right := panelBox("CONFIGURACIÓN", colorCyan, rightInner, padLines(m.configBody(), 2))
	return lipgloss.JoinHorizontal(lipgloss.Top, left, right)
}

// configBody renders inbox/library/backend and any alerts.
func (m model) configBody() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s\n", styleDim.Render("inbox  "), truncate(m.cfg.Inbox, 34))
	fmt.Fprintf(&b, "%s %s\n", styleDim.Render("library"), truncate(m.cfg.Library, 34))
	fmt.Fprintf(&b, "%s %s", styleDim.Render("backend"), lipgloss.NewStyle().Foreground(colorPurple).Render(m.cfg.AnalyzerBackend))

	var alerts []string
	if !m.apiKeySet {
		alerts = append(alerts, styleAlert.Render("⚠ ASSEMBLYAI_API_KEY no definida"))
	}
	if !dirExists(m.cfg.Inbox) {
		alerts = append(alerts, styleAlert.Render("⚠ inbox no existe"))
	}
	switch {
	case m.data.err != nil:
		alerts = append(alerts, styleAlert.Render("⚠ status.json ilegible: "+m.data.err.Error()))
	case m.data.statusMissing && m.data.service == serviceActive:
		alerts = append(alerts,
			styleAlert.Render("⚠ el servicio corre pero no publica estado"),
			styleAlert.Render("  (binario antiguo: reinicia el servicio)"))
	case m.data.statusMissing:
		alerts = append(alerts, styleAlert.Render("⚠ sin estado en vivo: inicia patro serve"))
	case m.data.statusStale:
		alerts = append(alerts, styleAlert.Render("⚠ estado de una sesión anterior de serve"))
	}
	if len(alerts) > 0 {
		b.WriteString("\n" + strings.Join(alerts, "\n"))
	}
	return b.String()
}

// renderRow2 draws the recent meetings and failures panels.
func (m model) renderRow2() string {
	leftTotal := m.width / 2
	rightTotal := m.width - leftTotal
	leftInner := leftTotal - 2
	rightInner := rightTotal - 2

	// Recent.
	var recentBody strings.Builder
	if s := m.data.snap; s != nil && len(s.Recent) > 0 {
		for i, r := range s.Recent {
			if i >= 4 {
				break
			}
			fmt.Fprintf(&recentBody, "%s %s\n", styleAccent.Render("•"), truncate(r.Title, leftInner-3))
		}
	} else {
		recentBody.WriteString(styleDim.Render("(sin reuniones aún)"))
	}
	left := panelBox("RECIENTES", colorGreen, leftInner, padLines(strings.TrimRight(recentBody.String(), "\n"), 4))

	// Failures (focusable).
	failBorder := colorRed
	failTitle := "FALLADOS"
	if m.focus == focusFailures {
		failTitle = "FALLADOS ◂ enter: reintentar"
	}
	var failBody strings.Builder
	failures := m.data.failures()
	if len(failures) > 0 {
		for i, f := range failures {
			if i >= 4 {
				break
			}
			line := truncate(f.File+" — "+f.Reason, rightInner-2)
			if m.focus == focusFailures && i == m.failSel {
				failBody.WriteString(styleSelected.Render(" "+line+" ") + "\n")
			} else {
				failBody.WriteString(styleFail.Render(line) + "\n")
			}
		}
	} else {
		failBody.WriteString(styleDim.Render("(sin fallos)"))
	}
	right := panelBox(failTitle, failBorder, rightInner, padLines(strings.TrimRight(failBody.String(), "\n"), 4))
	return lipgloss.JoinHorizontal(lipgloss.Top, left, right)
}

// renderLog draws the scrollable, colorized log viewport.
func (m model) renderLog() string {
	inner := m.width - 2
	mode := "scroll"
	if m.followLog {
		mode = "follow"
	}
	border := colorPurple
	if m.focus == focusLog {
		border = colorCyan
	}
	title := fmt.Sprintf("LOG · %s", mode)
	return panelBox(title, border, inner, m.log.View())
}

// renderHelp draws the key hints and any transient toast.
func (m model) renderHelp() string {
	keys := styleHelp.Render("esc menú · q salir · tab foco · ↑↓ mover · enter reintentar · f follow · r refrescar · o/w web")
	if m.toast != "" && time.Since(m.toastAt) < 6*time.Second {
		return keys + "\n" + styleActive.Render("» "+m.toast)
	}
	return keys
}

// ---- helpers ----

func (m model) currentJob() *status.Job {
	if m.data.snap == nil {
		return nil
	}
	return m.data.snap.Current
}

// hasLiveStatus reports whether the snapshot was written by a serve process
// that is still running, i.e. its queue/current reflect reality right now.
func (m model) hasLiveStatus() bool {
	return m.data.snap != nil && !m.data.statusStale
}

func (m model) spinnerOrDash() string {
	if m.currentJob() != nil {
		return m.spinner.View() + " activo"
	}
	return "en reposo"
}

func (m model) serviceLabel() string {
	switch m.data.service {
	case serviceActive:
		return styleActive.Render("● servicio activo")
	case serviceInactive:
		return styleFail.Render("● servicio detenido")
	default:
		return styleDim.Render("○ servicio desconocido")
	}
}

func (m model) uptime() string {
	if !m.hasLiveStatus() || m.data.snap.StartedAt.IsZero() {
		return "—"
	}
	return formatDuration(time.Since(m.data.snap.StartedAt))
}

// formatDuration renders a duration as H:MM:SS (or MM:SS under an hour).
func formatDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	total := int(d.Seconds())
	h, mnt, s := total/3600, (total/60)%60, total%60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, mnt, s)
	}
	return fmt.Sprintf("%02d:%02d", mnt, s)
}

// padLines pads content to at least n lines so panels keep a stable height.
func padLines(content string, n int) string {
	lines := strings.Count(content, "\n") + 1
	if content == "" {
		lines = 1
	}
	if lines < n {
		content += strings.Repeat("\n", n-lines)
	}
	return content
}

// truncate shortens s to max runes, adding an ellipsis when cut.
func truncate(s string, max int) string {
	if max < 1 {
		return ""
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	if max <= 1 {
		return string(r[:max])
	}
	return string(r[:max-1]) + "…"
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
