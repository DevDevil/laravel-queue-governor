package ipc

import "time"

// MetricSample is a lightweight, independently-paced load/iowait reading
// (PLAN.md §8 / ticket 16 follow-up) — sampled on its own fixed ~1s
// cadence, decoupled from the scheduler's check_interval-driven decision
// ticks, so `lqg watch`'s graphs can update smoothly regardless of how
// slow check_interval is configured. Requested via {"cmd":"metrics"}: the
// daemon sends its buffered history first (oldest first), then streams
// live samples as they're produced.
type MetricSample struct {
	Ts          time.Time `json:"ts"`
	Load        float64   `json:"load"`
	LoadPerCore float64   `json:"load_per_core"`
	IOWait      float64   `json:"iowait"`

	// MaxLoad/MaxIOWait are the current global.max_system_load/
	// max_iowait ceilings (ticket 16 Phase 3) — duplicated onto every
	// sample, the same precedent as scheduler.Decision.Load/IOWait
	// duplicating the tick's shared global reading onto every app. Lets
	// the client compute "% of ceiling" for threshold-based colour
	// (green under ~70%, amber approaching, red over) without a second,
	// differently-shaped message on this stream.
	MaxLoad   float64 `json:"max_load"`
	MaxIOWait float64 `json:"max_iowait"`
}
