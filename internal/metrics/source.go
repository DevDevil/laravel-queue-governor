// Package metrics defines the load/iowait signal the scheduler polls once
// per tick. ProcSource (proc_source.go) is the real /proc-backed
// implementation; the Source interface below is what lets the scheduler
// and FSM be built and tested against a fake instead.
package metrics

// Metrics is one poll's worth of system load signal.
type Metrics struct {
	Load1     float64 // 1-minute load average, absolute (matches uptime/top)
	IOWaitPct float64 // iowait percent, delta-sampled since the previous poll
}

// Source is implemented by the real /proc reader in production and by fakes
// in tests — the scheduler never reads /proc directly.
type Source interface {
	Sample() (Metrics, error)
}
