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

// ScheduleEvents converts one schedule tick's decisions (PLAN.md §6.1)
// into wire-format events, Kind "schedule": "dispatched" for a started
// schedule:run, "skipped" with a reason otherwise. State is "running" for
// a started run, "idle" for a skip — it describes the schedule:run, not
// the app's queue FSM. Call after ScheduleTick has updated each app's
// ScheduleState, so the embedded Schedule status is current.
func ScheduleEvents(now time.Time, decisions []scheduler.ScheduleDecision) []ipc.DecisionEvent {
	events := make([]ipc.DecisionEvent, len(decisions))
	for i, d := range decisions {
		e := ipc.DecisionEvent{
			Timestamp: now,
			App:       d.App.Config.Name,
			Kind:      ipc.KindSchedule,
			Load:      d.Load,
			IOWait:    d.IOWait,
			Schedule:  ScheduleStatus(now, d.App),
		}
		if d.Run {
			e.Decision = "dispatched"
			e.State = "running"
		} else {
			e.Decision = "skipped"
			e.State = "idle"
			reason := string(d.Reason)
			e.Reason = &reason
		}
		events[i] = e
	}
	return events
}

// ScheduleCompletedEvent is the Kind "schedule", Decision "completed"
// event for a finished schedule:run — its exit code and duration are in
// the embedded Schedule status.
func ScheduleCompletedEvent(now time.Time, app *scheduler.App) ipc.DecisionEvent {
	return ipc.DecisionEvent{
		Timestamp: now,
		App:       app.Config.Name,
		Kind:      ipc.KindSchedule,
		Decision:  "completed",
		State:     "idle",
		Schedule:  ScheduleStatus(now, app),
	}
}
