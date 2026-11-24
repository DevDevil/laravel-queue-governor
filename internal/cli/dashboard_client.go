package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"

	"github.com/DevDevil/laravel-queue-governor/internal/ipc"
)

// dashboardFrame is exactly one of its fields non-nil — the sum type for
// whichever tagged frame streamDashboard/StreamDashboard just decoded
// (TUI.md §12). Unlike watch/metrics (one payload shape per connection,
// see streamNDJSON), a dashboard connection multiplexes five shapes on
// one connection, so a single generic handler can't decode the line
// without first reading its "type".
type dashboardFrame struct {
	Hello    *ipc.HelloFrame
	History  *ipc.HistoryFrame
	Snapshot *ipc.SnapshotFrame
	Sample   *ipc.SampleFrame
	Decision *ipc.DecisionFrame
}

// StreamDashboard connects to socketPath, sends {"cmd":"dashboard"}, and
// calls handle for every decoded frame until the stream ends: the server
// closes it, ctx is canceled, or handle itself returns an error. Frame
// types the client doesn't recognize (future daemon versions) are
// silently skipped rather than erroring, so an older client degrades
// gracefully instead of dying on an unknown "type".
func StreamDashboard(ctx context.Context, socketPath string, handle func(dashboardFrame) error) error {
	conn, err := dialDaemon(socketPath)
	if err != nil {
		return err
	}
	defer conn.Close()

	if _, err := conn.Write([]byte(`{"cmd":"dashboard"}` + "\n")); err != nil {
		return fmt.Errorf("sending dashboard request: %w", err)
	}

	stopped := make(chan struct{})
	defer close(stopped)
	go func() {
		select {
		case <-ctx.Done():
			conn.Close()
		case <-stopped:
		}
	}()

	scanner := bufio.NewScanner(conn)
	for scanner.Scan() {
		line := scanner.Bytes()

		var env ipc.Envelope
		if err := json.Unmarshal(line, &env); err != nil {
			return fmt.Errorf("parsing dashboard frame: %w", err)
		}

		var frame dashboardFrame
		switch env.Type {
		case ipc.FrameHello:
			var f ipc.HelloFrame
			if err := json.Unmarshal(line, &f); err != nil {
				return fmt.Errorf("parsing hello frame: %w", err)
			}
			frame.Hello = &f
		case ipc.FrameHistory:
			var f ipc.HistoryFrame
			if err := json.Unmarshal(line, &f); err != nil {
				return fmt.Errorf("parsing history frame: %w", err)
			}
			frame.History = &f
		case ipc.FrameSnapshot:
			var f ipc.SnapshotFrame
			if err := json.Unmarshal(line, &f); err != nil {
				return fmt.Errorf("parsing snapshot frame: %w", err)
			}
			frame.Snapshot = &f
		case ipc.FrameSample:
			var f ipc.SampleFrame
			if err := json.Unmarshal(line, &f); err != nil {
				return fmt.Errorf("parsing sample frame: %w", err)
			}
			frame.Sample = &f
		case ipc.FrameDecision:
			var f ipc.DecisionFrame
			if err := json.Unmarshal(line, &f); err != nil {
				return fmt.Errorf("parsing decision frame: %w", err)
			}
			frame.Decision = &f
		default:
			continue
		}

		if err := handle(frame); err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil && ctx.Err() == nil {
		return fmt.Errorf("reading dashboard stream: %w", err)
	}
	return nil
}
