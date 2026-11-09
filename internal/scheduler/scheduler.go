// Package scheduler is the single decision point each tick (PLAN.md §2,
// §5): it evaluates every app's own state, layers the global
// load/iowait/concurrency gate on top of whatever clears its own checks,
// then fairly dispatches among what's left. It only decides — internal/daemon
// is what actually runs a dispatch and publishes the resulting decision
// events over the wire.
package scheduler

import (
	"sort"
	"time"

	"github.com/DevDevil/laravel-queue-governor/internal/appstate"
	"github.com/DevDevil/laravel-queue-governor/internal/config"
	"github.com/DevDevil/laravel-queue-governor/internal/metrics"
)

// Scheduler-level reasons (PLAN.md §8) layered on top of appstate's own
// reasons — these only ever apply to an app that already cleared its own
// state (§5). Typed as appstate.Reason (not a separate untyped string
// const here) so Decision.Reason has exactly one type across both
// packages — nothing short of that stops the fixed PLAN.md §8 vocabulary
// from drifting between the two as it grows.
const (
	ReasonGlobalLoadHigh    appstate.Reason = "global_load_high"
	ReasonGlobalIOWaitHigh  appstate.Reason = "global_iowait_high"
	ReasonGlobalConcurrency appstate.Reason = "global_concurrency"
	ReasonNotScheduled      appstate.Reason = "not_scheduled"
)

// App pairs one app's config with its own FSM.
type App struct {
	Config  config.AppConfig
	Machine *appstate.Machine
}

// Decision is this app's final outcome for one tick — a self-contained
// record of everything PLAN.md §8's decision event needs, so converting
// it to the wire format (ticket 10) is a pure mapping with no extra state
// to thread through.
type Decision struct {
	Name       string
	State      appstate.State
	Dispatched bool
	Reason     appstate.Reason // empty when Dispatched
	JobSeq     int             // valid only when Dispatched (PLAN.md §8)
	Load       float64
	IOWait     float64

	// Spike is whether THIS app's own max_load/max_iowait was exceeded
	// this tick, independent of Reason/Dispatched — Reason won't show
	// app_load_high/app_iowait_high while the app is already running
	// (already_running/app_concurrency wins that check first, §5), but
	// the daemon loop still needs to know a spike happened *during* an
	// in-flight run to accumulate it for OnRunComplete's spike_cooldown
	// extension (§4) once the run finishes.
	Spike bool
}

// Scheduler holds one App per configured app plus the shared metrics
// source polled once per tick.
type Scheduler struct {
	Global config.GlobalConfig
	Apps   []*App
	Source metrics.Source
}

// New builds a Scheduler with one appstate.Machine per app, seeded from
// each app's own concurrency/timing config (PLAN.md §7).
func New(global config.GlobalConfig, apps []config.AppConfig, source metrics.Source) *Scheduler {
	as := make([]*App, len(apps))
	for i, a := range apps {
		as[i] = &App{
			Config: a,
			Machine: appstate.New(
				a.MaxConcurrent,
				time.Duration(a.MinInterval)*time.Second,
				time.Duration(a.SpikeCooldown)*time.Second,
				a.JSONOutput,
			),
		}
	}
	return &Scheduler{Global: global, Apps: as, Source: source}
}

// Reload applies a newly validated config (PLAN.md §7/§14 — SIGHUP
// reload; the caller has already rejected an invalid one and kept the
// old config). Apps matched by name keep their existing *App/Machine —
// preserving RunningCount, cooldown, and dispatch history — with only
// Config and the Machine's tunables updated; new names get a fresh
// Machine. Apps missing from the new list are simply not included in the
// result: any in-flight run for one is unaffected, since callers hold
// the *App pointer directly rather than an index into Apps (ticket 14 —
// this is exactly why: an index would silently point at the wrong app,
// or panic, once Apps has been reshaped).
//
// Not the caller's goroutine's job to call this directly against a
// running Scheduler — the daemon loop applies it from its own single
// tick goroutine (internal/daemon), so Apps stays single-writer.
func (s *Scheduler) Reload(global config.GlobalConfig, apps []config.AppConfig) {
	existing := make(map[string]*App, len(s.Apps))
	for _, a := range s.Apps {
		existing[a.Config.Name] = a
	}

	next := make([]*App, len(apps))
	for i, cfg := range apps {
		minInterval := time.Duration(cfg.MinInterval) * time.Second
		spikeCooldown := time.Duration(cfg.SpikeCooldown) * time.Second

		if a, ok := existing[cfg.Name]; ok {
			a.Config = cfg
			a.Machine.UpdateTunables(cfg.MaxConcurrent, minInterval, spikeCooldown, cfg.JSONOutput)
			next[i] = a
		} else {
			next[i] = &App{
				Config:  cfg,
				Machine: appstate.New(cfg.MaxConcurrent, minInterval, spikeCooldown, cfg.JSONOutput),
			}
		}
	}

	s.Global = global
	s.Apps = next
}

// candidate is an eligible app awaiting fair dispatch.
type candidate struct {
	index    int
	priority int
	lastRun  time.Time
}

// globalGate is the tick's global load/iowait/concurrency check (PLAN.md
// §5 step 2), computed once per tick from one metrics sample and consulted
// by every app's own evaluateApp call.
type globalGate struct {
	loadHigh        bool
	iowaitHigh      bool
	concurrencyFull bool
	totalRunning    int
}

func (s *Scheduler) globalGate(m metrics.Metrics) globalGate {
	totalRunning := 0
	for _, a := range s.Apps {
		totalRunning += a.Machine.RunningCount()
	}
	return globalGate{
		loadHigh:        m.Load1 > s.Global.MaxSystemLoad,
		iowaitHigh:      m.IOWaitPct > float64(s.Global.MaxIOWait),
		concurrencyFull: totalRunning >= s.Global.MaxConcurrentJobs,
		totalRunning:    totalRunning,
	}
}

// evaluateApp is PLAN.md §5 steps 1+2 for one app: its own state always
// wins if ineligible; otherwise the global gate applies; otherwise it's a
// fair-dispatch candidate, which the caller collects across all apps
// before ranking (dispatchFairly), since ranking needs to compare
// candidates against each other.
func (s *Scheduler) evaluateApp(now time.Time, a *App, m metrics.Metrics, gate globalGate) (Decision, bool) {
	ownLoadHigh := m.Load1 > a.Config.MaxLoad
	ownIOWaitHigh := m.IOWaitPct > float64(a.Config.MaxIOWait)

	result := a.Machine.Evaluate(now, ownLoadHigh, ownIOWaitHigh)
	d := Decision{
		Name: a.Config.Name, State: result.State,
		Load: m.Load1, IOWait: m.IOWaitPct,
		Spike: ownLoadHigh || ownIOWaitHigh,
	}

	switch {
	case !result.Eligible:
		// This app's own state disqualifies it — that reason always
		// wins, regardless of global conditions (PLAN.md §5).
		d.Reason = result.Reason
		return d, false
	case gate.loadHigh:
		d.State = appstate.Throttled
		d.Reason = ReasonGlobalLoadHigh
		return d, false
	case gate.iowaitHigh:
		d.State = appstate.Throttled
		d.Reason = ReasonGlobalIOWaitHigh
		return d, false
	case gate.concurrencyFull:
		d.State = appstate.Throttled
		d.Reason = ReasonGlobalConcurrency
		return d, false
	default:
		return d, true
	}
}

// dispatchFairly is PLAN.md §5 step 3: rank the apps that cleared their own
// state and the global gate by priority then least-recently-served, and
// dispatch up to the remaining concurrency budget — the rest get
// ReasonNotScheduled. Mutates decisions in place and calls OnDispatch on
// each dispatched app's Machine.
func (s *Scheduler) dispatchFairly(now time.Time, decisions []Decision, candidates []candidate, totalRunning int) {
	// Higher priority first, then least-recently-served (oldest
	// LastDispatch, zero value for "never" sorts first) as the tiebreak —
	// round-robin among equal-priority apps so one busy app can't starve
	// the others.
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].priority != candidates[j].priority {
			return candidates[i].priority > candidates[j].priority
		}
		return candidates[i].lastRun.Before(candidates[j].lastRun)
	})

	budget := s.Global.MaxConcurrentJobs - totalRunning
	if budget < 0 {
		budget = 0
	}

	for rank, c := range candidates {
		if rank < budget {
			s.Apps[c.index].Machine.OnDispatch(now)
			decisions[c.index].Dispatched = true
			decisions[c.index].State = appstate.Running
			decisions[c.index].JobSeq = s.Apps[c.index].Machine.DispatchSeq()
		} else {
			decisions[c.index].Reason = ReasonNotScheduled
		}
	}
}

// Tick is the single decision point for one scheduler tick (PLAN.md §5):
// evaluate every app's own state, layer the global gate on top, then
// fairly dispatch among what's left, up to the remaining concurrency
// budget. `now` is caller-supplied — no time.Now() here — so tests drive
// this deterministically with a fixed clock and a fake metrics.Source.
//
// Dispatching here means calling each chosen app's Machine.OnDispatch —
// the FSM bookkeeping for "this app is now running". Actually invoking
// the runner is ticket 9's job, decoupled entirely: it feeds the result
// back later via Machine.OnRunComplete.
func (s *Scheduler) Tick(now time.Time) ([]Decision, error) {
	m, err := s.Source.Sample()
	if err != nil {
		return nil, err
	}

	gate := s.globalGate(m)

	decisions := make([]Decision, len(s.Apps))
	var candidates []candidate
	for i, a := range s.Apps {
		d, isCandidate := s.evaluateApp(now, a, m, gate)
		decisions[i] = d
		if isCandidate {
			candidates = append(candidates, candidate{
				index:    i,
				priority: a.Config.Priority,
				lastRun:  a.Machine.LastDispatch(),
			})
		}
	}

	s.dispatchFairly(now, decisions, candidates, gate.totalRunning)

	return decisions, nil
}
