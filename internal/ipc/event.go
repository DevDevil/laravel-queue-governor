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
	Decision  string    `json:"decision"` // "dispatched" | "skipped"
	Reason    *string   `json:"reason"`
	JobSeq    *int      `json:"job_seq"`
	Load      float64   `json:"load"`
	IOWait    float64   `json:"iowait"`
	State     string    `json:"state"`

	// ActiveRuns is each currently in-flight run for this app (TUI.md
	// apps panel's per-job sub-items) — omitempty since most ticks for
	// most apps have none.
	ActiveRuns []ActiveRun `json:"active_runs,omitempty"`
}
