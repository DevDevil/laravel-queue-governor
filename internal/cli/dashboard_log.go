package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/DevDevil/laravel-queue-governor/internal/ipc"
)

// dashLogCapacity is the log panel's in-memory ring size (TUI.md §10:
// "the last ~500 lines").
const dashLogCapacity = 500

// dashboardLogEntry is one rendered line's data — the daemon emits a
// decision event for every app every tick (CLAUDE.md invariant), so this
// is the ground truth of "picked up vs not, and why" TUI.md §10 wants the
// log panel to be. Detail/Reason are computed once, at the moment the
// decision arrives, against the ceilings known then — a later reload
// changing a ceiling shouldn't retroactively change what an old log line
// says happened.
type dashboardLogEntry struct {
	Time   time.Time
	App    string
	Verb   string // "dispatched" | "skipped"
	Detail string
	Reason string // bare reason, only set when Verb == "skipped"
}

// formatLogEntry builds one log entry from a live decision event, using
// row (the app's current known state, for its mode/batch size/ceilings)
// and hello (for the two global-reason ceilings) to fill in TUI.md §10's
// detail formats.
func formatLogEntry(d ipc.DecisionEvent, row dashboardAppRow, hello ipc.HelloFrame) dashboardLogEntry {
	e := dashboardLogEntry{Time: d.Timestamp, App: d.App}

	if d.Decision == "dispatched" {
		e.Verb = "dispatched"
		if row.Mode == "batch" {
			e.Detail = fmt.Sprintf("batch(%d)  load %.1f io %.0f%%", row.BatchSize, d.Load, d.IOWait)
		} else {
			seq := 0
			if d.JobSeq != nil {
				seq = *d.JobSeq
			}
			e.Detail = fmt.Sprintf("job #%d  load %.1f io %.0f%%", seq, d.Load, d.IOWait)
		}
	} else {
		e.Verb = "skipped"
		if d.Reason != nil {
			e.Reason = *d.Reason
		}
		e.Detail = formatSkippedDetail(e.Reason, row, hello)
	}

	if jobs := formatActiveRunJobs(row.ActiveRuns); jobs != "" {
		e.Detail += "  " + jobs
	}
	return e
}

// formatActiveRunJobs summarizes whichever of this app's currently
// in-flight runs have a known job (json_output apps only, once the
// runner has parsed a --json event for them) as "Job · status" pairs,
// comma-separated — the same per-job detail the apps panel's sub-items
// show (dashboard_apps.go), surfaced in the log too rather than only
// there. "" if none are known yet (an app without json_output, or
// before any event has arrived), so the log line stays exactly as
// before in that case.
func formatActiveRunJobs(runs []ipc.ActiveRun) string {
	var parts []string
	for _, r := range runs {
		if r.Job != "" {
			parts = append(parts, fmt.Sprintf("%s · %s", r.Job, r.Status))
		}
	}
	return strings.Join(parts, ", ")
}

// formatSkippedDetail is TUI.md §10's "skipped <reason> (<detail>)" —
// the parenthesized part only appears when reasonNumericSuffix has
// something to say about this reason.
func formatSkippedDetail(reason string, row dashboardAppRow, hello ipc.HelloFrame) string {
	if suffix := reasonNumericSuffix(reason, row, hello); suffix != "" {
		return fmt.Sprintf("%s (%s)", reason, suffix)
	}
	return reason
}

// dashLogReasonColor classes a skip reason by how concerning it is
// (TUI.md §10: "verb coloured by outcome"), independent of the row's
// current live state (a log line is historical — it shouldn't recolor
// itself if the app's state has since moved on). Load/iowait-ceiling
// reasons are exactly what the throttled state means; spike_cooldown is
// exactly what the cooldown state means; everything else (concurrency
// limits, pacing, idle queues) is routine rather than a warning.
func dashLogReasonColor(reason string) lipgloss.Color {
	switch reason {
	case "app_load_high", "app_iowait_high", "global_load_high", "global_iowait_high":
		return colStateThrottled
	case "spike_cooldown":
		return colStateCooldown
	default:
		return colStateIdle
	}
}

func appendCappedLogEntries(buf []dashboardLogEntry, e dashboardLogEntry, capacity int) []dashboardLogEntry {
	buf = append(buf, e)
	if len(buf) > capacity {
		buf = buf[len(buf)-capacity:]
	}
	return buf
}

// renderLogContent renders exactly height lines: the most recent min(height,
// len(entries)) entries, newest at the bottom, blank-padded at the TOP
// when there are fewer entries than height — TUI.md §10: "tails newest,
// fills remaining space", i.e. bottom-anchored, the opposite direction
// from fitLines' top-anchored padding used elsewhere in this layout.
func renderLogContent(entries []dashboardLogEntry, height int) string {
	if height < 1 {
		height = 1
	}

	visible := entries
	if len(visible) > height {
		visible = visible[len(visible)-height:]
	}

	lines := make([]string, len(visible))
	for i, e := range visible {
		lines[i] = renderLogLine(e)
	}

	if len(lines) < height {
		pad := make([]string, height-len(lines))
		lines = append(pad, lines...)
	}
	return strings.Join(lines, "\n")
}

func renderLogLine(e dashboardLogEntry) string {
	ts := lipgloss.NewStyle().Foreground(colTextMuted).Render(e.Time.Format("15:04:05"))
	app := lipgloss.NewStyle().Foreground(colTextPrimary).Render(e.App)

	verbColor := colStateRunning
	if e.Verb == "skipped" {
		verbColor = dashLogReasonColor(e.Reason)
	}
	verb := lipgloss.NewStyle().Foreground(verbColor).Render(e.Verb)
	detail := lipgloss.NewStyle().Foreground(colTextSecond).Render(e.Detail)

	return ts + "  " + app + "  " + verb + "  " + detail
}
