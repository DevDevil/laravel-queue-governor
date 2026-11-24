// Package ipc is the wire-format contract between the daemon (monitor)
// and the client (cli) — plain types only, no scheduling logic, so
// changing the protocol means changing it in exactly one place (CLAUDE.md
// §"Package layout").
package ipc

import "time"

// DecisionEvent is emitted once per app per scheduler tick (PLAN.md §8) —
// dispatched or skipped, always with a reason when skipped. Reason and
// JobSeq use pointers rather than omitempty so an inapplicable field
// serializes as explicit JSON null (matching PLAN.md §8's example, which
// shows "reason": null on a dispatched event) rather than disappearing.
type DecisionEvent struct {
	Timestamp time.Time `json:"ts"`
	App       string    `json:"app"`

	// Kind distinguishes a schedule:run decision ("schedule", PLAN.md
	// §6.1) from a queue-worker decision (omitted/""). Schedule events
	// are only emitted when an app's minute comes due, not every tick,
	// and must not be applied to the app's queue state/reason by clients.
	Kind string `json:"kind,omitempty"`

	// Schedule is the app's schedule:run status right after this event
	// (Kind "schedule" only), so clients can update it without waiting
	// for the next snapshot.
	Schedule *ScheduleStatus `json:"schedule,omitempty"`

	Decision string  `json:"decision"` // "dispatched" | "skipped" | "completed" (Kind "schedule" only)
	Reason   *string `json:"reason"`
	JobSeq   *int    `json:"job_seq"`
	Load     float64 `json:"load"`
	IOWait   float64 `json:"iowait"`
	State    string  `json:"state"`

	// ActiveRuns is each currently in-flight run for this app (TUI.md
	// apps panel's per-job sub-items) — omitempty since most ticks for
	// most apps have none.
	ActiveRuns []ActiveRun `json:"active_runs,omitempty"`
}

// KindSchedule marks a DecisionEvent as a schedule:run decision.
const KindSchedule = "schedule"
