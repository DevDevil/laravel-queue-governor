package monitor

import (
	"sync"

	"github.com/DevDevil/laravel-queue-governor/internal/ipc"
)

// DefaultMetricHistorySize is how many MetricSamples MetricHistory keeps
// by default — at the ~1s sampling cadence, 300 is 5 minutes of history.
const DefaultMetricHistorySize = 300

// MetricHistory is a fixed-capacity ring buffer of the most recent
// MetricSamples. A `metrics` client gets this sent first, on connect,
// before it starts receiving live samples, so a freshly-opened `lqg watch`
// graph is pre-filled instead of starting empty.
type MetricHistory struct {
	mu       sync.Mutex
	samples  []ipc.MetricSample
	capacity int
}

func NewMetricHistory(capacity int) *MetricHistory {
	if capacity < 1 {
		capacity = 1
	}
	return &MetricHistory{capacity: capacity}
}

// Add appends a sample, dropping the oldest once at capacity.
func (h *MetricHistory) Add(s ipc.MetricSample) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.samples = append(h.samples, s)
	if len(h.samples) > h.capacity {
		h.samples = h.samples[len(h.samples)-h.capacity:]
	}
}

// Snapshot returns a copy of the current buffer, oldest first — a copy
// so the caller can't mutate internal state, and so it stays consistent
// while Add continues to run concurrently.
func (h *MetricHistory) Snapshot() []ipc.MetricSample {
	h.mu.Lock()
	defer h.mu.Unlock()

	out := make([]ipc.MetricSample, len(h.samples))
	copy(out, h.samples)
	return out
}
