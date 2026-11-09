package monitor

import (
	"time"

	"github.com/DevDevil/laravel-queue-governor/internal/appstate"
	"github.com/DevDevil/laravel-queue-governor/internal/ipc"
	"github.com/DevDevil/laravel-queue-governor/internal/scheduler"
)

// toIPCActiveRuns converts appstate's internal ActiveRun records to the
// wire-format shape, shared by Snapshot and Events (both convert the same
// Machine.ActiveRuns() data, just into different frame types).
func toIPCActiveRuns(ars []appstate.ActiveRun) []ipc.ActiveRun {
	out := make([]ipc.ActiveRun, len(ars))
	for i, ar := range ars {
		out[i] = ipc.ActiveRun{PID: ar.PID, Start: ar.Start, Job: ar.Job, Status: ar.Status}
	}
	return out
}

// Snapshot builds the `status` response (PLAN.md §8) from one tick's
// Decisions plus the Scheduler's own Apps slice — Decision alone doesn't
// carry LastDispatch/DispatchSeq, since those persist across ticks rather
// than being per-tick facts. decisions and apps must be the same slice
// Scheduler.Tick was called against, in the same order (Tick always
// builds decisions by iterating s.Apps, so this holds for any direct
// Tick call).
func Snapshot(now time.Time, decisions []scheduler.Decision, apps []*scheduler.App) ipc.StatusSnapshot {
	// Same defensive degrade as Events (monitor/events.go) — see its
	// comment for why this can't just trust the stated invariant.
	n := len(decisions)
	if len(apps) < n {
		n = len(apps)
	}
	statuses := make([]ipc.AppStatus, n)
	for i, d := range decisions[:n] {
		var reason *string
		if !d.Dispatched && d.Reason != "" {
			r := string(d.Reason)
			reason = &r
		}

		var lastDispatch *time.Time
		if ld := apps[i].Machine.LastDispatch(); !ld.IsZero() {
			lastDispatch = &ld
		}

		statuses[i] = ipc.AppStatus{
			Name:         d.Name,
			State:        d.State.String(),
			Load:         d.Load,
			IOWait:       d.IOWait,
			JobsSession:  apps[i].Machine.DispatchSeq(),
			LastDispatch: lastDispatch,
			Reason:       reason,

			Mode:             string(apps[i].Config.Mode),
			MaxLoadCeiling:   apps[i].Config.MaxLoad,
			MaxIOWaitCeiling: apps[i].Config.MaxIOWait,
			MaxConcurrent:    apps[i].Config.MaxConcurrent,
			BatchSize:        apps[i].Config.BatchSize,
			ActiveRuns:       toIPCActiveRuns(apps[i].Machine.ActiveRuns()),
			JSONOutput:       apps[i].Config.JSONOutput,

			MinInterval:   apps[i].Config.MinInterval,
			SpikeCooldown: apps[i].Config.SpikeCooldown,
			Priority:      apps[i].Config.Priority,
			Nice:          apps[i].Config.Nice,
		}
	}
	return ipc.StatusSnapshot{Ts: now, Apps: statuses}
}
