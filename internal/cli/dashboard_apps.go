package cli

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/DevDevil/laravel-queue-governor/internal/ipc"
)

// dashboardAppRow is one app's latest known state for the TUI.md apps
// table (§8) — rebuilt from a snapshot frame (sent on every connect and
// again on every reconnect) plus decision frames (every tick) in between.
// Mode/ceiling fields are static config, only refreshed when a new
// snapshot arrives.
type dashboardAppRow struct {
	Name             string
	State            string
	Load             float64
	IOWait           float64
	Mode             string
	JobsSession      int
	Reason           string
	MaxLoadCeiling   float64
	MaxIOWaitCeiling int
	MaxConcurrent    int
	BatchSize        int
	ActiveRuns       []ipc.ActiveRun

	// MinInterval/SpikeCooldown/Priority/Nice are the remainder of this
	// app's live-tunable config (PLAN.md §7, TUI.md §9.6) not otherwise
	// needed by the apps table — populated from ipc.AppStatus purely so
	// the edit overlay can show current values. Nice mirrors
	// config.AppConfig.Nice: nil means "inherits global.nice".
	MinInterval   int
	SpikeCooldown int
	Priority      int
	Nice          *int

	// Schedule is this app's schedule:run status (PLAN.md §6.1), nil
	// unless scheduler.enabled — rendered as one extra sub-item line.
	Schedule *ipc.ScheduleStatus
}

// dashStateRank orders TUI.md §9.2's "state (running first)" sort.
func dashStateRank(state string) int {
	switch state {
	case "running":
		return 0
	case "cooldown":
		return 1
	case "throttled":
		return 2
	default:
		return 3 // idle
	}
}

// Column width bounds (TUI.md §8.1/§14): headers set the floor, these cap
// how wide a column can grow from its widest cell so one long app name
// or reason doesn't blow out the whole table — long cells truncate with
// "…" instead (padCell).
const (
	appsAppMaxWidth    = 24
	appsLoadMaxWidth   = 11
	appsIOMaxWidth     = 5
	appsModeMaxWidth   = 8
	appsJobsMaxWidth   = 6
	appsReasonMinWidth = 8
)

type appsColumnLayout struct {
	id    string
	width int
	right bool
}

// appsColumns computes each column's width from its header and widest
// cell (clamped to the max above), then drops optional columns — io,
// then mode, then jobs, in that order (TUI.md §8.1) — until the table
// fits width, always keeping app/state/load/reason. reason gets whatever
// width remains after the other included columns and their gaps.
func appsColumns(rows []dashboardAppRow, width int) []appsColumnLayout {
	appCol := appsColumnLayout{id: "app", width: columnWidth(len("app"), appsAppMaxWidth, rows, func(r dashboardAppRow) string { return r.Name })}
	stateCol := appsColumnLayout{id: "state", width: columnWidth(len("state"), 1<<30, rows, func(r dashboardAppRow) string {
		return dashStateGlyph(r.State) + " " + r.State
	})}
	loadCol := appsColumnLayout{id: "load", width: columnWidth(len("load"), appsLoadMaxWidth, rows, formatLoadCell)}
	ioCol := appsColumnLayout{id: "io", width: columnWidth(len("io"), appsIOMaxWidth, rows, formatIOCell)}
	modeCol := appsColumnLayout{id: "mode", width: columnWidth(len("mode"), appsModeMaxWidth, rows, func(r dashboardAppRow) string { return r.Mode })}
	jobsCol := appsColumnLayout{id: "jobs", right: true, width: columnWidth(len("jobs"), appsJobsMaxWidth, rows, func(r dashboardAppRow) string {
		return strconv.Itoa(r.JobsSession)
	})}

	core := []appsColumnLayout{appCol, stateCol, loadCol}
	optional := []appsColumnLayout{ioCol, modeCol, jobsCol} // drop order, left to right

	active := append(append([]appsColumnLayout{}, core...), optional...)

	fits := func(cols []appsColumnLayout) bool {
		total := appsReasonMinWidth + len(cols) // gaps: one between every pair, including before reason
		for _, c := range cols {
			total += c.width
		}
		return total <= width
	}

	for _, drop := range optional {
		if fits(active) {
			break
		}
		filtered := active[:0]
		for _, c := range active {
			if c.id != drop.id {
				filtered = append(filtered, c)
			}
		}
		active = filtered
	}

	fixedTotal := 0
	for _, c := range active {
		fixedTotal += c.width
	}
	reasonWidth := width - fixedTotal - len(active)
	if reasonWidth < appsReasonMinWidth {
		reasonWidth = appsReasonMinWidth
	}

	return append(active, appsColumnLayout{id: "reason", width: reasonWidth})
}

func columnWidth(headerLen, maxWidth int, rows []dashboardAppRow, cell func(dashboardAppRow) string) int {
	w := headerLen
	for _, r := range rows {
		if l := len([]rune(cell(r))); l > w {
			w = l
		}
	}
	if w > maxWidth {
		w = maxWidth
	}
	return w
}

func formatLoadCell(r dashboardAppRow) string {
	return fmt.Sprintf("%.1f/%.1f", r.Load, r.MaxLoadCeiling)
}

func formatIOCell(r dashboardAppRow) string {
	return fmt.Sprintf("%d%%", int(math.Round(r.IOWait)))
}

// reasonNumericSuffix is the "current vs ceiling" detail for the four
// reasons where that's a simple, always-available comparison (TUI.md
// §8.3's "3.8>2.5" example) — "" for every other reason in the PLAN.md §8
// vocabulary. min_interval/spike_cooldown have no case here: their
// detail would be "seconds remaining", which needs last-dispatch timing
// DecisionEvent doesn't carry per-tick — not worth a wire-format change
// for a nice-to-have annotation. Shared by the apps table's reason
// column (formatReasonDetail) and the log panel's skipped lines
// (formatSkippedDetail), so both render the same detail for the same
// reason.
func reasonNumericSuffix(reason string, r dashboardAppRow, hello ipc.HelloFrame) string {
	switch reason {
	case "app_load_high":
		return fmt.Sprintf("%.1f>%.1f", r.Load, r.MaxLoadCeiling)
	case "app_iowait_high":
		return fmt.Sprintf("%d>%d", int(math.Round(r.IOWait)), r.MaxIOWaitCeiling)
	case "global_load_high":
		return fmt.Sprintf("%.1f>%.1f", r.Load, hello.MaxSystemLoad)
	case "global_iowait_high":
		return fmt.Sprintf("%d>%d", int(math.Round(r.IOWait)), hello.MaxIOWait)
	default:
		return ""
	}
}

// formatReasonDetail renders the apps table's reason cell: bare reason,
// plus reasonNumericSuffix's detail when there is one.
func formatReasonDetail(r dashboardAppRow, hello ipc.HelloFrame) string {
	if r.Reason == "" {
		return "—"
	}
	if suffix := reasonNumericSuffix(r.Reason, r, hello); suffix != "" {
		return r.Reason + " " + suffix
	}
	return r.Reason
}

// dashRowReasonColor matches the reason text's colour to the row's state
// (TUI.md §8.3: cooldown reasons amber, throttle reasons red, idle
// reasons grey), muted when there's no active reason at all.
func dashRowReasonColor(reason, state string) lipgloss.Color {
	if reason == "" {
		return colTextMuted
	}
	return dashStateColor(state)
}

// padCell pads s to width with trailing (or leading, if right) spaces,
// or truncates (via truncateWithEllipsis) if s is longer than width —
// TUI.md §8.1: "never rely on proportional spacing".
func padCell(s string, width int, right bool) string {
	if len([]rune(s)) > width {
		return truncateWithEllipsis(s, width)
	}
	r := []rune(s)
	pad := strings.Repeat(" ", width-len(r))
	if right {
		return pad + s
	}
	return s + pad
}

// renderAppsTable renders the full apps table (TUI.md §8): a header row
// plus one row per app, monospace-aligned per appsColumns' layout, plus
// one indented sub-item line per currently-running job (r.ActiveRuns)
// showing how long it's been running. highlightIdx (TUI.md §9.1's
// selection) is the row index to render with a highlight background, or
// -1 for none; a selected app's sub-item lines get the same background,
// for visual grouping with their parent row. now is the reference point
// elapsed durations are measured against — passed in rather than read via
// time.Now() so this stays a pure, deterministically testable function.
// appsTableLineCount returns exactly how many lines renderAppsTable
// produces for rows (1 for the "no apps configured" placeholder;
// otherwise 1 header line + one line per row + one per that row's
// ActiveRuns). buildAppsBox (dashboard.go) uses this instead of a
// separately maintained formula, so the two can't silently drift apart —
// TestAppsTableLineCount_MatchesRenderAppsTableLineCount checks they stay
// in sync.
func appsTableLineCount(rows []dashboardAppRow) int {
	if len(rows) == 0 {
		return 1
	}
	n := 1 // header
	for _, r := range rows {
		n += 1 + len(r.ActiveRuns)
		if r.Schedule != nil {
			n++
		}
	}
	return n
}

func renderAppsTable(rows []dashboardAppRow, width int, hello ipc.HelloFrame, highlightIdx int, now time.Time) string {
	if len(rows) == 0 {
		return lipgloss.NewStyle().Foreground(colTextMuted).Render("no apps configured")
	}

	cols := appsColumns(rows, width)

	var b strings.Builder
	b.WriteString(renderAppsHeaderRow(cols))
	for i, r := range rows {
		b.WriteString("\n")
		highlighted := i == highlightIdx
		b.WriteString(renderAppsDataRow(r, cols, hello, highlighted))
		for _, run := range r.ActiveRuns {
			b.WriteString("\n")
			b.WriteString(renderAppsSubItemLine(run, width, now, highlighted))
		}
		if r.Schedule != nil {
			b.WriteString("\n")
			b.WriteString(renderScheduleSubItemLine(r.Schedule, width, now, highlighted))
		}
	}
	return b.String()
}

// dashSubItemPrefix is the tree-style indent for a per-job sub-item line
// under its app's row.
const dashSubItemPrefix = "  └─ "

// renderAppsSubItemLine renders one currently-running job as a sub-item
// line under its app's row: indented, showing its PID and elapsed time
// since dispatch, plus — once the runner has parsed a --json event for
// it (run.Job != "", json_output apps only) — the actual job class and
// status, e.g. "App\Jobs\ExampleJob · starting". An app that hasn't
// opted into json_output (or hasn't produced an event for this run yet)
// just renders without that suffix — no guessed/heuristic label, since
// we either know for certain or say nothing. Padded to width like a data
// row cell so the panel's right border stays aligned
// (padCell/truncateWithEllipsis, same as every other apps-table line).
// Uses colTextSecond rather than colTextMuted — a single app is always
// index 0 (the default selection), so this line routinely sits on
// colRowHighlight's background; colTextMuted is too close in luminance
// to that background on non-truecolor terminals and disappears (only
// reappearing via a terminal's selection/copy inversion), so this needs
// the higher-contrast tone.
func renderAppsSubItemLine(run ipc.ActiveRun, width int, now time.Time, highlighted bool) string {
	text := fmt.Sprintf("%srunning %s  pid %d", dashSubItemPrefix, formatElapsed(now.Sub(run.Start)), run.PID)
	if run.Job != "" {
		text += fmt.Sprintf("  %s · %s", run.Job, run.Status)
	}
	base := lipgloss.NewStyle().Foreground(colTextSecond)
	if highlighted {
		base = base.Background(colRowHighlight)
	}
	return base.Render(padCell(text, width, false))
}

// renderScheduleSubItemLine renders an app's schedule:run status as a
// sub-item line under its row (PLAN.md §6.1), styled like the per-job
// sub-items: "schedule:run running 12s" while in flight, otherwise the
// last run's exit code and duration plus when the next run is due.
func renderScheduleSubItemLine(st *ipc.ScheduleStatus, width int, now time.Time, highlighted bool) string {
	text := dashSubItemPrefix + FormatScheduleSummary(st, now)
	base := lipgloss.NewStyle().Foreground(colTextSecond)
	if highlighted {
		base = base.Background(colRowHighlight)
	}
	return base.Render(padCell(text, width, false))
}

// FormatScheduleSummary is a one-line, human-readable schedule:run status
// shared by the dashboard sub-item and `lqg status`.
func FormatScheduleSummary(st *ipc.ScheduleStatus, now time.Time) string {
	if st == nil {
		return "-"
	}
	var parts []string
	switch {
	case st.Running && st.LastStart != nil:
		parts = append(parts, "schedule:run running "+formatElapsed(now.Sub(*st.LastStart)))
	case st.LastExitCode != nil:
		parts = append(parts, fmt.Sprintf("schedule:run exit %d (%s)", *st.LastExitCode, formatMillis(st.LastDurationMs)))
	default:
		parts = append(parts, "schedule:run not run yet")
	}
	if st.LastSkipReason != nil && st.LastSkipAt != nil && (st.LastStart == nil || st.LastSkipAt.After(*st.LastStart)) {
		parts = append(parts, "skipped "+*st.LastSkipReason)
	}
	if st.NextDue != nil {
		parts = append(parts, "next "+formatElapsed(st.NextDue.Sub(now)))
	}
	return strings.Join(parts, "  ")
}

// formatMillis renders a run duration: "850ms" under a second, else
// seconds with one decimal ("1.2s"), then formatElapsed beyond a minute.
func formatMillis(ms int64) string {
	switch {
	case ms < 1000:
		return fmt.Sprintf("%dms", ms)
	case ms < 60000:
		return fmt.Sprintf("%.1fs", float64(ms)/1000)
	default:
		return formatElapsed(time.Duration(ms) * time.Millisecond)
	}
}

// formatElapsed renders a duration compactly for the apps table's
// sub-item lines: "12s" under a minute, "1m32s" under an hour, "1h05m"
// beyond that. Negative durations (clock skew) clamp to 0.
func formatElapsed(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	total := int(d.Seconds())
	h, m, s := total/3600, (total%3600)/60, total%60
	switch {
	case h > 0:
		return fmt.Sprintf("%dh%02dm", h, m)
	case m > 0:
		return fmt.Sprintf("%dm%02ds", m, s)
	default:
		return fmt.Sprintf("%ds", s)
	}
}

func renderAppsHeaderRow(cols []appsColumnLayout) string {
	parts := make([]string, len(cols))
	for i, c := range cols {
		parts[i] = padCell(c.id, c.width, c.right)
	}
	return lipgloss.NewStyle().Foreground(colTextSecond).Render(strings.Join(parts, " "))
}

// renderAppsDataRow renders one row. When highlighted (this row is the
// current selection, TUI.md §9.1), every cell — and the gaps between
// them — gets a highlight background, rather than wrapping the whole
// already-rendered row string in another style call: nesting lipgloss
// renders like that is exactly what caused the graph panel's overflow
// bug (dashboard_graphs.go), so each cell keeps building through a
// single Render() call, just with Background chained on when needed.
func renderAppsDataRow(r dashboardAppRow, cols []appsColumnLayout, hello ipc.HelloFrame, highlighted bool) string {
	base := lipgloss.NewStyle()
	if highlighted {
		base = base.Background(colRowHighlight)
	}

	parts := make([]string, len(cols))
	for i, c := range cols {
		switch c.id {
		case "app":
			parts[i] = base.Foreground(colTextPrimary).Render(padCell(r.Name, c.width, false))
		case "state":
			cell := padCell(dashStateGlyph(r.State)+" "+r.State, c.width, false)
			parts[i] = base.Foreground(dashStateColor(r.State)).Render(cell)
		case "load":
			cell := padCell(formatLoadCell(r), c.width, false)
			parts[i] = base.Foreground(dashThresholdColor(r.Load, r.MaxLoadCeiling)).Render(cell)
		case "io":
			cell := padCell(formatIOCell(r), c.width, false)
			parts[i] = base.Foreground(dashThresholdColor(r.IOWait, float64(r.MaxIOWaitCeiling))).Render(cell)
		case "mode":
			parts[i] = base.Foreground(colTextMuted).Render(padCell(r.Mode, c.width, false))
		case "jobs":
			parts[i] = base.Foreground(colTextSecond).Render(padCell(strconv.Itoa(r.JobsSession), c.width, true))
		case "reason":
			text := formatReasonDetail(r, hello)
			parts[i] = base.Foreground(dashRowReasonColor(r.Reason, r.State)).Render(padCell(text, c.width, false))
		}
	}

	sep := " "
	if highlighted {
		sep = base.Render(" ")
	}
	return strings.Join(parts, sep)
}
