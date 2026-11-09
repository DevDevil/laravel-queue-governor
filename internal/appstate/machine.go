// Package appstate implements the per-app state machine (PLAN.md §4):
// idle, running, cooldown, throttled. It never touches metrics or a clock
// directly — callers pass `now` explicitly and pre-computed load/iowait
// booleans (already compared against this app's own thresholds), which is
// what keeps this package trivially testable with a fake clock and free of
// any dependency beyond the stdlib.
package appstate

import "time"

type State int

const (
	Idle State = iota
	Running
	Cooldown
	Throttled
)

func (s State) String() string {
	switch s {
	case Idle:
		return "idle"
	case Running:
		return "running"
	case Cooldown:
		return "cooldown"
	case Throttled:
		return "throttled"
	default:
		return "unknown"
	}
}

// Reason mirrors the vocabulary in PLAN.md §8 that the FSM itself can
// determine. global_load_high, global_iowait_high, global_concurrency and
// not_scheduled are the scheduler's concern (§5 steps 2/3/5) — layered on
// top of, or instead of, what Evaluate returns.
type Reason string

const (
	ReasonNone           Reason = ""
	ReasonAlreadyRunning Reason = "already_running"
	ReasonAppConcurrency Reason = "app_concurrency"
	ReasonMinInterval    Reason = "min_interval"
	ReasonQueueIdle      Reason = "queue_idle"
	ReasonSpikeCooldown  Reason = "spike_cooldown"
	ReasonAppLoadHigh    Reason = "app_load_high"
	ReasonAppIOWaitHigh  Reason = "app_iowait_high"

	// ReasonUnconfirmedWork gates scaling out to more than one concurrent
	// run for a json_output app: a 2nd/3rd slot only opens up once at
	// least one already-running process has confirmed (via its parsed
	// --json status) that it's actually processing a job, not just
	// checking an empty queue. Apps without json_output have no such
	// signal and are never subject to this gate (PLAN.md §7/§8).
	ReasonUnconfirmedWork Reason = "unconfirmed_work"
)

// Result is what Evaluate reports for one tick.
type Result struct {
	State    State
	Eligible bool
	Reason   Reason
}

// ActiveRun is one currently in-flight run: its OS process id, when it
// started, and — for a json_output app once the runner has parsed a
// `--json` line for it — the job class currently (or most recently)
// being processed and its status ("starting", "success",
// "released_after_exception", "failed"). Job/Status stay "" for an app
// that isn't configured with json_output, or before the first event for
// this run has arrived yet (TUI.md apps panel's per-job sub-items).
type ActiveRun struct {
	PID    int
	Start  time.Time
	Job    string
	Status string
}

// Machine is one app's state. Not safe for concurrent use — the scheduler
// owns one per app and drives it from its single tick loop.
type Machine struct {
	maxConcurrent int
	minInterval   time.Duration
	spikeCooldown time.Duration
	jsonOutput    bool

	runningCount    int
	activeRuns      []ActiveRun
	lastDispatch    time.Time
	dispatchSeq     int
	lastRunEnd      time.Time
	haveLastRun     bool
	lastRunWasIdle  bool
	lastRunHadSpike bool
}

// New builds a Machine from an app's own concurrency/timing config
// (PLAN.md §7: max_concurrent, min_interval, spike_cooldown).
func New(maxConcurrent int, minInterval, spikeCooldown time.Duration, jsonOutput bool) *Machine {
	return &Machine{maxConcurrent: clampMaxConcurrent(maxConcurrent), minInterval: minInterval, spikeCooldown: spikeCooldown, jsonOutput: jsonOutput}
}

// clampMaxConcurrent enforces max_concurrent's floor of 1 — shared by New
// and UpdateTunables so the invariant can't drift between them.
func clampMaxConcurrent(n int) int {
	if n < 1 {
		return 1
	}
	return n
}

// UpdateTunables applies new max_concurrent/min_interval/spike_cooldown
// values (e.g. after a config reload, PLAN.md §7/§14) without touching any
// in-flight state — a SIGHUP shouldn't make a busy app suddenly look
// brand new: RunningCount, LastDispatch, DispatchSeq, and any active
// cooldown all carry through unchanged, just measured against the new
// durations from here on.
func (m *Machine) UpdateTunables(maxConcurrent int, minInterval, spikeCooldown time.Duration, jsonOutput bool) {
	m.maxConcurrent = clampMaxConcurrent(maxConcurrent)
	m.minInterval = minInterval
	m.spikeCooldown = spikeCooldown
	m.jsonOutput = jsonOutput
}

// OnDispatch records that a run just started. Callers must only do this
// after Evaluate reported Eligible: true. The run's PID isn't known yet
// at this point (the scheduler decides to dispatch before the runner has
// actually exec'd anything) — RecordRunStarted fills that in once it is.
func (m *Machine) OnDispatch(now time.Time) {
	m.runningCount++
	m.lastDispatch = now
	m.dispatchSeq++
}

// RecordRunStarted attaches a PID to the run most recently dispatched,
// once the runner reports the child process has actually started — a
// short while after OnDispatch, once exec succeeds. If exec fails, this
// is never called for that run: RunningCount was already incremented by
// OnDispatch, but no ActiveRuns entry is added for it, and OnRunComplete
// will still correctly decrement RunningCount when the failure is
// reported back.
func (m *Machine) RecordRunStarted(pid int, start time.Time) {
	m.activeRuns = append(m.activeRuns, ActiveRun{PID: pid, Start: start})
}

// UpdateActiveRunJob attaches the current job/status to the ActiveRun
// with the given pid, once the runner has parsed a `--json` line for it
// (json_output apps only). Unlike OnRunComplete's FIFO pop, this is an
// exact match — the pid comes straight from the same process's own
// stdout, so there's no ambiguity about which run it belongs to. A no-op
// if the pid isn't found (the run may have already completed and been
// popped by the time a trailing event arrives).
func (m *Machine) UpdateActiveRunJob(pid int, job, status string) {
	for i := range m.activeRuns {
		if m.activeRuns[i].PID == pid {
			m.activeRuns[i].Job = job
			m.activeRuns[i].Status = status
			return
		}
	}
}

// anyRunConfirmedWorking is Evaluate's unconfirmed_work gate: true if any
// active run's last known status is "starting" — the present-tense
// definition of "confirmed processing a job right now," not "has ever
// had any status." A run's own terminal status (e.g. "success") does not
// count, even briefly before that process exits and frees its slot.
func (m *Machine) anyRunConfirmedWorking() bool {
	for _, ar := range m.activeRuns {
		if ar.Status == "starting" {
			return true
		}
	}
	return false
}

// DispatchSeq is how many times this app has been dispatched (session-only
// — PLAN.md §13 defaults counters to session-only, not persisted across
// restarts). The scheduler puts this on a dispatched Decision as job_seq
// (PLAN.md §8).
func (m *Machine) DispatchSeq() int {
	return m.dispatchSeq
}

// RunningCount is how many of this app's runs are currently in flight —
// the scheduler sums this across all apps for the global concurrency gate
// (PLAN.md §5).
func (m *Machine) RunningCount() int {
	return m.runningCount
}

// ActiveRuns returns each currently in-flight run (PID + start time),
// oldest first — a defensive copy, safe for callers to retain.
// OnRunComplete can't tell which run just finished (no per-run identity
// flows back from the runner on completion, only on start), so it
// removes the oldest entry as a best-effort approximation — exact for
// the common max_concurrent: 1 case, FIFO for max_concurrent > 1.
func (m *Machine) ActiveRuns() []ActiveRun {
	out := make([]ActiveRun, len(m.activeRuns))
	copy(out, m.activeRuns)
	return out
}

// LastDispatch is when this app was last dispatched — the zero time if
// never. The scheduler uses this for least-recently-served fair-dispatch
// ordering (PLAN.md §5 step 5): a zero value sorts first, so an
// never-dispatched app is treated as maximally overdue.
func (m *Machine) LastDispatch() time.Time {
	return m.lastDispatch
}

// OnRunComplete records that a run just ended. didWork is false when the
// run returned with no evidence of work (the queue_idle heuristic,
// PLAN.md §6); spikeObserved is true if a load/iowait spike was seen
// during/around the run (triggers the spike_cooldown extension, §4).
// started must be false only when the completing run's own exec never
// actually got the child running (so RecordRunStarted was never called
// for it, and it never added an ActiveRuns entry) — otherwise the FIFO pop
// below would discard a still-running sibling's entry instead of its own
// (which was never there to begin with), corrupting ActiveRuns() for
// max_concurrent > 1 apps.
//
// Cooldown gates the whole app, starting only once every concurrent run
// has finished (runningCount reaches 0) — a deliberate simplification for
// max_concurrent > 1, where true per-slot cooldown tracking would add
// real complexity for a case the sample config never actually uses.
func (m *Machine) OnRunComplete(now time.Time, didWork, spikeObserved, started bool) {
	if m.runningCount > 0 {
		m.runningCount--
	}
	if started && len(m.activeRuns) > 0 {
		m.activeRuns = m.activeRuns[1:]
	}
	if m.runningCount == 0 {
		m.lastRunEnd = now
		m.haveLastRun = true
		m.lastRunWasIdle = !didWork
		m.lastRunHadSpike = spikeObserved
	}
}

// Evaluate reports this app's own state, eligibility, and (if not
// eligible) the reason, given the current time and this tick's derived
// load/iowait signals. Evaluate never sees raw metrics — the caller
// already compared them against this app's own max_load/max_iowait.
func (m *Machine) Evaluate(now time.Time, ownLoadHigh, ownIOWaitHigh bool) Result {
	if m.runningCount >= m.maxConcurrent {
		reason := ReasonAppConcurrency
		if m.maxConcurrent == 1 {
			reason = ReasonAlreadyRunning
		}
		return Result{State: Running, Eligible: false, Reason: reason}
	}

	// A json_output app doesn't scale out to a 2nd/3rd concurrent run just
	// because there's an unused slot — it waits until at least one
	// already-running process has confirmed (via its parsed --json
	// status) that it actually picked up a job, not just that it's alive
	// checking an empty queue. Only relevant once something is already
	// running (runningCount > 0); the very first dispatch is never
	// blocked by this. Apps without json_output have no such signal at
	// all, so this never applies to them — behavior unchanged.
	if m.jsonOutput && m.runningCount > 0 && !m.anyRunConfirmedWorking() {
		return Result{State: Running, Eligible: false, Reason: ReasonUnconfirmedWork}
	}

	state := Idle
	if m.runningCount > 0 {
		state = Running
	}

	if m.haveLastRun {
		baseDeadline := m.lastRunEnd.Add(m.minInterval)
		if now.Before(baseDeadline) {
			reason := ReasonMinInterval
			if m.lastRunWasIdle {
				reason = ReasonQueueIdle
			}
			return Result{State: Cooldown, Eligible: false, Reason: reason}
		}

		if m.lastRunHadSpike {
			spikeDeadline := baseDeadline.Add(m.spikeCooldown)
			if now.Before(spikeDeadline) {
				return Result{State: Cooldown, Eligible: false, Reason: ReasonSpikeCooldown}
			}
		}
	}

	if ownLoadHigh {
		return Result{State: Throttled, Eligible: false, Reason: ReasonAppLoadHigh}
	}
	if ownIOWaitHigh {
		return Result{State: Throttled, Eligible: false, Reason: ReasonAppIOWaitHigh}
	}

	return Result{State: state, Eligible: true, Reason: ReasonNone}
}
