// Package monitor is the event bus that publishes decision events from
// the scheduler for the CLI to consume (PLAN.md §2, §8). It's the only
// thing standing between scheduler.Decision (internal scheduling record)
// and ipc.DecisionEvent (wire format) — that conversion lives here, not
// in ipc (kept a pure schema package) or scheduler (which shouldn't know
// events or JSON exist).
package monitor

import (
	"time"

	"github.com/DevDevil/laravel-queue-governor/internal/ipc"
	"github.com/DevDevil/laravel-queue-governor/internal/scheduler"
)

// Events converts one tick's Decisions into the wire-format DecisionEvent
// list (PLAN.md §8), ready to Publish. `now` is caller-supplied, matching
// the same tick's Scheduler.Tick(now) call — one timestamp for every
// event in the batch. decisions and apps must be the same slice
// Scheduler.Tick was called against, in the same order (see Snapshot).
func Events(now time.Time, decisions []scheduler.Decision, apps []*scheduler.App) []ipc.DecisionEvent {
	// Defends the stated invariant above rather than trusting it blindly:
	// indexing apps[i] past its length would panic and take the whole
	// daemon down (CLAUDE.md: never crash on a single app's failure), so a
	// violation degrades to dropping the unmatched tail instead.
	n := len(decisions)
	if len(apps) < n {
		n = len(apps)
	}
	events := make([]ipc.DecisionEvent, n)
	for i, d := range decisions[:n] {
		e := ipc.DecisionEvent{
			Timestamp:  now,
			App:        d.Name,
			Load:       d.Load,
			IOWait:     d.IOWait,
			State:      d.State.String(),
			ActiveRuns: toIPCActiveRuns(apps[i].Machine.ActiveRuns()),
		}
		if d.Dispatched {
			e.Decision = "dispatched"
			seq := d.JobSeq
			e.JobSeq = &seq
		} else {
			e.Decision = "skipped"
			reason := string(d.Reason)
			e.Reason = &reason
		}
		events[i] = e
	}
	return events
}
