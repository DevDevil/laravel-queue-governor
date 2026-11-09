package cli

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/DevDevil/laravel-queue-governor/internal/ipc"
	"github.com/DevDevil/laravel-queue-governor/internal/monitor"
)

func TestStreamWatch_ReceivesPublishedEvents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lqg.sock")

	srv := &monitor.Server{Snapshots: monitor.NewSnapshotStore(), Bus: monitor.NewBus()}
	if err := srv.Listen(path); err != nil {
		t.Fatalf("Listen: %v", err)
	}
	t.Cleanup(func() { srv.Close() })
	go srv.Serve()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	received := make(chan ipc.DecisionEvent, 4)
	streamErr := make(chan error, 1)
	go func() {
		streamErr <- StreamWatch(ctx, path, func(e ipc.DecisionEvent) error {
			received <- e
			return nil
		})
	}()

	// Give the client a moment to connect and subscribe before publishing,
	// so the event isn't dropped by the bus's non-blocking send.
	time.Sleep(100 * time.Millisecond)
	srv.Bus.Publish([]ipc.DecisionEvent{{App: "wonen31", Decision: "dispatched", State: "running"}})

	select {
	case e := <-received:
		if e.App != "wonen31" {
			t.Errorf("App = %q, want wonen31", e.App)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for streamed event")
	}

	cancel()
	select {
	case err := <-streamErr:
		if err != nil {
			t.Errorf("StreamWatch returned error after context cancellation: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("StreamWatch did not return promptly after context cancellation")
	}
}

func TestStreamWatch_HandleErrorStopsTheStream(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lqg.sock")

	srv := &monitor.Server{Snapshots: monitor.NewSnapshotStore(), Bus: monitor.NewBus()}
	if err := srv.Listen(path); err != nil {
		t.Fatalf("Listen: %v", err)
	}
	t.Cleanup(func() { srv.Close() })
	go srv.Serve()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	boom := errors.New("boom")
	streamErr := make(chan error, 1)
	go func() {
		streamErr <- StreamWatch(ctx, path, func(e ipc.DecisionEvent) error {
			return boom
		})
	}()

	time.Sleep(100 * time.Millisecond)
	srv.Bus.Publish([]ipc.DecisionEvent{{App: "a"}})

	select {
	case err := <-streamErr:
		if err != boom {
			t.Errorf("StreamWatch error = %v, want %v", err, boom)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for StreamWatch to stop after handle error")
	}
}
