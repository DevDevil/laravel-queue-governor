package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/DevDevil/laravel-queue-governor/internal/ipc"
)

var dashTestNow = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func TestPadCell_PadsShortLeftAndRight(t *testing.T) {
	if got := padCell("ab", 5, false); got != "ab   " {
		t.Errorf("left-align padCell = %q, want %q", got, "ab   ")
	}
	if got := padCell("ab", 5, true); got != "   ab" {
		t.Errorf("right-align padCell = %q, want %q", got, "   ab")
	}
}

func TestPadCell_TruncatesWithEllipsis(t *testing.T) {
	if got := padCell("abcdef", 4, false); got != "abc…" {
		t.Errorf("padCell truncation = %q, want %q", got, "abc…")
	}
}

func TestPadCell_ExactWidthUnchanged(t *testing.T) {
	if got := padCell("abcd", 4, false); got != "abcd" {
		t.Errorf("padCell exact width = %q, want unchanged %q", got, "abcd")
	}
}

func TestAppsColumns_AllColumnsPresentWhenRoomy(t *testing.T) {
	rows := []dashboardAppRow{{Name: "saas_seed_spa", State: "running", Mode: "per_job"}}
	cols := appsColumns(rows, 100)

	ids := make([]string, len(cols))
	for i, c := range cols {
		ids[i] = c.id
	}
	want := []string{"app", "state", "load", "io", "mode", "jobs", "reason"}
	if strings.Join(ids, ",") != strings.Join(want, ",") {
		t.Errorf("columns = %v, want %v", ids, want)
	}
}

func TestAppsColumns_DropsIOThenModeThenJobsWhenNarrow(t *testing.T) {
	rows := []dashboardAppRow{{Name: "a", State: "running", Mode: "per_job"}}

	hasCol := func(cols []appsColumnLayout, id string) bool {
		for _, c := range cols {
			if c.id == id {
				return true
			}
		}
		return false
	}

	// Roomy: everything present.
	full := appsColumns(rows, 100)
	if !hasCol(full, "io") || !hasCol(full, "mode") || !hasCol(full, "jobs") {
		t.Fatalf("expected all optional columns at width 100, got %+v", full)
	}

	// Progressively narrower must drop io, then mode, then jobs, in that
	// order, while app/state/load/reason always survive.
	prevWidth := 100
	for _, drop := range []string{"io", "mode", "jobs"} {
		width := prevWidth
		var cols []appsColumnLayout
		for width > 10 {
			width -= 5
			cols = appsColumns(rows, width)
			if !hasCol(cols, drop) {
				break
			}
		}
		if hasCol(cols, drop) {
			t.Errorf("expected %q to be droppable by narrowing width, still present at width %d: %+v", drop, width, cols)
		}
		for _, keep := range []string{"app", "state", "load", "reason"} {
			if !hasCol(cols, keep) {
				t.Errorf("core column %q was dropped (should never be), cols=%+v", keep, cols)
			}
		}
		prevWidth = width
	}
}

func TestAppsColumns_ReasonGetsRemainingWidth(t *testing.T) {
	rows := []dashboardAppRow{{Name: "a", State: "running"}}
	cols := appsColumns(rows, 60)

	fixedTotal := 0
	var reasonWidth int
	for _, c := range cols {
		if c.id == "reason" {
			reasonWidth = c.width
			continue
		}
		fixedTotal += c.width
	}
	if reasonWidth < appsReasonMinWidth {
		t.Errorf("reason width = %d, want at least the %d minimum", reasonWidth, appsReasonMinWidth)
	}
	gaps := len(cols) - 1 // cols includes reason; gaps = number of columns - 1
	total := fixedTotal + reasonWidth + gaps
	if total > 60 {
		t.Errorf("column layout total = %d, must not exceed available width 60", total)
	}
}

func TestFormatReasonDetail_KnownReasonsGetNumericDetail(t *testing.T) {
	row := dashboardAppRow{Load: 3.8, IOWait: 12, MaxLoadCeiling: 2.5, MaxIOWaitCeiling: 10, Reason: "app_load_high"}
	if got, want := formatReasonDetail(row, ipc.HelloFrame{}), "app_load_high 3.8>2.5"; got != want {
		t.Errorf("formatReasonDetail(app_load_high) = %q, want %q", got, want)
	}

	row.Reason = "app_iowait_high"
	if got, want := formatReasonDetail(row, ipc.HelloFrame{}), "app_iowait_high 12>10"; got != want {
		t.Errorf("formatReasonDetail(app_iowait_high) = %q, want %q", got, want)
	}

	row.Reason = "global_load_high"
	if got, want := formatReasonDetail(row, ipc.HelloFrame{MaxSystemLoad: 6.0}), "global_load_high 3.8>6.0"; got != want {
		t.Errorf("formatReasonDetail(global_load_high) = %q, want %q", got, want)
	}
}

func TestFormatReasonDetail_UnknownReasonIsBare(t *testing.T) {
	row := dashboardAppRow{Reason: "min_interval"}
	if got, want := formatReasonDetail(row, ipc.HelloFrame{}), "min_interval"; got != want {
		t.Errorf("formatReasonDetail(min_interval) = %q, want bare %q", got, want)
	}
}

func TestFormatReasonDetail_EmptyReasonIsEmDash(t *testing.T) {
	if got, want := formatReasonDetail(dashboardAppRow{}, ipc.HelloFrame{}), "—"; got != want {
		t.Errorf("formatReasonDetail(empty) = %q, want %q", got, want)
	}
}

func TestDashStateRank_RunningFirst(t *testing.T) {
	if dashStateRank("running") >= dashStateRank("cooldown") ||
		dashStateRank("cooldown") >= dashStateRank("throttled") ||
		dashStateRank("throttled") >= dashStateRank("idle") {
		t.Errorf("expected rank order running < cooldown < throttled < idle, got running=%d cooldown=%d throttled=%d idle=%d",
			dashStateRank("running"), dashStateRank("cooldown"), dashStateRank("throttled"), dashStateRank("idle"))
	}
}

// TestAppsTableLineCount_MatchesRenderAppsTableLineCount is the safety net
// for buildAppsBox (dashboard.go) using appsTableLineCount instead of its
// own formula: if renderAppsTable's actual output ever grows/shrinks a
// line without appsTableLineCount being updated to match, this test fails
// instead of the drift silently showing up as a log-panel sizing glitch.
func TestAppsTableLineCount_MatchesRenderAppsTableLineCount(t *testing.T) {
	cases := [][]dashboardAppRow{
		nil,
		{{Name: "a", State: "running"}},
		{{Name: "a", State: "running"}, {Name: "b", State: "idle"}},
		{{Name: "a", State: "running", ActiveRuns: []ipc.ActiveRun{{PID: 1}, {PID: 2}}}},
		{
			{Name: "a", State: "running", ActiveRuns: []ipc.ActiveRun{{PID: 1}}},
			{Name: "b", State: "idle"},
			{Name: "c", State: "cooldown", ActiveRuns: []ipc.ActiveRun{{PID: 2}, {PID: 3}, {PID: 4}}},
		},
	}
	for _, rows := range cases {
		got := appsTableLineCount(rows)
		want := len(strings.Split(renderAppsTable(rows, 80, ipc.HelloFrame{}, -1, dashTestNow), "\n"))
		if got != want {
			t.Errorf("appsTableLineCount(%d rows) = %d, want %d (renderAppsTable's actual line count)", len(rows), got, want)
		}
	}
}

func TestRenderAppsTable_NoAppsShowsPlaceholder(t *testing.T) {
	out := stripANSI(renderAppsTable(nil, 60, ipc.HelloFrame{}, -1, dashTestNow))
	if out != "no apps configured" {
		t.Errorf("renderAppsTable(nil) = %q, want the no-apps placeholder", out)
	}
}

func TestRenderAppsTable_HeaderAndOneRowPerApp(t *testing.T) {
	rows := []dashboardAppRow{
		{Name: "a", State: "running", Mode: "per_job"},
		{Name: "b", State: "idle", Mode: "batch"},
	}
	out := stripANSI(renderAppsTable(rows, 80, ipc.HelloFrame{}, -1, dashTestNow))
	lines := strings.Split(out, "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3 (header + 2 rows): %q", len(lines), out)
	}
	if !containsAll(lines[0], "app", "state", "load", "reason") {
		t.Errorf("header line = %q, missing expected column names", lines[0])
	}
	if !strings.Contains(lines[1], "a") || !strings.Contains(lines[1], "running") {
		t.Errorf("row 1 = %q, want app a/running", lines[1])
	}
	if !strings.Contains(lines[2], "b") || !strings.Contains(lines[2], "idle") {
		t.Errorf("row 2 = %q, want app b/idle", lines[2])
	}
}

// TestRenderAppsTable_VeryLongNameAndReasonTruncateWithoutBreakingAlignment
// is TUI.md §14: "Very long app name / reason -> truncate with …, never
// wrap or break the grid." Uses a name/reason far longer than any
// column's max width to force truncation on both.
func TestRenderAppsTable_VeryLongNameAndReasonTruncateWithoutBreakingAlignment(t *testing.T) {
	rows := []dashboardAppRow{
		{
			Name:  "this-is-a-way-too-long-application-name-for-any-reasonable-column-width",
			State: "throttled",
			Reason: "app_load_high_but_with_a_lot_of_extra_padding_text_appended_so_it_is_" +
				"definitely_longer_than_any_column_could_ever_reasonably_be",
		},
	}
	out := stripANSI(renderAppsTable(rows, 80, ipc.HelloFrame{}, -1, dashTestNow))
	lines := strings.Split(out, "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2 (header + 1 row)", len(lines))
	}

	// Every rendered line must be exactly as wide as the header (no
	// wrapping onto extra lines, no ragged/overflowing row).
	want := len([]rune(lines[0]))
	for i, l := range lines {
		if got := len([]rune(l)); got != want {
			t.Errorf("line[%d] width = %d, want %d (must match header exactly, no wrap)", i, got, want)
		}
	}
	if !strings.Contains(lines[1], "…") {
		t.Errorf("row = %q, want a truncated (…) fragment somewhere (name or reason)", lines[1])
	}
	if strings.Contains(lines[1], "this-is-a-way-too-long-application-name-for-any-reasonable-column-width") {
		t.Error("the full app name should not appear untruncated")
	}
}

func TestFormatElapsed(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{0, "0s"},
		{12 * time.Second, "12s"},
		{59 * time.Second, "59s"},
		{60 * time.Second, "1m00s"},
		{92 * time.Second, "1m32s"},
		{59 * time.Minute, "59m00s"},
		{60 * time.Minute, "1h00m"},
		{65*time.Minute + 5*time.Second, "1h05m"},
		{-5 * time.Second, "0s"},
	}
	for _, c := range cases {
		if got := formatElapsed(c.d); got != c.want {
			t.Errorf("formatElapsed(%v) = %q, want %q", c.d, got, c.want)
		}
	}
}

func TestRenderAppsTable_RunningAppWithOneActiveRunGetsSubItemLine(t *testing.T) {
	rows := []dashboardAppRow{
		{Name: "a", State: "running", ActiveRuns: []ipc.ActiveRun{{PID: 4242, Start: dashTestNow.Add(-12 * time.Second)}}},
	}
	out := stripANSI(renderAppsTable(rows, 80, ipc.HelloFrame{}, -1, dashTestNow))
	lines := strings.Split(out, "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3 (header + row + 1 sub-item): %q", len(lines), out)
	}
	if !strings.Contains(lines[2], "running 12s") {
		t.Errorf("sub-item line = %q, want it to mention \"running 12s\"", lines[2])
	}
	if !strings.Contains(lines[2], "pid 4242") {
		t.Errorf("sub-item line = %q, want it to mention \"pid 4242\"", lines[2])
	}
}

func TestRenderAppsTable_ActiveRunWithJobShowsJobAndStatus(t *testing.T) {
	rows := []dashboardAppRow{
		{
			Name: "a", State: "running",
			ActiveRuns: []ipc.ActiveRun{{PID: 4242, Start: dashTestNow.Add(-12 * time.Second), Job: `App\Jobs\ExampleJob`, Status: "starting"}},
		},
	}
	out := stripANSI(renderAppsTable(rows, 80, ipc.HelloFrame{}, -1, dashTestNow))
	lines := strings.Split(out, "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3 (header + row + 1 sub-item): %q", len(lines), out)
	}
	if !strings.Contains(lines[2], `App\Jobs\ExampleJob`) || !strings.Contains(lines[2], "starting") {
		t.Errorf("sub-item line = %q, want it to mention the job class and status", lines[2])
	}
}

func TestRenderAppsTable_ActiveRunWithoutJobOmitsJobSuffix(t *testing.T) {
	// json_output not configured (or no event has arrived yet) — Job is
	// "", so the line must render exactly as before, no guessed label.
	rows := []dashboardAppRow{
		{Name: "a", State: "running", ActiveRuns: []ipc.ActiveRun{{PID: 4242, Start: dashTestNow.Add(-12 * time.Second)}}},
	}
	out := stripANSI(renderAppsTable(rows, 80, ipc.HelloFrame{}, -1, dashTestNow))
	lines := strings.Split(out, "\n")
	if strings.Contains(lines[2], "·") {
		t.Errorf("sub-item line = %q, want no job/status suffix when Job is empty", lines[2])
	}
}

func TestRenderAppsTable_MultipleActiveRunsGetOneSubItemLineEach(t *testing.T) {
	rows := []dashboardAppRow{
		{Name: "a", State: "running", ActiveRuns: []ipc.ActiveRun{
			{PID: 100, Start: dashTestNow.Add(-12 * time.Second)},
			{PID: 200, Start: dashTestNow.Add(-4 * time.Second)},
		}},
	}
	out := stripANSI(renderAppsTable(rows, 80, ipc.HelloFrame{}, -1, dashTestNow))
	lines := strings.Split(out, "\n")
	if len(lines) != 4 {
		t.Fatalf("got %d lines, want 4 (header + row + 2 sub-items): %q", len(lines), out)
	}
	if !strings.Contains(lines[2], "running 12s") || !strings.Contains(lines[2], "pid 100") {
		t.Errorf("first sub-item = %q, want \"running 12s\" and \"pid 100\"", lines[2])
	}
	if !strings.Contains(lines[3], "running 4s") || !strings.Contains(lines[3], "pid 200") {
		t.Errorf("second sub-item = %q, want \"running 4s\" and \"pid 200\"", lines[3])
	}
}

func TestRenderAppsTable_NoActiveRunsHasNoSubItemLines(t *testing.T) {
	rows := []dashboardAppRow{{Name: "a", State: "idle"}}
	out := stripANSI(renderAppsTable(rows, 80, ipc.HelloFrame{}, -1, dashTestNow))
	lines := strings.Split(out, "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2 (header + row, no sub-items): %q", len(lines), out)
	}
}

func TestRenderAppsTable_SubItemLinesMatchRowWidth(t *testing.T) {
	rows := []dashboardAppRow{
		{Name: "a", State: "running", ActiveRuns: []ipc.ActiveRun{{PID: 4242, Start: dashTestNow.Add(-12 * time.Second)}}},
	}
	out := stripANSI(renderAppsTable(rows, 80, ipc.HelloFrame{}, -1, dashTestNow))
	lines := strings.Split(out, "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3", len(lines))
	}
	want := len([]rune(lines[0]))
	for i, l := range lines {
		if got := len([]rune(l)); got != want {
			t.Errorf("line[%d] width = %d, want %d (must match header, no ragged sub-item line)", i, got, want)
		}
	}
}
