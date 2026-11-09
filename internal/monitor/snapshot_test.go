package monitor

import (
	"testing"
	"time"

	"github.com/DevDevil/laravel-queue-governor/internal/appstate"
	"github.com/DevDevil/laravel-queue-governor/internal/config"
	"github.com/DevDevil/laravel-queue-governor/internal/scheduler"
)

func TestSnapshot_DispatchedAppHasNoReasonAndLastDispatchSet(t *testing.T) {
	m := appstate.New(1, 2*time.Second, 30*time.Second, false)
	m.OnDispatch(base)

	apps := []*scheduler.App{{Machine: m}}
	decisions := []scheduler.Decision{
		{Name: "wonen31", State: appstate.Running, Dispatched: true, JobSeq: 1, Load: 3.8, IOWait: 12},
	}

	snap := Snapshot(base, decisions, apps)
	if len(snap.Apps) != 1 {
		t.Fatalf("len(Apps) = %d, want 1", len(snap.Apps))
	}

	a := snap.Apps[0]
	if a.Name != "wonen31" || a.State != "running" || a.Load != 3.8 || a.IOWait != 12 {
		t.Errorf("AppStatus = %+v, unexpected core fields", a)
	}
	if a.Reason != nil {
		t.Errorf("Reason = %v, want nil for a dispatched app", a.Reason)
	}
	if a.JobsSession != 1 {
		t.Errorf("JobsSession = %d, want 1", a.JobsSession)
	}
	if a.LastDispatch == nil || !a.LastDispatch.Equal(base) {
		t.Errorf("LastDispatch = %v, want %v", a.LastDispatch, base)
	}
}

func TestSnapshot_NeverDispatchedHasNilLastDispatch(t *testing.T) {
	m := appstate.New(1, 2*time.Second, 30*time.Second, false)
	apps := []*scheduler.App{{Machine: m}}
	decisions := []scheduler.Decision{
		{Name: "squaremanager", State: appstate.Idle, Dispatched: false, Reason: "not_scheduled"},
	}

	snap := Snapshot(base, decisions, apps)
	a := snap.Apps[0]

	if a.LastDispatch != nil {
		t.Errorf("LastDispatch = %v, want nil (never dispatched)", a.LastDispatch)
	}
	if a.Reason == nil || *a.Reason != "not_scheduled" {
		t.Errorf("Reason = %v, want pointer to not_scheduled", a.Reason)
	}
	if a.JobsSession != 0 {
		t.Errorf("JobsSession = %d, want 0", a.JobsSession)
	}
}

func TestSnapshot_CarriesStaticAppConfigForDashboard(t *testing.T) {
	m := appstate.New(1, 2*time.Second, 30*time.Second, false)
	apps := []*scheduler.App{{
		Machine: m,
		Config: config.AppConfig{
			Mode:          config.ModeBatch,
			MaxLoad:       4.0,
			MaxIOWait:     20,
			MaxConcurrent: 2,
			BatchSize:     50,
		},
	}}
	decisions := []scheduler.Decision{{Name: "wonen31", State: appstate.Idle}}

	a := Snapshot(base, decisions, apps).Apps[0]
	if a.Mode != "batch" || a.MaxLoadCeiling != 4.0 || a.MaxIOWaitCeiling != 20 || a.MaxConcurrent != 2 || a.BatchSize != 50 {
		t.Errorf("static config fields = %+v, want mode=batch max_load_ceiling=4.0 max_iowait_ceiling=20 max_concurrent=2 batch_size=50", a)
	}
}

func TestSnapshot_ActiveRunsFromMachine(t *testing.T) {
	m := appstate.New(2, 0, 0, false)
	t1 := base
	t2 := base.Add(5 * time.Second)
	m.OnDispatch(t1)
	m.RecordRunStarted(100, t1)
	m.OnDispatch(t2)
	m.RecordRunStarted(200, t2)
	apps := []*scheduler.App{{Machine: m}}
	decisions := []scheduler.Decision{{Name: "wonen31", State: appstate.Running}}

	a := Snapshot(base.Add(10*time.Second), decisions, apps).Apps[0]
	if len(a.ActiveRuns) != 2 || a.ActiveRuns[0].PID != 100 || !a.ActiveRuns[0].Start.Equal(t1) ||
		a.ActiveRuns[1].PID != 200 || !a.ActiveRuns[1].Start.Equal(t2) {
		t.Errorf("ActiveRuns = %v, want [{100 %v} {200 %v}]", a.ActiveRuns, t1, t2)
	}
}

func TestSnapshot_ActiveRunsCarryJobAndStatus(t *testing.T) {
	m := appstate.New(1, 0, 0, false)
	m.OnDispatch(base)
	m.RecordRunStarted(100, base)
	m.UpdateActiveRunJob(100, `App\Jobs\ExampleJob`, "starting")
	apps := []*scheduler.App{{Machine: m}}
	decisions := []scheduler.Decision{{Name: "wonen31", State: appstate.Running}}

	a := Snapshot(base, decisions, apps).Apps[0]
	if len(a.ActiveRuns) != 1 || a.ActiveRuns[0].Job != `App\Jobs\ExampleJob` || a.ActiveRuns[0].Status != "starting" {
		t.Errorf("ActiveRuns = %+v, want job/status carried through", a.ActiveRuns)
	}
}

func TestSnapshot_JSONOutputFromConfig(t *testing.T) {
	apps := []*scheduler.App{{
		Machine: appstate.New(1, 0, 0, false),
		Config:  config.AppConfig{JSONOutput: true},
	}}
	decisions := []scheduler.Decision{{Name: "wonen31", State: appstate.Idle}}

	a := Snapshot(base, decisions, apps).Apps[0]
	if !a.JSONOutput {
		t.Error("JSONOutput = false, want true (from app.Config.JSONOutput)")
	}
}

func TestSnapshotStore_DefaultsToEmptyThenUpdates(t *testing.T) {
	s := NewSnapshotStore()
	if got := s.Current(); len(got.Apps) != 0 {
		t.Errorf("initial Current().Apps = %v, want empty", got.Apps)
	}

	snap := Snapshot(base, []scheduler.Decision{{Name: "a"}}, []*scheduler.App{{Machine: appstate.New(1, 0, 0, false)}})
	s.Update(snap)

	got := s.Current()
	if len(got.Apps) != 1 || got.Apps[0].Name != "a" {
		t.Errorf("Current() after Update = %+v, want the updated snapshot", got)
	}
}
