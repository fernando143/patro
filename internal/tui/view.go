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
	return m.frameWithLog(m.log.View())
}

// frameWithLog composes the dashboard frame with body as the log panel's
// content. View() calls it with the live viewport content; resizeLog calls
// it (via reserveRows) with an empty body to measure how many rows the
// chrome around the log consumes, replacing the old hardcoded vpReserve.
func (m model) frameWithLog(body string) string {
	sections := []string{
		m.renderHeader(),
		m.renderStats(),
		m.renderRow1(),
		m.renderRow2(),
		m.renderLog(body),
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

// statCardSpec is the title/value/sub/color for one counter card, decoupled
// from layout so renderStats can lay the same four cards out at whatever
// column count fits the terminal.
type statCardSpec struct {
	title, value, sub string
	color             lipgloss.Color
}

// statCardSpecs builds the four counter cards' content.
func (m model) statCardSpecs() []statCardSpec {
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

	return []statCardSpec{
		{"PROCESANDO", processing, m.spinnerOrDash(), colorCyan},
		{"EN COLA", queueValue, queueSub, colorPurple},
		{"PROCESADOS", fmt.Sprint(m.data.processedTotal), fmt.Sprintf("%d esta sesión", processedSession), colorGreen},
		{"FALLADOS", fmt.Sprint(failed), "esta sesión", colorRed},
	}
}

// renderStats draws the four counter cards, falling back 4→2→1 columns as
// splitCols reports each breakpoint infeasible — 4 in a row on wide
// terminals, a 2x2 grid at medium widths, one per row at the narrow floor.
func (m model) renderStats() string {
	specs := m.statCardSpecs()
	for _, n := range []int{4, 2, 1} {
		if cols := splitCols(m.width, n, minCardInner); cols != nil {
			return joinCardGrid(specs, cols)
		}
	}
	// Below splitCols(_, 1, minCardInner)'s own floor (an unrealistically
	// narrow terminal menu/dashboard already bail out before reaching this
	// code at): still render one full-width column instead of nothing.
	return joinCardGrid(specs, []int{max(1, m.width)})
}

// joinCardGrid lays specs out len(cols) per row, reusing cols for every row
// so every card in a column shares its width. cols holds each column's
// on-screen TOTAL width (splitCols' contract: they sum to the terminal
// width); statCard/panelBox take the Width()-argument, which is 2 columns
// narrower once the border is added back on render — hence innerWidth.
func joinCardGrid(specs []statCardSpec, cols []int) string {
	n := len(cols)
	rows := make([]string, 0, (len(specs)+n-1)/n)
	for start := 0; start < len(specs); start += n {
		end := min(start+n, len(specs))
		row := make([]string, 0, end-start)
		for i := start; i < end; i++ {
			s := specs[i]
			row = append(row, statCard(s.title, s.value, s.sub, s.color, innerWidth(cols[i-start])))
		}
		rows = append(rows, lipgloss.JoinHorizontal(lipgloss.Top, row...))
	}
	return lipgloss.JoinVertical(lipgloss.Left, rows...)
}

// statCard renders a single counter card.
func statCard(title, value, sub string, color lipgloss.Color, inner int) string {
	body := statValue.Foreground(color).Render(value) + "\n" + styleDim.Render(sub)
	return panelBox(title, color, inner, body)
}

// renderRow1 draws the in-flight job panel and the background maintenance
// panel sharing the left half, and the config/alerts panel on the right —
// the same top-level 2-column split (and the same CONFIGURACIÓN width) as
// before maintenance existed, so narrow-terminal wrapping there is
// unaffected. Maintenance sits beside the in-flight job card rather than
// replacing it: design D10 lets a video job and a `patro reconcile` run
// (design D8) be in progress at the same time, so both must stay visible.
func (m model) renderRow1() string {
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
	maintBody := m.maintenanceBody()
	configBody := m.configBody()

	leftTotal, rightTotal := m.width, m.width
	cols := splitCols(m.width, 2, minPanelInner)
	if cols != nil {
		leftTotal, rightTotal = cols[0], cols[1]
	}
	left := renderJobAndMaintenance(leftTotal, jobBody, maintBody)
	right := panelBox("CONFIGURACIÓN", colorCyan, innerWidth(rightTotal), padLines(configBody, 2))

	if cols == nil {
		return lipgloss.JoinVertical(lipgloss.Left, left, right)
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, left, right)
}

// renderJobAndMaintenance lays the EN CURSO and MANTENIMIENTO cards out
// within totalWidth: side by side when there is room for two panel-width
// columns, stacked vertically otherwise — either way staying inside
// totalWidth, so CONFIGURACIÓN's own width (and wrapping) never changes.
// minPanelInner (not the narrower minCardInner) keeps each card wide enough
// that ordinary status phrases ("estado en vivo no disponible") never wrap.
func renderJobAndMaintenance(totalWidth int, jobBody, maintBody string) string {
	if cols := splitCols(totalWidth, 2, minPanelInner); cols != nil {
		job := panelBox("EN CURSO", colorSunset, innerWidth(cols[0]), padLines(jobBody, 2))
		maint := panelBox("MANTENIMIENTO", colorPurple, innerWidth(cols[1]), padLines(maintBody, 2))
		return lipgloss.JoinHorizontal(lipgloss.Top, job, maint)
	}
	job := panelBox("EN CURSO", colorSunset, innerWidth(totalWidth), padLines(jobBody, 2))
	maint := panelBox("MANTENIMIENTO", colorPurple, innerWidth(totalWidth), padLines(maintBody, 2))
	return lipgloss.JoinVertical(lipgloss.Left, job, maint)
}

// maintenanceBody renders the background maintenance card (design D10/D12
// data flow, TUI surface): live phase/progress while `patro reconcile` (`m`
// key) is running, or the flagged-topic count when idle.
func (m model) maintenanceBody() string {
	maint := m.data.maintenance()
	if maint == nil {
		if m.data.flaggedCount == 0 {
			return styleDim.Render("sin temas pendientes") + "\n" + styleDim.Render("m reconciliar")
		}
		return styleAlert.Render(fmt.Sprintf("%d tema(s) marcados", m.data.flaggedCount)) + "\n" +
			styleDim.Render("m reconciliar")
	}

	var phaseLabel, suffix string
	switch maint.Phase {
	case status.PhaseRebuildingIndex:
		phaseLabel = "reconstruyendo índice"
	case status.PhaseReconciling:
		phaseLabel = "reconciliando"
		suffix = " marcados"
	default:
		phaseLabel = string(maint.Phase)
	}
	return m.spinner.View() + " " + styleAccent.Render(phaseLabel) + "\n" +
		styleDim.Render(fmt.Sprintf("%d/%d%s", maint.Done, maint.Total, suffix))
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

// renderRow2 draws the recent meetings and failures panels, side by side
// when splitCols allows it, stacked otherwise. Both panels grow with
// listRows(m.height) instead of a fixed 4-item cap, and show a "+N más"
// indicator when items are truncated so the panel height stays stable.
func (m model) renderRow2() string {
	n := listRows(m.height)

	leftTotal, rightTotal := m.width, m.width
	cols := splitCols(m.width, 2, minPanelInner)
	if cols != nil {
		leftTotal, rightTotal = cols[0], cols[1]
	}
	leftInner, rightInner := innerWidth(leftTotal), innerWidth(rightTotal)

	recentBody := renderRecentBody(m.data.snap, n, leftInner-3)
	failBody, failTitle := m.renderFailuresBody(n, rightInner-2)

	left := panelBox("RECIENTES", colorGreen, leftInner, padLines(recentBody, n))
	right := panelBox(failTitle, colorRed, rightInner, padLines(failBody, n))

	if cols == nil {
		return lipgloss.JoinVertical(lipgloss.Left, left, right)
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, left, right)
}

// renderRecentBody lists up to n-1 recent meetings plus a "+N más"
// indicator when there are more than n, so the panel's height stays stable
// instead of growing without bound.
func renderRecentBody(snap *status.Snapshot, n, truncWidth int) string {
	if snap == nil || len(snap.Recent) == 0 {
		return styleDim.Render("(sin reuniones aún)")
	}
	items := snap.Recent
	shown, more := listShown(len(items), n)

	var b strings.Builder
	for i := 0; i < shown; i++ {
		fmt.Fprintf(&b, "%s %s\n", styleAccent.Render("•"), truncate(items[i].Title, truncWidth))
	}
	if more > 0 {
		fmt.Fprintf(&b, "%s\n", styleDim.Render(fmt.Sprintf("+%d más", more)))
	}
	return strings.TrimRight(b.String(), "\n")
}

// renderFailuresBody lists up to n-1 failures plus a "+N más" indicator, and
// reports the panel title (which grows a hint when the panel is focused).
func (m model) renderFailuresBody(n, truncWidth int) (body, title string) {
	title = "FALLADOS"
	if m.focus == focusFailures {
		title = "FALLADOS ◂ enter: reintentar"
	}

	failures := m.data.failures()
	if len(failures) == 0 {
		return styleDim.Render("(sin fallos)"), title
	}
	shown, more := listShown(len(failures), n)

	var b strings.Builder
	for i := 0; i < shown; i++ {
		f := failures[i]
		line := truncate(f.File+" — "+f.Reason, truncWidth)
		if m.focus == focusFailures && i == m.failSel {
			b.WriteString(styleSelected.Render(" "+line+" ") + "\n")
		} else {
			b.WriteString(styleFail.Render(line) + "\n")
		}
	}
	if more > 0 {
		fmt.Fprintf(&b, "%s\n", styleDim.Render(fmt.Sprintf("+%d más", more)))
	}
	return strings.TrimRight(b.String(), "\n"), title
}

// listShown reports how many of total items to render (shown) and how many
// are left over (more) for a "+N más" indicator, given a panel that can show
// at most n rows: all of them fit, or n-1 items plus the indicator line.
func listShown(total, n int) (shown, more int) {
	if total <= n {
		return total, 0
	}
	return n - 1, total - (n - 1)
}

// renderLog draws the scrollable, colorized log viewport around body (the
// live viewport content, or "" when frameWithLog is only being measured).
func (m model) renderLog(body string) string {
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
	return panelBox(title, border, inner, body)
}

// renderHelp draws the key hints and any transient toast, truncated to the
// terminal width like every other line in the frame.
func (m model) renderHelp() string {
	keys := styleHelp.Render(truncate("esc menú · q salir · tab foco · ↑↓ mover · enter reintentar · f follow · r refrescar · o/w web · m reconciliar", m.width))
	if m.toast != "" && time.Since(m.toastAt) < 6*time.Second {
		return keys + "\n" + styleActive.Render(truncate("» "+m.toast, m.width))
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
