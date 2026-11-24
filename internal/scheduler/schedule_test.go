package scheduler

import (
	"testing"
	"time"

	"github.com/DevDevil/laravel-queue-governor/internal/appstate"
	"github.com/DevDevil/laravel-queue-governor/internal/config"
	"github.com/DevDevil/laravel-queue-governor/internal/metrics"
)

func schedApp(name string, respectLoad bool) config.AppConfig {
	a := testApp(name)
	a.Scheduler = config.SchedulerConfig{Enabled: true, RespectLoad: respectLoad}
	return a
}

func TestScheduleOffset(t *testing.T) {
	a := testApp("shop-a")
	if got := ScheduleOffset(a); got != 0 {
		t.Errorf("offset without stagger = %v, want 0", got)
	}
	a.Scheduler.Stagger = true
	got := ScheduleOffset(a)
	if got < 0 || got >= time.Minute || got%time.Second != 0 {
		t.Errorf("offset = %v, want whole seconds in [0,60s)", got)
	}
	if again := ScheduleOffset(a); again != got {
		t.Errorf("offset not stable: %v vs %v", got, again)
	}
}

func TestNew_ScheduleOnlyWhenEnabled(t *testing.T) {
	s := New(testGlobal(), []config.AppConfig{testApp("a"), schedApp("b", false)}, fakeSource{})
	if s.Apps[0].Schedule != nil {
		t.Error("app a has Schedule without scheduler.enabled")
	}
	if s.Apps[1].Schedule == nil {
		t.Error("app b has no Schedule with scheduler.enabled")
	}
	if _, ok := s.NextScheduleDue(base); !ok {
		t.Error("NextScheduleDue ok=false with an enabled app")
	}
}

func TestScheduleTick_StartsWhenDueIgnoringLoad(t *testing.T) {
	src := fakeSource{m: metrics.Metrics{Load1: 50, IOWaitPct: 90}} // everything over every ceiling
	s := New(testGlobal(), []config.AppConfig{schedApp("a", false)}, src)
	if _, err := s.Tick(base); err != nil {
		t.Fatal(err)
	}

	ds := s.ScheduleTick(base) // base is :00, offset 0 → due
	if len(ds) != 1 || !ds[0].Run {
		t.Fatalf("decisions = %+v, want one Run despite high load (respect_load off)", ds)
	}
	if !s.Apps[0].Schedule.Running() {
		t.Error("Schedule not marked running")
	}
	if ds := s.ScheduleTick(base.Add(time.Second)); len(ds) != 0 {
		t.Errorf("second tick in same minute produced %+v, want nothing", ds)
	}
}

func TestScheduleTick_OverlapSkips(t *testing.T) {
	s := New(testGlobal(), []config.AppConfig{schedApp("a", false)}, fakeSource{})
	s.ScheduleTick(base) // starts, never completes

	ds := s.ScheduleTick(base.Add(time.Minute))
	if len(ds) != 1 || ds[0].Run || ds[0].Reason != appstate.ReasonScheduleOverlap {
		t.Fatalf("decisions = %+v, want schedule_overlap skip", ds)
	}
}

func TestScheduleTick_RespectLoad(t *testing.T) {
	cases := []struct {
		name string
		m    metrics.Metrics
		want appstate.Reason
	}{
		{"own load", metrics.Metrics{Load1: 5}, appstate.ReasonAppLoadHigh},
		{"own iowait", metrics.Metrics{Load1: 1, IOWaitPct: 21}, appstate.ReasonAppIOWaitHigh},
		{"clear", metrics.Metrics{Load1: 1, IOWaitPct: 5}, appstate.ReasonNone},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := New(testGlobal(), []config.AppConfig{schedApp("a", true)}, fakeSource{m: c.m})
			if _, err := s.Tick(base); err != nil {
				t.Fatal(err)
			}
			// The queue tick above dispatched app a; free its slot so only
			// load decides.
			s.Apps[0].Machine.OnRunComplete(base, true, false, false)

			ds := s.ScheduleTick(base)
			if len(ds) != 1 || ds[0].Reason != c.want || ds[0].Run != (c.want == appstate.ReasonNone) {
				t.Errorf("decisions = %+v, want reason %q", ds, c.want)
			}
		})
	}
}

func TestScheduleTick_RespectLoadGlobalConcurrency(t *testing.T) {
	g := testGlobal()
	g.MaxConcurrentJobs = 1
	busy := testApp("busy")
	s := New(g, []config.AppConfig{busy, schedApp("a", true)}, fakeSource{})
	s.Apps[0].Machine.OnDispatch(base) // the one global slot is taken

	ds := s.ScheduleTick(base)
	if len(ds) != 1 || ds[0].Reason != ReasonGlobalConcurrency {
		t.Fatalf("decisions = %+v, want global_concurrency", ds)
	}
}

func TestGlobalGate_CountsRunningSchedule(t *testing.T) {
	g := testGlobal()
	g.MaxConcurrentJobs = 1
	s := New(g, []config.AppConfig{schedApp("a", false), testApp("b")}, fakeSource{})
	s.ScheduleTick(base) // a's schedule:run now occupies the only slot

	ds, err := s.Tick(base)
	if err != nil {
		t.Fatal(err)
	}
	if ds[1].Dispatched || ds[1].Reason != ReasonGlobalConcurrency {
		t.Errorf("app b decision = %+v, want global_concurrency while a schedule:run runs", ds[1])
	}
}

func TestReload_SchedulerEnableDisableKeepsState(t *testing.T) {
	s := New(testGlobal(), []config.AppConfig{testApp("a")}, fakeSource{})

	s.Reload(testGlobal(), []config.AppConfig{schedApp("a", false)})
	st := s.Apps[0].Schedule
	if st == nil {
		t.Fatal("enable via reload: Schedule still nil")
	}
	s.ScheduleTick(base)

	s.Reload(testGlobal(), []config.AppConfig{schedApp("a", true)})
	if s.Apps[0].Schedule != st || !st.Running() {
		t.Error("reload with scheduler still enabled replaced/reset its state")
	}

	s.Reload(testGlobal(), []config.AppConfig{testApp("a")})
	if s.Apps[0].Schedule != nil {
		t.Error("disable via reload: Schedule not dropped")
	}
	if _, ok := s.NextScheduleDue(base); ok {
		t.Error("NextScheduleDue ok=true with no enabled app")
	}
}
