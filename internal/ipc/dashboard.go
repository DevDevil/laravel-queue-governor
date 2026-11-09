package ipc

// The full-screen dashboard (TUI.md) multiplexes several payload shapes
// over one connection ({"cmd":"dashboard"}), unlike watch/metrics which
// are each monomorphic streams. Every frame embeds its existing payload
// type plus a Type discriminator; encoding/json inlines embedded struct
// fields, so e.g. a SampleFrame marshals as
// {"type":"sample","ts":...,"load":...} with no field duplication.
const (
	FrameHello    = "hello"
	FrameHistory  = "history"
	FrameSnapshot = "snapshot"
	FrameSample   = "sample"
	FrameDecision = "decision"
)

// Envelope is decoded first to read Type, then the same line is decoded
// again into the concrete frame type it names — cheaper than a custom
// UnmarshalJSON per frame, and keeps every frame type a plain struct.
type Envelope struct {
	Type string `json:"type"`
}

// HelloFrame is sent first, on connect, on a dashboard connection (TUI.md
// §12.1.1): the static facts the client needs before it can render
// anything ceiling-relative (thresholds, "N/M running"). It (and
// SnapshotFrame) may also arrive again, unprompted, mid-stream — the
// daemon re-sends both to every open dashboard connection after any
// successful config change (a SIGHUP reload or a live tune, TUI.md §9.6) —
// clients must treat them as replace-on-arrival, not connect-only.
type HelloFrame struct {
	Type              string  `json:"type"`
	Cores             int     `json:"cores"`
	MaxSystemLoad     float64 `json:"max_system_load"`
	MaxIOWait         int     `json:"max_iowait"`
	MaxConcurrentJobs int     `json:"max_concurrent_jobs"`
	CheckInterval     int     `json:"check_interval"`
	Nice              int     `json:"nice"`
}

// HistoryFrame is sent once, second, on a dashboard connection: the
// pre-filled metric sample ring buffer (TUI.md §12.1.2), so the graphs
// are full immediately rather than empty and filling in. One combined
// message rather than one per metric (see PLAN.md/TUI.md rebuild plan) —
// MetricSample already carries load+iowait+ceilings per timestamp.
type HistoryFrame struct {
	Type    string         `json:"type"`
	Samples []MetricSample `json:"samples"`
}

// SnapshotFrame is sent once, third: current per-app state, to draw the
// apps table immediately without waiting for the next decision tick
// (TUI.md §12.1.3).
type SnapshotFrame struct {
	Type string      `json:"type"`
	Apps []AppStatus `json:"apps"`
}

// SampleFrame wraps one live MetricSample (TUI.md §12.2).
type SampleFrame struct {
	Type string `json:"type"`
	MetricSample
}

// DecisionFrame wraps one live DecisionEvent (TUI.md §12.2).
type DecisionFrame struct {
	Type string `json:"type"`
	DecisionEvent
}
