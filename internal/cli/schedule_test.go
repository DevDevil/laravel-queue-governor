package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/DevDevil/laravel-queue-governor/internal/ipc"
)

func TestFormatScheduleSummary(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 30, 0, time.UTC)
	start := now.Add(-20 * time.Second)
	next := now.Add(40 * time.Second)
	exit := 1
	skipAt := now.Add(-5 * time.Second)
	overlap := "schedule_overlap"

	cases := []struct {
		name string
		st   *ipc.ScheduleStatus
		want []string
	}{
		{"disabled", nil, []string{"-"}},
		{"never run", &ipc.ScheduleStatus{NextDue: &next}, []string{"not run yet", "next 40s"}},
		{"running", &ipc.ScheduleStatus{Running: true, LastStart: &start, NextDue: &next}, []string{"running 20s"}},
		{"failed", &ipc.ScheduleStatus{LastStart: &start, LastExitCode: &exit, LastDurationMs: 1500}, []string{"exit 1 (1.5s)"}},
		{"skipped after last run", &ipc.ScheduleStatus{LastStart: &start, LastExitCode: &exit, LastSkipAt: &skipAt, LastSkipReason: &overlap}, []string{"skipped schedule_overlap"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := FormatScheduleSummary(c.st, now)
			for _, w := range c.want {
				if !strings.Contains(got, w) {
					t.Errorf("FormatScheduleSummary = %q, want it to contain %q", got, w)
				}
			}
		})
	}
}

func TestDashboardModel_ScheduleDecisionKeepsQueueState(t *testing.T) {
	m := newDashboardModel(nil, nil, nil, "")
	m.apply(dashboardFrame{Snapshot: &ipc.SnapshotFrame{Apps: []ipc.AppStatus{{Name: "a", State: "running"}}}})

	reason := "schedule_overlap"
	st := &ipc.ScheduleStatus{}
	m.apply(dashboardFrame{Decision: &ipc.DecisionFrame{DecisionEvent: ipc.DecisionEvent{
		App: "a", Kind: ipc.KindSchedule, Decision: "skipped", Reason: &reason, State: "idle", Schedule: st,
	}}})

	row := m.appRows["a"]
	if row.State != "running" || row.Reason != "" {
		t.Errorf("queue state/reason changed by schedule event: %+v", row)
	}
	if row.Schedule != st {
		t.Error("row.Schedule not updated from schedule event")
	}
	if n := len(m.logEntries); n != 1 || m.logEntries[0].App != "a" || m.logEntries[0].Verb != "schedule:run skipped" {
		t.Errorf("log entries = %+v, want one schedule:run line", m.logEntries)
	}
	if got := appsTableLineCount([]dashboardAppRow{*row}); got != 3 {
		t.Errorf("appsTableLineCount = %d, want 3 (header, row, schedule sub-item)", got)
	}
}
