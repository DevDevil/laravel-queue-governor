package scheduler

import (
	"testing"
	"time"

	"github.com/DevDevil/laravel-queue-governor/internal/appstate"
	"github.com/DevDevil/laravel-queue-governor/internal/config"
	"github.com/DevDevil/laravel-queue-governor/internal/metrics"
)

var base = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// fakeSource is a canned metrics.Source — no real /proc in scheduler tests.
type fakeSource struct {
	m   metrics.Metrics
	err error
}

func (f fakeSource) Sample() (metrics.Metrics, error) { return f.m, f.err }

func testApp(name string) config.AppConfig {
	return config.AppConfig{
		Name:          name,
		MaxLoad:       4.0,
		MaxIOWait:     20,
		MaxConcurrent: 1,
		MinInterval:   2,
		SpikeCooldown: 30,
	}
}

func testGlobal() config.GlobalConfig {
	return config.GlobalConfig{
		MaxSystemLoad:     6.0,
		MaxIOWait:         25,
		MaxConcurrentJobs: 2,
	}
}

func TestTick_AllClearDispatchesAll(t *testing.T) {
	src := fakeSource{m: metrics.Metrics{Load1: 1.0, IOWaitPct: 5}}
	s := New(testGlobal(), []config.AppConfig{testApp("a"), testApp("b")}, src) // budget 2, 2 apps

	decisions, err := s.Tick(base)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range decisions {
		if !d.Dispatched || d.State != appstate.Running || d.Reason != "" {
			t.Errorf("Decision for %s = %+v, want dispatched/running/no-reason", d.Name, d)
		}
	}
}

func TestTick_GlobalLoadHigh(t *testing.T) {
	app := testApp("a")
	app.MaxLoad = 100                                               // own ceiling well above global's, so only the global gate trips
	src := fakeSource{m: metrics.Metrics{Load1: 7.0, IOWaitPct: 5}} // > global.MaxSystemLoad (6.0)
	s := New(testGlobal(), []config.AppConfig{app}, src)

	decisions, err := s.Tick(base)
	if err != nil {
		t.Fatal(err)
	}
	d := decisions[0]
	if d.Dispatched || d.Reason != ReasonGlobalLoadHigh || d.State != appstate.Throttled {
		t.Errorf("Decision = %+v, want {Throttled false global_load_high}", d)
	}
}

func TestTick_GlobalIOWaitHigh(t *testing.T) {
	// Own thresholds are also generous so only the global iowait gate trips.
	global := testGlobal()
	app := testApp("a")
	app.MaxIOWait = 90                                               // own iowait ceiling well above global's 25, so only the global gate trips
	src := fakeSource{m: metrics.Metrics{Load1: 1.0, IOWaitPct: 30}} // > global.MaxIOWait (25), < app.MaxIOWait (90)
	s := New(global, []config.AppConfig{app}, src)

	decisions, err := s.Tick(base)
	if err != nil {
		t.Fatal(err)
	}
	d := decisions[0]
	if d.Dispatched || d.Reason != ReasonGlobalIOWaitHigh {
		t.Errorf("Decision = %+v, want global_iowait_high", d)
	}
}

func TestTick_GlobalLoadPrecedesIOWait(t *testing.T) {
	app := testApp("a")
	app.MaxLoad = 100
	app.MaxIOWait = 90
	src := fakeSource{m: metrics.Metrics{Load1: 7.0, IOWaitPct: 30}} // both global thresholds exceeded
	s := New(testGlobal(), []config.AppConfig{app}, src)

	decisions, err := s.Tick(base)
	if err != nil {
		t.Fatal(err)
	}
	if decisions[0].Reason != ReasonGlobalLoadHigh {
		t.Errorf("Reason = %v, want global_load_high (load checked before iowait)", decisions[0].Reason)
	}
}

func TestTick_GlobalConcurrencyFull(t *testing.T) {
	global := testGlobal()
	global.MaxConcurrentJobs = 1

	appA := testApp("a")
	appB := testApp("b")
	src := fakeSource{m: metrics.Metrics{Load1: 1.0, IOWaitPct: 5}}
	s := New(global, []config.AppConfig{appA, appB}, src)

	// Simulate app "a" already running, consuming the only global slot.
	s.Apps[0].Machine.OnDispatch(base)

	decisions, err := s.Tick(base)
	if err != nil {
		t.Fatal(err)
	}

	// App "a" shows its OWN reason (already_running), not global_concurrency.
	da := decisions[0]
	if da.Dispatched || da.Reason != appstate.ReasonAlreadyRunning {
		t.Errorf("Decision[a] = %+v, want already_running (own reason wins)", da)
	}

	// App "b" is blocked purely by the global cap.
	db := decisions[1]
	if db.Dispatched || db.Reason != ReasonGlobalConcurrency {
		t.Errorf("Decision[b] = %+v, want global_concurrency", db)
	}
}

func TestTick_OwnReasonWinsOverGlobal(t *testing.T) {
	// Global load is fine, but the app is in its own cooldown — must
	// report min_interval, not dispatch.
	src := fakeSource{m: metrics.Metrics{Load1: 1.0, IOWaitPct: 5}}
	s := New(testGlobal(), []config.AppConfig{testApp("a")}, src)

	s.Apps[0].Machine.OnDispatch(base)
	s.Apps[0].Machine.OnRunComplete(base, true, false, true)

	decisions, err := s.Tick(base.Add(1 * time.Second)) // within min_interval (2s)
	if err != nil {
		t.Fatal(err)
	}
	if decisions[0].Dispatched || decisions[0].Reason != appstate.ReasonMinInterval {
		t.Errorf("Decision = %+v, want min_interval", decisions[0])
	}
}

func TestTick_OwnLoadHighIndependentOfGlobal(t *testing.T) {
	// Global load is fine, but this app's own (tighter) ceiling is exceeded.
	global := testGlobal()
	app := testApp("a")
	app.MaxLoad = 1.0
	src := fakeSource{m: metrics.Metrics{Load1: 2.0, IOWaitPct: 5}} // < global (6.0), > app.MaxLoad (1.0)
	s := New(global, []config.AppConfig{app}, src)

	decisions, err := s.Tick(base)
	if err != nil {
		t.Fatal(err)
	}
	if decisions[0].Dispatched || decisions[0].Reason != appstate.ReasonAppLoadHigh {
		t.Errorf("Decision = %+v, want app_load_high", decisions[0])
	}
}

func TestTick_FairDispatch_BudgetLimitedNotScheduled(t *testing.T) {
	global := testGlobal()
	global.MaxConcurrentJobs = 2 // budget 2, but 3 apps eligible
	src := fakeSource{m: metrics.Metrics{Load1: 1.0, IOWaitPct: 5}}
	s := New(global, []config.AppConfig{testApp("a"), testApp("b"), testApp("c")}, src)

	decisions, err := s.Tick(base)
	if err != nil {
		t.Fatal(err)
	}

	dispatched, notScheduled := 0, 0
	for _, d := range decisions {
		switch {
		case d.Dispatched:
			dispatched++
		case d.Reason == ReasonNotScheduled:
			notScheduled++
		default:
			t.Errorf("unexpected decision: %+v", d)
		}
	}
	if dispatched != 2 || notScheduled != 1 {
		t.Errorf("dispatched=%d notScheduled=%d, want 2 and 1", dispatched, notScheduled)
	}

	// The dispatched apps' RunningCount must actually reflect it.
	running := 0
	for _, a := range s.Apps {
		running += a.Machine.RunningCount()
	}
	if running != 2 {
		t.Errorf("total RunningCount = %d, want 2", running)
	}
}

func TestTick_FairDispatch_PriorityOrdering(t *testing.T) {
	global := testGlobal()
	global.MaxConcurrentJobs = 1 // budget 1, both eligible

	low := testApp("low")
	low.Priority = 1
	high := testApp("high")
	high.Priority = 10

	src := fakeSource{m: metrics.Metrics{Load1: 1.0, IOWaitPct: 5}}
	// Insertion order deliberately puts the lower-priority app first, to
	// prove ordering is driven by priority, not config order.
	s := New(global, []config.AppConfig{low, high}, src)

	decisions, err := s.Tick(base)
	if err != nil {
		t.Fatal(err)
	}

	byName := map[string]Decision{}
	for _, d := range decisions {
		byName[d.Name] = d
	}

	if !byName["high"].Dispatched {
		t.Errorf("high-priority app not dispatched: %+v", byName["high"])
	}
	if byName["low"].Dispatched || byName["low"].Reason != ReasonNotScheduled {
		t.Errorf("low-priority app should be not_scheduled: %+v", byName["low"])
	}
}

func TestTick_FairDispatch_LeastRecentlyServedOrdering(t *testing.T) {
	global := testGlobal()
	global.MaxConcurrentJobs = 1 // budget 1, both eligible, same priority

	appA := testApp("a")
	appB := testApp("b")
	src := fakeSource{m: metrics.Metrics{Load1: 1.0, IOWaitPct: 5}}
	s := New(global, []config.AppConfig{appA, appB}, src)

	// "a" was dispatched (and completed) long before "b" was.
	t0 := base
	t1 := base.Add(1 * time.Hour)
	s.Apps[0].Machine.OnDispatch(t0)
	s.Apps[0].Machine.OnRunComplete(t0, true, false, true)
	s.Apps[1].Machine.OnDispatch(t1)
	s.Apps[1].Machine.OnRunComplete(t1, true, false, true)

	// Well past both apps' min_interval (2s) by the time of this tick.
	now := t1.Add(1 * time.Minute)
	decisions, err := s.Tick(now)
	if err != nil {
		t.Fatal(err)
	}

	byName := map[string]Decision{}
	for _, d := range decisions {
		byName[d.Name] = d
	}

	if !byName["a"].Dispatched {
		t.Errorf("least-recently-served app 'a' should be dispatched: %+v", byName["a"])
	}
	if byName["b"].Dispatched || byName["b"].Reason != ReasonNotScheduled {
		t.Errorf("more-recently-served app 'b' should be not_scheduled: %+v", byName["b"])
	}
}

func TestTick_FairDispatch_NeverDispatchedSortsFirst(t *testing.T) {
	global := testGlobal()
	global.MaxConcurrentJobs = 1

	appA := testApp("a") // has run before
	appB := testApp("b") // never dispatched — should be treated as most overdue
	src := fakeSource{m: metrics.Metrics{Load1: 1.0, IOWaitPct: 5}}
	s := New(global, []config.AppConfig{appA, appB}, src)

	t0 := base.Add(-1 * time.Minute)
	s.Apps[0].Machine.OnDispatch(t0)
	s.Apps[0].Machine.OnRunComplete(t0, true, false, true)

	decisions, err := s.Tick(base)
	if err != nil {
		t.Fatal(err)
	}

	byName := map[string]Decision{}
	for _, d := range decisions {
		byName[d.Name] = d
	}

	if !byName["b"].Dispatched {
		t.Errorf("never-dispatched app 'b' should win LRS ordering: %+v", byName["b"])
	}
	if byName["a"].Dispatched {
		t.Errorf("previously-dispatched app 'a' should lose to never-dispatched 'b': %+v", byName["a"])
	}
}

func TestTick_UnconfirmedWork_BlocksSecondSlotUntilFirstRunConfirmsAJob(t *testing.T) {
	app := testApp("a")
	app.MaxConcurrent = 2
	app.JSONOutput = true
	src := fakeSource{m: metrics.Metrics{Load1: 1.0, IOWaitPct: 5}}
	s := New(testGlobal(), []config.AppConfig{app}, src) // global budget 2, room for a 2nd slot

	// Simulate one already-running, unconfirmed process for "a" (no
	// UpdateActiveRunJob yet — status is still "").
	s.Apps[0].Machine.OnDispatch(base)
	s.Apps[0].Machine.RecordRunStarted(100, base)

	decisions, err := s.Tick(base)
	if err != nil {
		t.Fatal(err)
	}
	if decisions[0].Dispatched || decisions[0].Reason != appstate.ReasonUnconfirmedWork {
		t.Fatalf("Decision = %+v, want unconfirmed_work (2nd slot withheld until the 1st confirms a job)", decisions[0])
	}

	// Once the 1st run confirms it's processing a real job, the 2nd slot opens up.
	s.Apps[0].Machine.UpdateActiveRunJob(100, `App\Jobs\ExampleJob`, "starting")

	decisions, err = s.Tick(base)
	if err != nil {
		t.Fatal(err)
	}
	if !decisions[0].Dispatched {
		t.Fatalf("Decision = %+v, want dispatched (2nd slot now available, work confirmed)", decisions[0])
	}
}
