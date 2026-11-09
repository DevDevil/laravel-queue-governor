package monitor

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/DevDevil/laravel-queue-governor/internal/metrics"
)

// fakeClock is a controllable clock.Clock: Now() is fixed, and After
// always returns the same channel regardless of the requested duration —
// the test decides exactly when a "tick" fires by sending on it.
type fakeClock struct {
	now   time.Time
	ticks chan time.Time
}

func newFakeClock(now time.Time) *fakeClock {
	return &fakeClock{now: now, ticks: make(chan time.Time)}
}

func (c *fakeClock) Now() time.Time                       { return c.now }
func (c *fakeClock) After(time.Duration) <-chan time.Time { return c.ticks }

type fakeMetricsSource struct {
	mu  sync.Mutex
	m   metrics.Metrics
	err error
}

func (f *fakeMetricsSource) Sample() (metrics.Metrics, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.m, f.err
}

func (f *fakeMetricsSource) set(m metrics.Metrics, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.m, f.err = m, err
}

func TestMetricSampler_SamplesOnTickAndPublishes(t *testing.T) {
	fc := newFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	src := &fakeMetricsSource{m: metrics.Metrics{Load1: 4.0, IOWaitPct: 12}}

	s := NewMetricSampler(src, fc, time.Second, 4 /* numCPU */, 10 /* history */, Ceilings{MaxLoad: 6.0, MaxIOWait: 25.0})

	ch, unsubscribe := s.Bus().Subscribe(4)
	defer unsubscribe()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Run(ctx)

	fc.ticks <- fc.now // trigger exactly one sample

	select {
	case sample := <-ch:
		if sample.Load != 4.0 || sample.IOWait != 12 || sample.LoadPerCore != 1.0 {
			t.Errorf("sample = %+v, want Load=4 IOWait=12 LoadPerCore=1 (4/4 cores)", sample)
		}
		if sample.MaxLoad != 6.0 || sample.MaxIOWait != 25.0 {
			t.Errorf("sample ceiling = %v/%v, want 6.0/25.0", sample.MaxLoad, sample.MaxIOWait)
		}
		if !sample.Ts.Equal(fc.now) {
			t.Errorf("Ts = %v, want %v", sample.Ts, fc.now)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for a published sample")
	}

	hist := s.History().Snapshot()
	if len(hist) != 1 || hist[0].Load != 4.0 {
		t.Errorf("History().Snapshot() = %+v, want one sample with Load=4", hist)
	}
}

// syncBuffer is a mutex-guarded bytes.Buffer — the sampler's Run
// goroutine writes to Stderr concurrently with the test goroutine reading
// it, and plain bytes.Buffer isn't safe for that.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Len()
}

func TestMetricSampler_SourceErrorSkipsSampleAndLogs(t *testing.T) {
	fc := newFakeClock(time.Now())
	src := &fakeMetricsSource{err: errors.New("boom")}
	var stderr syncBuffer

	s := NewMetricSampler(src, fc, time.Second, 4, 10, Ceilings{MaxLoad: 6.0, MaxIOWait: 25.0})
	s.Stderr = &stderr

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Run(ctx)

	fc.ticks <- fc.now

	// Give Run's goroutine a moment to process the tick before asserting
	// (there's no success signal to wait on when a sample is skipped).
	deadline := time.Now().Add(2 * time.Second)
	for len(s.History().Snapshot()) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	if got := s.History().Snapshot(); len(got) != 0 {
		t.Errorf("History().Snapshot() = %v, want empty after a source error", got)
	}
	if stderr.Len() == 0 {
		t.Error("expected the source error to be logged to Stderr")
	}
}

func TestMetricSampler_SetCeilingAffectsFutureSamples(t *testing.T) {
	fc := newFakeClock(time.Now())
	src := &fakeMetricsSource{m: metrics.Metrics{Load1: 1, IOWaitPct: 1}}
	s := NewMetricSampler(src, fc, time.Second, 4, 10, Ceilings{MaxLoad: 6.0, MaxIOWait: 25.0})

	ch, unsubscribe := s.Bus().Subscribe(4)
	defer unsubscribe()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Run(ctx)

	fc.ticks <- fc.now
	first := <-ch
	if first.MaxLoad != 6.0 {
		t.Fatalf("first sample MaxLoad = %v, want 6.0", first.MaxLoad)
	}

	s.SetCeiling(3.5, 40.0)
	fc.ticks <- fc.now
	second := <-ch
	if second.MaxLoad != 3.5 || second.MaxIOWait != 40.0 {
		t.Errorf("second sample ceiling = %v/%v, want 3.5/40.0 (SetCeiling should affect future samples)", second.MaxLoad, second.MaxIOWait)
	}
}

func TestMetricSampler_SetCeilingConcurrentWithSampling(t *testing.T) {
	fc := newFakeClock(time.Now())
	src := &fakeMetricsSource{m: metrics.Metrics{}}
	s := NewMetricSampler(src, fc, time.Second, 1, 10, Ceilings{MaxLoad: 6.0, MaxIOWait: 25.0})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Run(ctx)

	done := make(chan struct{})
	go func() {
		for i := 0; i < 20; i++ {
			s.SetCeiling(float64(i), float64(i))
		}
		close(done)
	}()

	for i := 0; i < 20; i++ {
		fc.ticks <- fc.now
	}
	<-done
}

func TestMetricSampler_StopsOnContextCancel(t *testing.T) {
	fc := newFakeClock(time.Now())
	src := &fakeMetricsSource{m: metrics.Metrics{}}
	s := NewMetricSampler(src, fc, time.Second, 1, 10, Ceilings{MaxLoad: 6.0, MaxIOWait: 25.0})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		s.Run(ctx)
		close(done)
	}()

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return promptly after context cancellation")
	}
}
