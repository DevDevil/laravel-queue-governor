package cli

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/DevDevil/laravel-queue-governor/internal/config"
	"github.com/DevDevil/laravel-queue-governor/internal/monitor"
)

func newTestDashboardServer(t *testing.T, path string) *monitor.Server {
	t.Helper()
	s := &monitor.Server{
		Snapshots:     monitor.NewSnapshotStore(),
		Bus:           monitor.NewBus(),
		MetricHistory: monitor.NewMetricHistory(10),
		MetricBus:     monitor.NewSampleBus(),
		Refresh:       monitor.NewRefreshBus(),
	}
	s.SetGlobalInfo(4, config.GlobalConfig{MaxSystemLoad: 6.0, MaxIOWait: 25, MaxConcurrentJobs: 2})
	if err := s.Listen(path); err != nil {
		t.Fatalf("Listen: %v", err)
	}
	go s.Serve()
	return s
}

// drainHello reads frames until a hello frame is seen (skipping any
// history/snapshot frames already buffered ahead of it), or fails the
// test after timeout.
func drainHello(t *testing.T, frames <-chan dashboardFrame, timeout time.Duration) {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case f := <-frames:
			if f.Hello != nil {
				return
			}
		case <-deadline:
			t.Fatal("timed out waiting for a hello frame")
		}
	}
}

// TestRunDashboardConnection_ReconnectsOnceDaemonAppears is TUI.md
// §12.4/§9.7 end to end: no daemon is listening yet when the connection
// loop starts (a monitor.Server's Close only stops accepting new
// connections — it doesn't drop already-open ones, PLAN.md §13's
// deliberately unresolved shutdown question — so "daemon not up yet" is
// the reliably reproducible way to exercise the disconnect path without
// depending on that). The loop must report the failure and keep
// retrying rather than giving up, and once a daemon starts listening,
// 'r' (retryNow) reconnects immediately rather than waiting out the
// full reconnectBackoff.
func TestRunDashboardConnection_ReconnectsOnceDaemonAppears(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lqg.sock")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	frames := make(chan dashboardFrame, 8)
	disconnectCh := make(chan error, 1)
	retryNow := make(chan struct{}, 1)
	go runDashboardConnection(ctx, path, frames, disconnectCh, retryNow)

	select {
	case err := <-disconnectCh:
		if err == nil {
			t.Error("expected a non-nil error for the initial failed connection attempt")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the initial disconnect notification")
	}

	s := newTestDashboardServer(t, path)
	defer s.Close()
	retryNow <- struct{}{}

	drainHello(t, frames, 2*time.Second)
}

func TestRunDashboardConnection_StopsOnContextCancel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lqg.sock")
	s := newTestDashboardServer(t, path)
	defer s.Close()

	ctx, cancel := context.WithCancel(context.Background())
	frames := make(chan dashboardFrame, 8)
	disconnectCh := make(chan error, 1)
	retryNow := make(chan struct{}, 1)

	done := make(chan struct{})
	go func() {
		runDashboardConnection(ctx, path, frames, disconnectCh, retryNow)
		close(done)
	}()

	drainHello(t, frames, 2*time.Second)

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("runDashboardConnection did not return promptly after context cancellation")
	}

	// Drain whatever's left buffered (history/snapshot sent right after
	// hello); the channel must end up closed, not just idle.
	closed := false
	for i := 0; i < 10; i++ {
		select {
		case _, ok := <-frames:
			if !ok {
				closed = true
			}
		default:
		}
		if closed {
			break
		}
	}
	if !closed {
		t.Error("frames channel should be closed once the connection loop exits for good")
	}
}
