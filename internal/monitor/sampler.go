package monitor

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/DevDevil/laravel-queue-governor/internal/clock"
	"github.com/DevDevil/laravel-queue-governor/internal/ipc"
	"github.com/DevDevil/laravel-queue-governor/internal/metrics"
)

// MetricSampler independently samples load/iowait on its own fixed
// cadence, decoupled entirely from the scheduler's check_interval-driven
// tick — so `lqg watch`'s graphs update smoothly no matter how slow
// check_interval is configured. Deliberately does not share the
// scheduler's metrics.Source: ProcSource does stateful iowait delta-
// sampling between calls, and two independent call cadences sharing one
// instance would corrupt both deltas. Reading /proc twice on two
// independent cadences is cheap and stdlib-only either way.
type MetricSampler struct {
	Source   metrics.Source
	Clock    clock.Clock
	Interval time.Duration
	NumCPU   int
	Stderr   io.Writer

	history *MetricHistory
	bus     *SampleBus

	ceilingMu sync.Mutex
	maxLoad   float64
	// maxIOWait is a float64 here (unlike ipc.HelloFrame.MaxIOWait/
	// ipc.AppStatus.MaxIOWaitCeiling, both int) because sample()'s "% of
	// ceiling" ratio math needs the precision; it's still reported as a
	// plain percent on the wire.
	maxIOWait float64
}

// Ceilings groups the two initial ceiling values NewMetricSampler needs —
// a named struct rather than two adjacent float64 params, so a caller
// can't silently transpose max_system_load and max_iowait (a swap would
// only surface later as wrong "% of ceiling" dashboard coloring).
type Ceilings struct {
	MaxLoad   float64
	MaxIOWait float64
}

// NewMetricSampler builds a sampler with its own history buffer and
// sample bus, ready to have Run called on it. ceilings are the initial
// global.max_system_load/max_iowait values reported on every sample —
// update them via SetCeiling after a config reload.
func NewMetricSampler(source metrics.Source, clk clock.Clock, interval time.Duration, numCPU, historySize int, ceilings Ceilings) *MetricSampler {
	return &MetricSampler{
		Source:    source,
		Clock:     clk,
		Interval:  interval,
		NumCPU:    numCPU,
		Stderr:    io.Discard,
		history:   NewMetricHistory(historySize),
		bus:       NewSampleBus(),
		maxLoad:   ceilings.MaxLoad,
		maxIOWait: ceilings.MaxIOWait,
	}
}

// History is the ring buffer a "metrics" client's connect-time snapshot
// is read from.
func (s *MetricSampler) History() *MetricHistory { return s.history }

// Bus is the live sample stream a "metrics" client subscribes to after
// its history snapshot.
func (s *MetricSampler) Bus() *SampleBus { return s.bus }

// SetCeiling updates the load/iowait ceilings reported on future samples.
// Call this alongside daemon.Loop.Reload so the dashboard's "% of ceiling"
// coloring stays correct after a config change. A mutex-guarded field
// rather than having sample() read Scheduler.Global directly: Run executes
// on its own goroutine, independent of Loop's, and Global is mutated by
// Reload on Loop's goroutine — reading it unsynchronized from here would
// race.
func (s *MetricSampler) SetCeiling(maxLoad, maxIOWait float64) {
	s.ceilingMu.Lock()
	defer s.ceilingMu.Unlock()
	s.maxLoad = maxLoad
	s.maxIOWait = maxIOWait
}

func (s *MetricSampler) ceiling() (maxLoad, maxIOWait float64) {
	s.ceilingMu.Lock()
	defer s.ceilingMu.Unlock()
	return s.maxLoad, s.maxIOWait
}

// Run blocks, sampling on Interval until ctx is canceled. A sample
// failure (metrics read error) is logged and skipped, not fatal — same
// "log and continue" principle as the main tick loop (internal/daemon).
func (s *MetricSampler) Run(ctx context.Context) {
	timer := s.Clock.After(s.Interval)
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer:
			s.sample()
			timer = s.Clock.After(s.Interval)
		}
	}
}

func (s *MetricSampler) sample() {
	m, err := s.Source.Sample()
	if err != nil {
		fmt.Fprintf(s.Stderr, "metric sample: %v\n", err)
		return
	}

	maxLoad, maxIOWait := s.ceiling()
	sample := ipc.MetricSample{
		Ts:          s.Clock.Now(),
		Load:        m.Load1,
		LoadPerCore: metrics.NormalizeLoad(m.Load1, s.NumCPU),
		IOWait:      m.IOWaitPct,
		MaxLoad:     maxLoad,
		MaxIOWait:   maxIOWait,
	}
	s.history.Add(sample)
	s.bus.Publish(sample)
}
