// Package daemon assembles and runs the actual daemon loop: it ticks the
// scheduler on an interval, executes dispatched runs, and feeds results
// back to the scheduler and appstate machines.
package daemon

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/DevDevil/laravel-queue-governor/internal/appstate"
	"github.com/DevDevil/laravel-queue-governor/internal/clock"
	"github.com/DevDevil/laravel-queue-governor/internal/config"
	"github.com/DevDevil/laravel-queue-governor/internal/ipc"
	"github.com/DevDevil/laravel-queue-governor/internal/monitor"
	"github.com/DevDevil/laravel-queue-governor/internal/runner"
	"github.com/DevDevil/laravel-queue-governor/internal/scheduler"
)

// runResult is what a dispatched run reports back once it finishes. It
// carries the *scheduler.App pointer it was dispatched against, not an
// index into Scheduler.Apps — a SIGHUP reload can add, remove, or reorder
// that slice while runs are in flight, and a pointer stays valid and
// correctly attributed regardless; an index would not.
type runResult struct {
	app    *scheduler.App
	result runner.RunResult
	err    error
	// started is true iff the runner's OnStart hook fired for this run —
	// i.e. an appstate.ActiveRun entry actually exists for it (via
	// RecordRunStarted). False only when exec itself never got the child
	// running (e.g. the binary couldn't be started) — distinct from a run
	// that started fine and later failed/was killed, which still has an
	// ActiveRun entry to account for. complete() needs this so it doesn't
	// tell OnRunComplete to pop an ActiveRuns entry that was never added.
	started bool
}

// runStart is what a dispatched run reports back as soon as the runner
// has actually started the child process — well before it finishes.
// Like runResult, it carries the *scheduler.App pointer rather than an
// index, for the same reload-safety reason.
type runStart struct {
	app   *scheduler.App
	pid   int
	start time.Time
}

// runJobEvent is what a dispatched run reports back each time the runner
// parses a recognized `--json` line off its stdout (json_output apps
// only). Like runStart/runResult, it carries the *scheduler.App pointer
// rather than an index, for the same reload-safety reason; pid identifies
// which of that app's (possibly several concurrent) ActiveRuns it
// belongs to.
type runJobEvent struct {
	app   *scheduler.App
	pid   int
	event runner.JobEvent
}

// scheduleResult is what a schedule:run (PLAN.md §6.1) reports back once
// it finishes. state is the exact ScheduleState the run was started
// against: if a reload disabled the scheduler for this app in the
// meantime (app.Schedule now nil or a fresh state), the result is
// dropped instead of being attributed to state it doesn't belong to.
type scheduleResult struct {
	app    *scheduler.App
	state  *appstate.ScheduleState
	result runner.RunResult
	err    error
}

// reloadRequest is a validated config waiting to be applied by Run's own
// goroutine.
type reloadRequest struct {
	global config.GlobalConfig
	apps   []config.AppConfig
}

// snapshotRequest asks Run's own goroutine for a cheap, no-I/O clone of the
// current effective config — used by ConfigSnapshot (called from other
// goroutines, e.g. a live-tune request arriving over the socket) so the
// caller can safely read Scheduler.Global/Apps without racing Run's direct,
// unsynchronized field access to the same data.
type snapshotRequest struct {
	resp chan config.Config
}

// Loop owns the running daemon. Background goroutines started for each
// dispatched run only exec the process and time it — they never touch an
// appstate.Machine directly. Only Loop.Run's own goroutine calls
// OnDispatch/OnRunComplete/Scheduler.Reload, via channels, which is what
// keeps Machine and Scheduler.Apps single-writer without a mutex (the
// direction recommended in PLAN.md §13).
type Loop struct {
	Scheduler *scheduler.Scheduler
	Runner    runner.Runner
	Bus       *monitor.Bus
	Snapshots *monitor.SnapshotStore
	Clock     clock.Clock
	Stderr    io.Writer

	CheckInterval time.Duration

	// Refresh is published to after every applied reload (SIGHUP-driven or
	// tune-driven, see applyReload) so already-connected dashboard clients
	// get a fresh hello+snapshot without reconnecting (TUI.md §9.6/§12).
	// nil-safe: New always sets it, but tests constructing a Loop directly
	// may leave it nil.
	Refresh *monitor.RefreshBus

	spikeSeen map[*scheduler.App]bool
	reload    chan reloadRequest
	snapshot  chan snapshotRequest
}

func New(sched *scheduler.Scheduler, run runner.Runner, bus *monitor.Bus, snapshots *monitor.SnapshotStore, clk clock.Clock, stderr io.Writer, checkInterval time.Duration, refresh *monitor.RefreshBus) *Loop {
	return &Loop{
		Scheduler:     sched,
		Runner:        run,
		Bus:           bus,
		Snapshots:     snapshots,
		Clock:         clk,
		Stderr:        stderr,
		CheckInterval: checkInterval,
		Refresh:       refresh,
		spikeSeen:     make(map[*scheduler.App]bool),
		reload:        make(chan reloadRequest, 1),
		snapshot:      make(chan snapshotRequest),
	}
}

// Reload queues an already-validated config to be applied on Run's next
// loop iteration. The caller (cmd/lqg's SIGHUP handler) is responsible
// for loading and validating first — PLAN.md §7/CLAUDE.md: invalid config
// is rejected and the previous config kept, so Reload should only ever
// be called with a config known to be good. This never touches Scheduler
// directly; it just hands the new config to Run's own goroutine.
func (l *Loop) Reload(global config.GlobalConfig, apps []config.AppConfig) {
	l.reload <- reloadRequest{global: global, apps: apps}
}

// ConfigSnapshot returns a clone of the daemon's current effective config
// (Scheduler.Global plus each app's Config), safe to call from any
// goroutine — it blocks until Run's own goroutine services the request, the
// only goroutine allowed to read Scheduler.Global/Apps directly (see the
// Loop doc comment). Used by Tune (tune.go) to get a safe base to
// clone-mutate before validating a proposed live-tune change.
func (l *Loop) ConfigSnapshot() config.Config {
	resp := make(chan config.Config, 1)
	l.snapshot <- snapshotRequest{resp: resp}
	return <-resp
}

// effectiveConfig clones Scheduler.Global and every app's Config — pure
// struct copies, no I/O — safe only when called from Run's own goroutine,
// same rule as every other direct Scheduler field read in this file.
func (l *Loop) effectiveConfig() config.Config {
	apps := make([]config.AppConfig, len(l.Scheduler.Apps))
	for i, a := range l.Scheduler.Apps {
		apps[i] = a.Config
	}
	return config.Config{Global: l.Scheduler.Global, Apps: apps}
}

// Run blocks until ctx is canceled. It never returns an error of its own:
// a Tick failure (e.g. a metrics read error) is logged to Stderr and the
// loop continues (CLAUDE.md: never crash on a single failure).
//
// v1 does not wait for in-flight runs to finish before returning — ctx
// cancellation propagates into runner.Run (exec.CommandContext kills the
// child), and process supervision (systemd, ticket 15) owns the rest of
// shutdown. The results/starts/jobEvents channels are sized to a fixed,
// generous constant (notifyChanCap) rather than Global.MaxConcurrentJobs —
// they're async notification plumbing only, NOT the concurrency gate itself
// (that's Scheduler.Global.MaxConcurrentJobs, read fresh every tick by
// dispatchFairly, which is already reload/tune-safe). Sizing to the
// startup value alone would mean a later live tune raising
// max_concurrent_jobs (Tune, tune.go) hits avoidable backpressure — blocked
// sends, delayed job-start/job-event notifications — even though nothing
// about it is unsafe; a fixed cap sidesteps that. A goroutine finishing
// after Run has already returned can still send its result without
// blocking as long as the buffer isn't full — it just won't be read, and
// gets garbage collected once the goroutine exits.
func (l *Loop) Run(ctx context.Context) {
	results := make(chan runResult, notifyChanCap)
	starts := make(chan runStart, notifyChanCap)
	// jobEvents can carry several events per run (batch mode's several
	// jobs in sequence), not just one — sized generously relative to
	// notifyChanCap rather than reused as-is, so a chatty batch app can't
	// stall waiting for the main loop to drain it mid-tick.
	jobEvents := make(chan runJobEvent, notifyChanCap*jobEventsCapMultiplier)

	scheduleResults := make(chan scheduleResult, notifyChanCap)

	timer := l.Clock.After(l.CheckInterval)
	// scheduleTimer fires when the next app's schedule:run minute comes
	// due (PLAN.md §6.1) — nil (blocks forever in select) when no app has
	// the scheduler enabled, so a config without it behaves exactly as
	// before. Re-armed after every schedule tick and every reload.
	scheduleTimer := l.scheduleTimer()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer:
			l.tick(ctx, results, starts, jobEvents)
			timer = l.Clock.After(l.CheckInterval)
		case <-scheduleTimer:
			l.scheduleTick(ctx, scheduleResults)
			scheduleTimer = l.scheduleTimer()
		case r := <-scheduleResults:
			l.completeSchedule(r)
		case rs := <-starts:
			rs.app.Machine.RecordRunStarted(rs.pid, rs.start)
		case je := <-jobEvents:
			je.app.Machine.UpdateActiveRunJob(je.pid, je.event.Job, je.event.Status)
		case r := <-results:
			l.complete(r)
		case req := <-l.reload:
			l.applyReload(req)
			scheduleTimer = l.scheduleTimer()
		case req := <-l.snapshot:
			req.resp <- l.effectiveConfig()
		}
	}
}

// notifyChanCap bounds the results/starts/jobEvents channel buffers — see
// Run's doc comment for why this is a fixed constant rather than derived
// from Global.MaxConcurrentJobs.
const notifyChanCap = 256

// jobEventsCapMultiplier: batch mode can emit several --json job events in
// quick succession for one run, so jobEvents is sized as a multiple of
// notifyChanCap rather than reusing it directly.
const jobEventsCapMultiplier = 8

func (l *Loop) applyReload(req reloadRequest) {
	l.Scheduler.Reload(req.global, req.apps)
	// Refreshes the config-derived fields of the already-known apps in
	// the snapshot store immediately, rather than waiting up to
	// check_interval seconds for the next tick to do it — see
	// SnapshotStore.PatchAppConfig's doc comment for why this matters
	// once Refresh (below) can push a snapshot mid-stream.
	l.Snapshots.PatchAppConfig(req.apps)
	// check_interval takes effect from the next scheduled tick onward —
	// the currently in-flight timer (already scheduled at the old
	// interval) still fires once more before this applies. socket_path
	// is deliberately NOT re-applied: re-binding a listening socket while
	// clients (e.g. watch) may be connected is out of scope for v1.
	if req.global.CheckInterval > 0 {
		l.CheckInterval = time.Duration(req.global.CheckInterval) * time.Second
	}
	if l.Refresh != nil {
		l.Refresh.Publish()
	}
	fmt.Fprintf(l.Stderr, "config reloaded: %d app(s)\n", len(req.apps))
}

func (l *Loop) tick(ctx context.Context, results chan<- runResult, starts chan<- runStart, jobEvents chan<- runJobEvent) {
	now := l.Clock.Now()
	decisions, err := l.Scheduler.Tick(now)
	if err != nil {
		fmt.Fprintf(l.Stderr, "tick: %v\n", err)
		return
	}

	l.Bus.Publish(monitor.Events(now, decisions, l.Scheduler.Apps))
	l.Snapshots.Update(monitor.Snapshot(now, decisions, l.Scheduler.Apps))

	for i, d := range decisions {
		app := l.Scheduler.Apps[i]

		// Accumulate spike observation for every app with a run in
		// flight (including one dispatched this very tick) — needed at
		// completion time for the spike_cooldown extension (§4), since
		// Reason won't show app_load_high/app_iowait_high for a running
		// app (already_running/app_concurrency wins that check first).
		if app.Machine.RunningCount() > 0 && d.Spike {
			l.spikeSeen[app] = true
		}

		if d.Dispatched {
			l.startRun(ctx, app, results, starts, jobEvents)
		}
	}
}

func (l *Loop) startRun(ctx context.Context, app *scheduler.App, results chan<- runResult, starts chan<- runStart, jobEvents chan<- runJobEvent) {
	spec := runner.BuildSpec(l.Scheduler.Global, app.Config)
	go func() {
		var pid int
		var started bool
		result, err := l.Runner.Run(ctx, spec, runner.RunHooks{
			OnStart: func(p int) {
				pid = p
				started = true
				starts <- runStart{app: app, pid: p, start: l.Clock.Now()}
			},
			OnJobEvent: func(e runner.JobEvent) {
				jobEvents <- runJobEvent{app: app, pid: pid, event: e}
			},
		})
		results <- runResult{app: app, result: result, err: err, started: started}
	}()
}

func (l *Loop) complete(r runResult) {
	now := l.Clock.Now()

	if r.err != nil {
		fmt.Fprintf(l.Stderr, "run failed for %s: %v\n", r.app.Config.Name, r.err)
	}

	// A failed-to-start run (r.err != nil) is treated as "no work done" —
	// there's no dedicated reason in the vocabulary (PLAN.md §8) for a
	// runner-level failure, and it was near-instant with nothing
	// processed, so queue_idle's heuristic (§6) at least isn't
	// contradicted; the error itself is still logged above for operator
	// visibility.
	didWork := r.err == nil && runner.DidWork(r.result)

	spike := l.spikeSeen[r.app]
	if r.app.Machine.RunningCount() <= 1 {
		delete(l.spikeSeen, r.app)
	}

	r.app.Machine.OnRunComplete(now, didWork, spike, r.started)
}

// scheduleTimer arms a timer for the earliest due schedule:run across all
// apps, or returns nil if no app has the scheduler enabled.
func (l *Loop) scheduleTimer() <-chan time.Time {
	now := l.Clock.Now()
	due, ok := l.Scheduler.NextScheduleDue(now)
	if !ok {
		return nil
	}
	d := due.Sub(now)
	if d < 0 {
		d = 0
	}
	return l.Clock.After(d)
}

// scheduleTick resolves every app whose schedule:run minute is due
// (Scheduler.ScheduleTick), publishes a schedule decision event for each,
// and starts the runs it decided on.
func (l *Loop) scheduleTick(ctx context.Context, results chan<- scheduleResult) {
	now := l.Clock.Now()
	decisions := l.Scheduler.ScheduleTick(now)
	if len(decisions) == 0 {
		return
	}

	events := monitor.ScheduleEvents(now, decisions)
	l.Bus.Publish(events)
	for i, d := range decisions {
		l.Snapshots.PatchSchedule(d.App.Config.Name, events[i].Schedule)
		if !d.Run {
			fmt.Fprintf(l.Stderr, "schedule:run for %s skipped: %s\n", d.App.Config.Name, d.Reason)
			continue
		}
		l.startSchedule(ctx, d.App, results)
	}
}

func (l *Loop) startSchedule(ctx context.Context, app *scheduler.App, results chan<- scheduleResult) {
	spec := runner.BuildScheduleSpec(l.Scheduler.Global, app.Config)
	state := app.Schedule
	go func() {
		result, err := l.Runner.Run(ctx, spec, runner.RunHooks{})
		results <- scheduleResult{app: app, state: state, result: result, err: err}
	}()
}

func (l *Loop) completeSchedule(r scheduleResult) {
	name := r.app.Config.Name
	switch {
	case r.err != nil:
		fmt.Fprintf(l.Stderr, "schedule:run failed for %s: %v\n", name, r.err)
	case r.result.ExitCode != 0:
		fmt.Fprintf(l.Stderr, "schedule:run for %s exited %d after %s\n", name, r.result.ExitCode, r.result.Duration.Round(time.Millisecond))
	}

	if r.app.Schedule != r.state {
		return // scheduler disabled (or reset) for this app since the run started
	}
	r.state.OnComplete(r.result.ExitCode, r.result.Duration)
	now := l.Clock.Now()
	ev := monitor.ScheduleCompletedEvent(now, r.app)
	l.Bus.Publish([]ipc.DecisionEvent{ev})
	l.Snapshots.PatchSchedule(name, ev.Schedule)
}
