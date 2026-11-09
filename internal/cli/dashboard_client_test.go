package cli

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/DevDevil/laravel-queue-governor/internal/config"
	"github.com/DevDevil/laravel-queue-governor/internal/ipc"
	"github.com/DevDevil/laravel-queue-governor/internal/monitor"
)

func TestStreamDashboard_DecodesEveryFrameType(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lqg.sock")

	srv := &monitor.Server{
		Snapshots:     monitor.NewSnapshotStore(),
		Bus:           monitor.NewBus(),
		MetricHistory: monitor.NewMetricHistory(10),
		MetricBus:     monitor.NewSampleBus(),
		Refresh:       monitor.NewRefreshBus(),
	}
	srv.SetGlobalInfo(8, config.GlobalConfig{MaxSystemLoad: 6.0, MaxIOWait: 25, MaxConcurrentJobs: 2})
	srv.MetricHistory.Add(ipc.MetricSample{Load: 1})
	srv.Snapshots.Update(ipc.StatusSnapshot{Apps: []ipc.AppStatus{{Name: "wonen31"}}})

	if err := srv.Listen(path); err != nil {
		t.Fatalf("Listen: %v", err)
	}
	t.Cleanup(func() { srv.Close() })
	go srv.Serve()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	received := make(chan dashboardFrame, 8)
	streamErr := make(chan error, 1)
	go func() {
		streamErr <- StreamDashboard(ctx, path, func(f dashboardFrame) error {
			received <- f
			return nil
		})
	}()

	var gotHello, gotHistory, gotSnapshot bool
	for i := 0; i < 3; i++ {
		select {
		case f := <-received:
			switch {
			case f.Hello != nil:
				gotHello = true
				if f.Hello.Cores != 8 || f.Hello.MaxConcurrentJobs != 2 {
					t.Errorf("Hello = %+v, want cores=8 max_concurrent_jobs=2", f.Hello)
				}
			case f.History != nil:
				gotHistory = true
				if len(f.History.Samples) != 1 || f.History.Samples[0].Load != 1 {
					t.Errorf("History.Samples = %+v, want one sample Load=1", f.History.Samples)
				}
			case f.Snapshot != nil:
				gotSnapshot = true
				if len(f.Snapshot.Apps) != 1 || f.Snapshot.Apps[0].Name != "wonen31" {
					t.Errorf("Snapshot.Apps = %+v, want wonen31", f.Snapshot.Apps)
				}
			default:
				t.Fatalf("unexpected empty frame at handshake position %d: %+v", i, f)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for handshake frame %d", i)
		}
	}
	if !gotHello || !gotHistory || !gotSnapshot {
		t.Errorf("gotHello=%v gotHistory=%v gotSnapshot=%v, want all three", gotHello, gotHistory, gotSnapshot)
	}

	time.Sleep(100 * time.Millisecond)
	srv.MetricBus.Publish(ipc.MetricSample{Load: 4})
	srv.Bus.Publish([]ipc.DecisionEvent{{App: "wonen31", Decision: "dispatched"}})

	var gotSample, gotDecision bool
	for i := 0; i < 2; i++ {
		select {
		case f := <-received:
			switch {
			case f.Sample != nil:
				gotSample = true
				if f.Sample.Load != 4 {
					t.Errorf("Sample.Load = %v, want 4", f.Sample.Load)
				}
			case f.Decision != nil:
				gotDecision = true
				if f.Decision.App != "wonen31" {
					t.Errorf("Decision.App = %q, want wonen31", f.Decision.App)
				}
			default:
				t.Fatalf("unexpected empty live frame: %+v", f)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for a live frame")
		}
	}
	if !gotSample || !gotDecision {
		t.Errorf("gotSample=%v gotDecision=%v, want both", gotSample, gotDecision)
	}

	cancel()
	select {
	case err := <-streamErr:
		if err != nil {
			t.Errorf("StreamDashboard returned error after context cancellation: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("StreamDashboard did not return promptly after context cancellation")
	}
}
