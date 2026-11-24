package daemon

import (
	"bytes"
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/DevDevil/laravel-queue-governor/internal/config"
	"github.com/DevDevil/laravel-queue-governor/internal/metrics"
	"github.com/DevDevil/laravel-queue-governor/internal/monitor"
	"github.com/DevDevil/laravel-queue-governor/internal/runner"
	"github.com/DevDevil/laravel-queue-governor/internal/scheduler"
)

var base = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// fixedClock always reports `t` for Now(), regardless of real wall-clock
// time — deterministic assertions need l.Clock.Now() (used inside
// tick/complete) to match the `now` values the test compares against.
type fixedClock struct{ t time.Time }

func (f fixedClock) Now() time.Time { return f.t }
func (f fixedClock) After(d time.Duration) <-chan time.Time {
	ch := make(chan time.Time, 1)
	ch <- f.t.Add(d)
	return ch
}

type fakeSource struct{ m metrics.Metrics }

func (f fakeSource) Sample() (metrics.Metrics, error) { return f.m, nil }

// fakeRunner records every spec it was called with and returns a canned
// result. If release is non-nil, Run blocks until it's closed (or ctx
// ends) — lets tests hold a "run" open to simulate it still being in
// flight across multiple ticks.
type fakeRunner struct {
	mu              sync.Mutex
	calls           []runner.RunSpec
	release         chan struct{}
	result          runner.RunResult
	err             error
	nextPID         int
	jobEvents       []runner.JobEvent // fired via OnJobEvent, in order, right after OnStart
	failBeforeStart bool              // simulates exec itself failing: never calls hooks.OnStart, returns err immediately
}

func (f *fakeRunner) Run(ctx context.Context, spec runner.RunSpec, hooks runner.RunHooks) (runner.RunResult, error) {
	f.mu.Lock()
	f.calls = append(f.calls, spec)
	f.nextPID++
	pid := f.nextPID
	release := f.release
	events := f.jobEvents
	failBeforeStart := f.failBeforeStart
	f.mu.Unlock()

	if failBeforeStart {
		return runner.RunResult{ExitCode: -1}, f.err
	}

	if hooks.OnStart != nil {
		hooks.OnStart(pid)
	}
	if hooks.OnJobEvent != nil {
		for _, e := range events {
			hooks.OnJobEvent(e)
		}
	}
	if hooks.Output != nil {
		fmt.Fprintf(hooks.Output(pid, "stdout"), "worker says hi\n")
		fmt.Fprintf(hooks.Output(pid, "stderr"), "worker warns\n")
	}

	if release != nil {
		select {
		case <-release:
		case <-ctx.Done():
			return runner.RunResult{}, ctx.Err()
		}
	}
	return f.result, f.err
}

func (f *fakeRunner) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func testGlobal() config.GlobalConfig {
	return config.GlobalConfig{MaxSystemLoad: 6.0, MaxIOWait: 25, MaxConcurrentJobs: 2}
}

func testApp(name string) config.AppConfig {
	return config.AppConfig{
		Name: name, Connection: "redis", Queues: []string{"default"}, Mode: config.ModePerJob,
		MaxLoad: 4.0, MaxIOWait: 20, MaxConcurrent: 1, MinInterval: 2, SpikeCooldown: 30,
	}
}

func newTestLoop(t *testing.T, global config.GlobalConfig, apps []config.AppConfig, src metrics.Source, run runner.Runner) (*Loop, *scheduler.Scheduler) {
	t.Helper()
	sched := scheduler.New(global, apps, src)
	l := New(sched, run, monitor.NewBus(), monitor.NewSnapshotStore(), fixedClock{t: base}, &bytes.Buffer{}, time.Second, monitor.NewRefreshBus())
	return l, sched
}

func TestLoop_TickDispatchesPublishesAndUpdatesSnapshot(t *testing.T) {
	fr := &fakeRunner{result: runner.RunResult{ExitCode: 0, Duration: time.Second}}
	l, sched := newTestLoop(t, testGlobal(), []config.AppConfig{testApp("wonen31")}, fakeSource{m: metrics.Metrics{Load1: 1, IOWaitPct: 5}}, fr)

	ch, unsubscribe := l.Bus.Subscribe(4)
	defer unsubscribe()

	results := make(chan runResult, 2)
	starts := make(chan runStart, 2)
	jobEvents := make(chan runJobEvent, 8)
	l.tick(context.Background(), results, starts, jobEvents)

	if sched.Apps[0].Machine.RunningCount() != 1 {
		t.Fatalf("RunningCount after dispatch = %d, want 1", sched.Apps[0].Machine.RunningCount())
	}

	select {
	case e := <-ch:
		if e.App != "wonen31" || e.Decision != "dispatched" {
			t.Errorf("published event = %+v, want dispatched wonen31", e)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for published event")
	}

	snap := l.Snapshots.Current()
	if len(snap.Apps) != 1 || snap.Apps[0].Name != "wonen31" {
		t.Errorf("snapshot = %+v, want one app wonen31", snap)
	}

	select {
	case r := <-results:
		if r.app != sched.Apps[0] || r.err != nil {
			t.Errorf("runResult = %+v, want sched.Apps[0], no error", r)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for the runner goroutine's result")
	}

	if fr.callCount() != 1 {
		t.Errorf("runner called %d times, want 1", fr.callCount())
	}
}

func TestLoop_TickSendsRunStartWithPID(t *testing.T) {
	fr := &fakeRunner{result: runner.RunResult{ExitCode: 0, Duration: time.Second}}
	l, sched := newTestLoop(t, testGlobal(), []config.AppConfig{testApp("wonen31")}, fakeSource{m: metrics.Metrics{Load1: 1, IOWaitPct: 5}}, fr)

	results := make(chan runResult, 2)
	starts := make(chan runStart, 2)
	jobEvents := make(chan runJobEvent, 8)
	l.tick(context.Background(), results, starts, jobEvents)

	select {
	case rs := <-starts:
		if rs.app != sched.Apps[0] || rs.pid <= 0 {
			t.Errorf("runStart = %+v, want sched.Apps[0] with a positive pid", rs)
		}
		// This is Run's own job normally (its select loop's `case rs :=
		// <-starts` branch) — done manually here since the test drives
		// tick() directly rather than running the full Loop.Run loop.
		rs.app.Machine.RecordRunStarted(rs.pid, rs.start)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for runStart")
	}

	active := sched.Apps[0].Machine.ActiveRuns()
	if len(active) != 1 || active[0].PID <= 0 {
		t.Errorf("ActiveRuns after RecordRunStarted = %+v, want one entry with a positive pid", active)
	}
}

func TestLoop_TickSendsJobEventsRoutedByPID(t *testing.T) {
	fr := &fakeRunner{
		result:    runner.RunResult{ExitCode: 0, Duration: time.Second},
		jobEvents: []runner.JobEvent{{Status: "starting", Job: `App\Jobs\ExampleJob`}},
	}
	l, sched := newTestLoop(t, testGlobal(), []config.AppConfig{testApp("wonen31")}, fakeSource{m: metrics.Metrics{Load1: 1, IOWaitPct: 5}}, fr)

	results := make(chan runResult, 2)
	starts := make(chan runStart, 2)
	jobEvents := make(chan runJobEvent, 8)
	l.tick(context.Background(), results, starts, jobEvents)

	var pid int
	select {
	case rs := <-starts:
		pid = rs.pid
		rs.app.Machine.RecordRunStarted(rs.pid, rs.start)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for runStart")
	}

	select {
	case je := <-jobEvents:
		if je.app != sched.Apps[0] || je.pid != pid || je.event.Status != "starting" {
			t.Fatalf("runJobEvent = %+v, want app=sched.Apps[0] pid=%d status=starting", je, pid)
		}
		// Normally Run's own select loop's job (`case je := <-jobEvents`).
		je.app.Machine.UpdateActiveRunJob(je.pid, je.event.Job, je.event.Status)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for runJobEvent")
	}

	active := sched.Apps[0].Machine.ActiveRuns()
	if len(active) != 1 || active[0].Job != `App\Jobs\ExampleJob` || active[0].Status != "starting" {
		t.Errorf("ActiveRuns after UpdateActiveRunJob = %+v, want job/status applied", active)
	}
}

func TestLoop_CompleteAppliesQueueIdleWhenNoWorkDone(t *testing.T) {
	fr := &fakeRunner{result: runner.RunResult{ExitCode: 0, Duration: 10 * time.Millisecond}} // under the queue_idle threshold
	l, sched := newTestLoop(t, testGlobal(), []config.AppConfig{testApp("wonen31")}, fakeSource{m: metrics.Metrics{Load1: 1, IOWaitPct: 5}}, fr)

	sched.Apps[0].Machine.OnDispatch(base)
	l.complete(runResult{app: sched.Apps[0], result: fr.result})

	// Just past dispatch, still within min_interval — reason should be
	// queue_idle (no work done), not the generic min_interval.
	got := sched.Apps[0].Machine.Evaluate(base.Add(time.Second), false, false)
	if got.Eligible || got.Reason != "queue_idle" {
		t.Errorf("Evaluate after a no-work run = %+v, want queue_idle", got)
	}
}

func TestLoop_CompleteAppliesMinIntervalWhenWorkDone(t *testing.T) {
	fr := &fakeRunner{result: runner.RunResult{ExitCode: 0, Duration: time.Second}} // over the queue_idle threshold
	l, sched := newTestLoop(t, testGlobal(), []config.AppConfig{testApp("wonen31")}, fakeSource{m: metrics.Metrics{Load1: 1, IOWaitPct: 5}}, fr)

	sched.Apps[0].Machine.OnDispatch(base)
	l.complete(runResult{app: sched.Apps[0], result: fr.result})

	got := sched.Apps[0].Machine.Evaluate(base.Add(time.Second), false, false)
	if got.Eligible || got.Reason != "min_interval" {
		t.Errorf("Evaluate after a real run = %+v, want min_interval", got)
	}
}

func TestLoop_RunnerErrorTreatedAsNoWorkDone(t *testing.T) {
	fr := &fakeRunner{}
	l, sched := newTestLoop(t, testGlobal(), []config.AppConfig{testApp("wonen31")}, fakeSource{m: metrics.Metrics{Load1: 1, IOWaitPct: 5}}, fr)

	sched.Apps[0].Machine.OnDispatch(base)
	l.complete(runResult{app: sched.Apps[0], result: runner.RunResult{Duration: 5 * time.Second}, err: context.DeadlineExceeded})

	got := sched.Apps[0].Machine.Evaluate(base.Add(time.Second), false, false)
	if got.Eligible || got.Reason != "queue_idle" {
		t.Errorf("Evaluate after a failed run = %+v, want queue_idle (failure treated as no-work)", got)
	}
}

// TestLoop_Complete_UnstartedRunDoesNotCorruptSiblingsActiveRuns is the
// daemon-level regression test for the bug fixed by threading runResult's
// started field through to appstate.Machine.OnRunComplete: a run whose
// exec never actually started (started: false) must not pop a genuinely
// in-flight sibling's ActiveRuns entry when it "completes."
func TestLoop_Complete_UnstartedRunDoesNotCorruptSiblingsActiveRuns(t *testing.T) {
	fr := &fakeRunner{}
	app := testApp("wonen31")
	app.MaxConcurrent = 2
	l, sched := newTestLoop(t, testGlobal(), []config.AppConfig{app}, fakeSource{m: metrics.Metrics{Load1: 1, IOWaitPct: 5}}, fr)
	appPtr := sched.Apps[0]

	appPtr.Machine.OnDispatch(base)
	appPtr.Machine.RecordRunStarted(100, base) // run A actually started
	appPtr.Machine.OnDispatch(base)            // run B dispatched, but its exec fails

	l.complete(runResult{app: appPtr, result: runner.RunResult{ExitCode: -1}, err: context.DeadlineExceeded, started: false})

	got := appPtr.Machine.ActiveRuns()
	if len(got) != 1 || got[0].PID != 100 {
		t.Fatalf("ActiveRuns after the unstarted run's completion = %v, want run A (pid 100) still present", got)
	}
	if appPtr.Machine.RunningCount() != 1 {
		t.Errorf("RunningCount = %d, want 1 (run A still in flight)", appPtr.Machine.RunningCount())
	}
}

// TestLoop_Complete_SpikeSurvivesNonFinalCompletionForConcurrentRuns is the
// regression test for spikeSeen being deleted on every completion instead
// of only the one that brings RunningCount to 0: for a max_concurrent > 1
// app, a spike observed while two runs are in flight must still trigger
// spike_cooldown at the batch's real last completion, not get silently
// dropped by an earlier, non-final one.
func TestLoop_Complete_SpikeSurvivesNonFinalCompletionForConcurrentRuns(t *testing.T) {
	fr := &fakeRunner{}
	app := testApp("wonen31")
	app.MaxConcurrent = 2
	l, sched := newTestLoop(t, testGlobal(), []config.AppConfig{app}, fakeSource{m: metrics.Metrics{Load1: 1, IOWaitPct: 5}}, fr)
	appPtr := sched.Apps[0]

	appPtr.Machine.OnDispatch(base)
	appPtr.Machine.RecordRunStarted(100, base)
	appPtr.Machine.OnDispatch(base)
	appPtr.Machine.RecordRunStarted(200, base)

	l.spikeSeen[appPtr] = true // a spike was observed while both runs were in flight

	// First, non-final completion: RunningCount goes 2 -> 1.
	l.complete(runResult{app: appPtr, result: runner.RunResult{Duration: time.Second}, started: true})
	if got := appPtr.Machine.RunningCount(); got != 1 {
		t.Fatalf("RunningCount after first completion = %d, want 1", got)
	}

	// Second, final completion: RunningCount reaches 0 — the spike must
	// still be there to extend cooldown with spike_cooldown, not have been
	// wiped by the first completion above.
	l.complete(runResult{app: appPtr, result: runner.RunResult{Duration: time.Second}, started: true})

	got := appPtr.Machine.Evaluate(base.Add(3*time.Second), false, false)
	if got.Eligible || got.Reason != "spike_cooldown" {
		t.Errorf("Evaluate after both completions = %+v, want spike_cooldown (spike must survive the non-final completion)", got)
	}
}

func TestLoop_SpikeAccumulatesAcrossTicksWhileRunInFlight(t *testing.T) {
	release := make(chan struct{})
	fr := &fakeRunner{release: release, result: runner.RunResult{Duration: time.Second}}
	app := testApp("wonen31")
	// low own ceiling so a later tick's load reading counts as a spike for this app
	app.MaxLoad = 1.0

	// First tick: clear load, dispatches and starts the (blocked) run.
	src := &mutableSource{m: metrics.Metrics{Load1: 0.5, IOWaitPct: 5}}
	l, sched := newTestLoop(t, testGlobal(), []config.AppConfig{app}, src, fr)

	results := make(chan runResult, 2)
	starts := make(chan runStart, 2)
	jobEvents := make(chan runJobEvent, 8)
	l.tick(context.Background(), results, starts, jobEvents)
	if sched.Apps[0].Machine.RunningCount() != 1 {
		t.Fatalf("RunningCount = %d, want 1 after first tick", sched.Apps[0].Machine.RunningCount())
	}

	// Second tick: load now exceeds this app's own ceiling while its run
	// is still in flight (release hasn't been closed yet).
	src.set(metrics.Metrics{Load1: 3.0, IOWaitPct: 5})
	l.tick(context.Background(), results, starts, jobEvents)

	if !l.spikeSeen[sched.Apps[0]] {
		t.Fatal("spikeSeen[app] = false, want true after a high-load tick during an in-flight run")
	}

	// Let the run finish and process its result.
	close(release)
	select {
	case r := <-results:
		l.complete(r)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the run to finish")
	}

	// min_interval (2s) has elapsed but spike_cooldown (30s) hasn't — the
	// spike observed mid-run must have carried through to OnRunComplete.
	got := sched.Apps[0].Machine.Evaluate(base.Add(3*time.Second), false, false)
	if got.Eligible || got.Reason != "spike_cooldown" {
		t.Errorf("Evaluate after the run completed = %+v, want spike_cooldown", got)
	}
}

// mutableSource lets a test change the metrics.Metrics returned between
// ticks without rebuilding the Scheduler.
type mutableSource struct {
	mu sync.Mutex
	m  metrics.Metrics
}

func (s *mutableSource) Sample() (metrics.Metrics, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.m, nil
}

func (s *mutableSource) set(m metrics.Metrics) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m = m
}
