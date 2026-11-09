package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/DevDevil/laravel-queue-governor/internal/ipc"
)

func TestFormatLogEntry_DispatchedPerJob(t *testing.T) {
	seq := 42
	d := ipc.DecisionEvent{Timestamp: time.Now(), App: "wonen31", Decision: "dispatched", JobSeq: &seq, Load: 3.8, IOWait: 12}
	row := dashboardAppRow{Mode: "per_job"}

	e := formatLogEntry(d, row, ipc.HelloFrame{})
	if e.Verb != "dispatched" {
		t.Errorf("Verb = %q, want dispatched", e.Verb)
	}
	if want := "job #42  load 3.8 io 12%"; e.Detail != want {
		t.Errorf("Detail = %q, want %q", e.Detail, want)
	}
}

func TestFormatLogEntry_DispatchedBatch(t *testing.T) {
	d := ipc.DecisionEvent{Timestamp: time.Now(), App: "wonen31", Decision: "dispatched", Load: 3.8, IOWait: 12}
	row := dashboardAppRow{Mode: "batch", BatchSize: 50}

	e := formatLogEntry(d, row, ipc.HelloFrame{})
	if want := "batch(50)  load 3.8 io 12%"; e.Detail != want {
		t.Errorf("Detail = %q, want %q", e.Detail, want)
	}
}

func TestFormatLogEntry_SkippedWithNumericDetail(t *testing.T) {
	reason := "app_load_high"
	d := ipc.DecisionEvent{Timestamp: time.Now(), App: "wonen31", Decision: "skipped", Reason: &reason}
	row := dashboardAppRow{Load: 3.8, MaxLoadCeiling: 2.5}

	e := formatLogEntry(d, row, ipc.HelloFrame{})
	if e.Verb != "skipped" {
		t.Errorf("Verb = %q, want skipped", e.Verb)
	}
	if e.Reason != "app_load_high" {
		t.Errorf("Reason = %q, want app_load_high", e.Reason)
	}
	if want := "app_load_high (3.8>2.5)"; e.Detail != want {
		t.Errorf("Detail = %q, want %q", e.Detail, want)
	}
}

func TestFormatLogEntry_SkippedWithoutNumericDetailIsBareReason(t *testing.T) {
	reason := "min_interval"
	d := ipc.DecisionEvent{Timestamp: time.Now(), App: "wonen31", Decision: "skipped", Reason: &reason}

	e := formatLogEntry(d, dashboardAppRow{}, ipc.HelloFrame{})
	if e.Detail != "min_interval" {
		t.Errorf("Detail = %q, want bare %q (no parens when there's no numeric detail)", e.Detail, "min_interval")
	}
}

func TestFormatLogEntry_DispatchedWithKnownJobAppendsJobStatus(t *testing.T) {
	seq := 42
	d := ipc.DecisionEvent{Timestamp: time.Now(), App: "wonen31", Decision: "dispatched", JobSeq: &seq, Load: 3.8, IOWait: 12}
	row := dashboardAppRow{
		Mode:       "per_job",
		ActiveRuns: []ipc.ActiveRun{{PID: 1, Job: `App\Jobs\ExampleJob`, Status: "starting"}},
	}

	e := formatLogEntry(d, row, ipc.HelloFrame{})
	if want := `job #42  load 3.8 io 12%  App\Jobs\ExampleJob · starting`; e.Detail != want {
		t.Errorf("Detail = %q, want %q", e.Detail, want)
	}
}

func TestFormatLogEntry_SkippedWithKnownJobAppendsJobStatus(t *testing.T) {
	reason := "app_concurrency"
	d := ipc.DecisionEvent{Timestamp: time.Now(), App: "wonen31", Decision: "skipped", Reason: &reason}
	row := dashboardAppRow{
		ActiveRuns: []ipc.ActiveRun{{PID: 1, Job: `App\Jobs\ExampleJob`, Status: "success"}},
	}

	e := formatLogEntry(d, row, ipc.HelloFrame{})
	if want := `app_concurrency  App\Jobs\ExampleJob · success`; e.Detail != want {
		t.Errorf("Detail = %q, want %q", e.Detail, want)
	}
}

func TestFormatLogEntry_MultipleActiveRunsWithJobsAreCommaJoined(t *testing.T) {
	reason := "app_concurrency"
	d := ipc.DecisionEvent{Timestamp: time.Now(), App: "wonen31", Decision: "skipped", Reason: &reason}
	row := dashboardAppRow{
		ActiveRuns: []ipc.ActiveRun{
			{PID: 1, Job: `App\Jobs\A`, Status: "starting"},
			{PID: 2, Job: `App\Jobs\B`, Status: "success"},
		},
	}

	e := formatLogEntry(d, row, ipc.HelloFrame{})
	if want := `app_concurrency  App\Jobs\A · starting, App\Jobs\B · success`; e.Detail != want {
		t.Errorf("Detail = %q, want %q", e.Detail, want)
	}
}

func TestFormatLogEntry_ActiveRunsWithoutJobLeavesDetailUnchanged(t *testing.T) {
	// json_output not configured (or no event yet) — ActiveRuns present
	// but Job empty, must not append anything.
	seq := 42
	d := ipc.DecisionEvent{Timestamp: time.Now(), App: "wonen31", Decision: "dispatched", JobSeq: &seq, Load: 3.8, IOWait: 12}
	row := dashboardAppRow{
		Mode:       "per_job",
		ActiveRuns: []ipc.ActiveRun{{PID: 1}},
	}

	e := formatLogEntry(d, row, ipc.HelloFrame{})
	if want := "job #42  load 3.8 io 12%"; e.Detail != want {
		t.Errorf("Detail = %q, want %q (unchanged, no job info known)", e.Detail, want)
	}
}

func TestAppendCappedLogEntries_DropsOldestAtCapacity(t *testing.T) {
	var buf []dashboardLogEntry
	for i := 0; i < 5; i++ {
		buf = appendCappedLogEntries(buf, dashboardLogEntry{App: string(rune('a' + i))}, 3)
	}
	if len(buf) != 3 {
		t.Fatalf("len(buf) = %d, want capped at 3", len(buf))
	}
	if buf[0].App != "c" || buf[2].App != "e" {
		t.Errorf("buf = %+v, want oldest two dropped (c,d,e remain)", buf)
	}
}

func TestDashLogReasonColor_MatchesReasonClass(t *testing.T) {
	if dashLogReasonColor("app_load_high") != colStateThrottled {
		t.Error("app_load_high should be the throttled colour")
	}
	if dashLogReasonColor("spike_cooldown") != colStateCooldown {
		t.Error("spike_cooldown should be the cooldown colour")
	}
	if dashLogReasonColor("queue_idle") != colStateIdle {
		t.Error("queue_idle should be the idle colour")
	}
}

func TestRenderLogContent_PadsAtTopWhenFewerEntriesThanHeight(t *testing.T) {
	entries := []dashboardLogEntry{{App: "a", Verb: "dispatched", Detail: "x"}}
	out := renderLogContent(entries, 4)
	lines := strings.Split(out, "\n")
	if len(lines) != 4 {
		t.Fatalf("got %d lines, want 4", len(lines))
	}
	if strings.TrimSpace(stripANSI(lines[0])) != "" || strings.TrimSpace(stripANSI(lines[1])) != "" || strings.TrimSpace(stripANSI(lines[2])) != "" {
		t.Errorf("expected the first 3 lines blank (bottom-anchored), got %q", lines[:3])
	}
	if !strings.Contains(stripANSI(lines[3]), "a") {
		t.Errorf("expected the one real entry on the LAST line, got %q", lines[3])
	}
}

func TestRenderLogContent_ShowsOnlyTheMostRecentEntriesWhenOverflowing(t *testing.T) {
	entries := []dashboardLogEntry{
		{App: "old", Verb: "dispatched", Detail: "x"},
		{App: "mid", Verb: "dispatched", Detail: "x"},
		{App: "new", Verb: "dispatched", Detail: "x"},
	}
	out := stripANSI(renderLogContent(entries, 2))
	lines := strings.Split(out, "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2", len(lines))
	}
	if strings.Contains(out, "old") {
		t.Errorf("oldest entry should have scrolled off when only 2 of 3 fit: %q", out)
	}
	if !strings.Contains(lines[1], "new") {
		t.Errorf("newest entry should be on the last line, got %q", lines[1])
	}
}

func TestRenderLogLine_VerbColourByOutcome(t *testing.T) {
	dispatched := renderLogLine(dashboardLogEntry{Verb: "dispatched", Detail: "x"})
	skipped := renderLogLine(dashboardLogEntry{Verb: "skipped", Reason: "app_load_high", Detail: "x"})
	if dispatched == skipped {
		t.Error("dispatched and skipped lines should render with different colours")
	}
}
