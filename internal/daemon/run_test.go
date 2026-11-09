package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/DevDevil/laravel-queue-governor/internal/clock"
	"github.com/DevDevil/laravel-queue-governor/internal/config"
	"github.com/DevDevil/laravel-queue-governor/internal/metrics"
	"github.com/DevDevil/laravel-queue-governor/internal/monitor"
	"github.com/DevDevil/laravel-queue-governor/internal/runner"
	"github.com/DevDevil/laravel-queue-governor/internal/scheduler"
)

// TestRun_TicksRepeatedlyAndDispatchesOverRealTime is deliberately a
// real-time integration test (unlike the rest of this package's
// deterministic, fixedClock-driven unit tests): Run's own job is pacing
// itself against a real clock via Clock.After, so exercising that pacing
// means letting real time pass. Bounded to well under a second so it
// stays fast and non-flaky.
func TestRun_TicksRepeatedlyAndDispatchesOverRealTime(t *testing.T) {
	fr := &fakeRunner{result: runner.RunResult{ExitCode: 0, Duration: time.Millisecond}}
	app := testApp("wonen31")
	app.MinInterval = 0
	app.SpikeCooldown = 0

	sched := scheduler.New(testGlobal(), []config.AppConfig{app}, fakeSource{m: metrics.Metrics{Load1: 1, IOWaitPct: 5}})
	l := New(sched, fr, monitor.NewBus(), monitor.NewSnapshotStore(), clock.RealClock{}, discard{}, 20*time.Millisecond, monitor.NewRefreshBus())

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	l.Run(ctx)

	if got := fr.callCount(); got < 3 {
		t.Errorf("runner called %d times over ~150ms at a 20ms interval, want at least 3", got)
	}
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }
