package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/DevDevil/laravel-queue-governor/internal/ipc"
)

const (
	ansiGreen  = "\033[32m"
	ansiYellow = "\033[33m"
	ansiReset  = "\033[0m"
)

// streamNDJSON connects to the daemon's unix socket, sends {"cmd":cmd},
// and calls handle for every NDJSON-decoded value received until the
// stream ends: the server closes it, ctx is canceled (e.g. Ctrl-C —
// closes the connection to unblock the read), or handle itself returns
// an error. Unlike FetchStatus, there is no read deadline: watch is meant
// to block indefinitely waiting for the next line. Generic over the
// payload type so any future NDJSON stream command can reuse this same
// connect/scan/cancel shape, the way StreamWatch (ipc.DecisionEvent)
// already does.
func streamNDJSON[T any](ctx context.Context, socketPath, cmd string, handle func(T) error) error {
	conn, err := dialDaemon(socketPath)
	if err != nil {
		return err
	}
	defer conn.Close()

	if _, err := conn.Write([]byte(`{"cmd":"` + cmd + `"}` + "\n")); err != nil {
		return fmt.Errorf("sending %s request: %w", cmd, err)
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
		var v T
		if err := json.Unmarshal(scanner.Bytes(), &v); err != nil {
			return fmt.Errorf("parsing %s response: %w", cmd, err)
		}
		if err := handle(v); err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil && ctx.Err() == nil {
		return fmt.Errorf("reading %s stream: %w", cmd, err)
	}
	return nil
}

// StreamWatch streams decision events (PLAN.md §8: {"cmd":"watch"}).
func StreamWatch(ctx context.Context, socketPath string, handle func(ipc.DecisionEvent) error) error {
	return streamNDJSON(ctx, socketPath, "watch", handle)
}

// WriteWatchLine writes one decision event as an NDJSON line (PLAN.md §8:
// "v1: coloured NDJSON lines"), optionally wrapped in an ANSI color
// (green for dispatched, yellow for skipped). colorize should be false
// when the output isn't a terminal, so the stream stays valid NDJSON for
// piping into jq etc. — the color codes would otherwise sit inside what's
// supposed to be parseable JSON.
func WriteWatchLine(w io.Writer, e ipc.DecisionEvent, colorize bool) error {
	data, err := json.Marshal(e)
	if err != nil {
		return err
	}

	if !colorize {
		_, err := fmt.Fprintf(w, "%s\n", data)
		return err
	}

	color := ansiYellow
	if e.Decision == "dispatched" {
		color = ansiGreen
	}
	_, err = fmt.Fprintf(w, "%s%s%s\n", color, data, ansiReset)
	return err
}
