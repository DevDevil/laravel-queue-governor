package monitor

import (
	"testing"
	"time"

	"github.com/DevDevil/laravel-queue-governor/internal/appstate"
	"github.com/DevDevil/laravel-queue-governor/internal/scheduler"
)

var base = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func TestEvents_Dispatched(t *testing.T) {
	m := appstate.New(1, 0, 0, false)
	m.OnDispatch(base)
	m.RecordRunStarted(1234, base)
	apps := []*scheduler.App{{Machine: m}}
	decisions := []scheduler.Decision{
		{Name: "wonen31", State: appstate.Running, Dispatched: true, JobSeq: 7, Load: 3.8, IOWait: 12},
	}

	events := Events(base, decisions, apps)
	if len(events) != 1 {
		t.Fatalf("len(events) = %d, want 1", len(events))
	}

	e := events[0]
	if e.Decision != "dispatched" {
		t.Errorf("Decision = %q, want dispatched", e.Decision)
	}
	if e.Reason != nil {
		t.Errorf("Reason = %v, want nil", e.Reason)
	}
	if e.JobSeq == nil || *e.JobSeq != 7 {
		t.Errorf("JobSeq = %v, want pointer to 7", e.JobSeq)
	}
	if e.State != "running" || e.App != "wonen31" || e.Load != 3.8 || e.IOWait != 12 {
		t.Errorf("event = %+v, want matching state/app/load/iowait", e)
	}
	if !e.Timestamp.Equal(base) {
		t.Errorf("Timestamp = %v, want %v", e.Timestamp, base)
	}
	if len(e.ActiveRuns) != 1 || e.ActiveRuns[0].PID != 1234 || !e.ActiveRuns[0].Start.Equal(base) {
		t.Errorf("ActiveRuns = %v, want [{1234 %v}]", e.ActiveRuns, base)
	}
}

func TestEvents_Skipped(t *testing.T) {
	m := appstate.New(1, 0, 0, false)
	apps := []*scheduler.App{{Machine: m}}
	decisions := []scheduler.Decision{
		{Name: "squaremanager", State: appstate.Throttled, Dispatched: false, Reason: "app_load_high", Load: 5.0, IOWait: 3},
	}

	events := Events(base, decisions, apps)
	e := events[0]

	if e.Decision != "skipped" {
		t.Errorf("Decision = %q, want skipped", e.Decision)
	}
	if e.Reason == nil || *e.Reason != "app_load_high" {
		t.Errorf("Reason = %v, want pointer to app_load_high", e.Reason)
	}
	if e.JobSeq != nil {
		t.Errorf("JobSeq = %v, want nil", e.JobSeq)
	}
}

func TestEvents_OneEventPerDecision(t *testing.T) {
	apps := []*scheduler.App{
		{Machine: appstate.New(1, 0, 0, false)},
		{Machine: appstate.New(1, 0, 0, false)},
		{Machine: appstate.New(1, 0, 0, false)},
	}
	decisions := []scheduler.Decision{
		{Name: "a", Dispatched: true},
		{Name: "b", Dispatched: false, Reason: "not_scheduled"},
		{Name: "c", Dispatched: false, Reason: "min_interval"},
	}

	events := Events(base, decisions, apps)
	if len(events) != 3 {
		t.Fatalf("len(events) = %d, want 3", len(events))
	}
	for i, e := range events {
		if e.App != decisions[i].Name {
			t.Errorf("events[%d].App = %q, want %q", i, e.App, decisions[i].Name)
		}
	}
}

func TestEvents_ActiveRunsReflectsMultipleConcurrentRuns(t *testing.T) {
	m := appstate.New(2, 0, 0, false)
	t1 := base
	t2 := base.Add(5 * time.Second)
	m.OnDispatch(t1)
	m.RecordRunStarted(100, t1)
	m.OnDispatch(t2)
	m.RecordRunStarted(200, t2)
	apps := []*scheduler.App{{Machine: m}}
	decisions := []scheduler.Decision{
		{Name: "wonen31", State: appstate.Running, Dispatched: false, Reason: "app_concurrency"},
	}

	events := Events(base.Add(10*time.Second), decisions, apps)
	e := events[0]

	if len(e.ActiveRuns) != 2 || e.ActiveRuns[0].PID != 100 || !e.ActiveRuns[0].Start.Equal(t1) ||
		e.ActiveRuns[1].PID != 200 || !e.ActiveRuns[1].Start.Equal(t2) {
		t.Fatalf("ActiveRuns = %v, want [{100 %v} {200 %v}]", e.ActiveRuns, t1, t2)
	}
}

func TestEvents_ActiveRunsCarryJobAndStatus(t *testing.T) {
	m := appstate.New(1, 0, 0, false)
	m.OnDispatch(base)
	m.RecordRunStarted(100, base)
	m.UpdateActiveRunJob(100, `App\Jobs\ExampleJob`, "success")
	apps := []*scheduler.App{{Machine: m}}
	decisions := []scheduler.Decision{{Name: "wonen31", State: appstate.Running}}

	events := Events(base, decisions, apps)
	e := events[0]

	if len(e.ActiveRuns) != 1 || e.ActiveRuns[0].Job != `App\Jobs\ExampleJob` || e.ActiveRuns[0].Status != "success" {
		t.Errorf("ActiveRuns = %+v, want job/status carried through", e.ActiveRuns)
	}
}

func TestEvents_NoActiveRunsOmitsField(t *testing.T) {
	m := appstate.New(1, 0, 0, false)
	apps := []*scheduler.App{{Machine: m}}
	decisions := []scheduler.Decision{
		{Name: "wonen31", State: appstate.Idle, Dispatched: true, JobSeq: 1},
	}

	events := Events(base, decisions, apps)
	if len(events[0].ActiveRuns) != 0 {
		t.Fatalf("ActiveRuns = %v, want empty", events[0].ActiveRuns)
	}
}
