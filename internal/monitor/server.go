package monitor

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/user"
	"strconv"

	"github.com/DevDevil/laravel-queue-governor/internal/ipc"
)

// Tuner is the function {"cmd":"set"} calls to mutate the running daemon's
// config. monitor must not import internal/daemon (daemon already imports
// monitor), so this is wired to the real implementation by whatever
// constructs the Server. nil-checked defensively in handleSet.
type Tuner func(scope, app, field, value string) (warnings []string, err error)

// Server is the unix-socket NDJSON server (PLAN.md §8) — see handle's
// switch for the request vocabulary and each cmd's response shape.
type Server struct {
	Snapshots     *SnapshotStore
	Bus           *Bus
	MetricHistory *MetricHistory
	MetricBus     *SampleBus
	Refresh       *RefreshBus
	Tune          Tuner

	listener net.Listener
	global   globalInfo
}

// Listen creates (or replaces) the unix socket at path, mode 0660 (PLAN.md
// §8/§9). Group ownership (root:lqg) is best-effort: if the "lqg" group
// doesn't exist — dev/test, or before the install script has run — this
// logs a warning and continues rather than failing socket setup over a
// cosmetic permission detail; chmod 0660 alone still keeps it off-limits
// to other users on the box.
func (s *Server) Listen(path string) error {
	if err := os.RemoveAll(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing stale socket %s: %w", path, err)
	}

	l, err := net.Listen("unix", path)
	if err != nil {
		return fmt.Errorf("listening on %s: %w", path, err)
	}

	if err := os.Chmod(path, 0o660); err != nil {
		l.Close()
		return fmt.Errorf("chmod %s: %w", path, err)
	}

	if gid, ok := lookupGroupGID("lqg"); ok {
		if err := os.Chown(path, -1, gid); err != nil {
			fmt.Fprintf(os.Stderr, "warning: chown %s to group \"lqg\": %v\n", path, err)
		}
	} else {
		fmt.Fprintf(os.Stderr, "warning: group \"lqg\" not found; socket %s left owned by the daemon's own group\n", path)
	}

	s.listener = l
	return nil
}

// Close stops accepting new connections. In-flight watch streams keep
// running until their client disconnects (PLAN.md §13's open loop/
// shutdown question is unresolved — see PLAN.md §13).
func (s *Server) Close() error {
	if s.listener == nil {
		return nil
	}
	return s.listener.Close()
}

// Serve accepts connections until the listener is closed.
func (s *Server) Serve() error {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		go s.handle(conn)
	}
}

func (s *Server) handle(conn net.Conn) {
	defer conn.Close()

	scanner := bufio.NewScanner(conn)
	if !scanner.Scan() {
		return
	}

	var req ipc.Request
	if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
		writeJSONLine(conn, ipc.ErrorResponse{Error: fmt.Sprintf("invalid request: %v", err)})
		return
	}

	switch req.Cmd {
	case "status":
		writeJSONLine(conn, s.Snapshots.Current())
	case "watch":
		s.streamWatch(conn)
	case "metrics":
		s.streamMetrics(conn)
	case "dashboard":
		s.streamDashboard(conn)
	case "set":
		s.handleSet(conn, scanner.Bytes())
	default:
		writeJSONLine(conn, ipc.ErrorResponse{Error: fmt.Sprintf("unknown cmd %q", req.Cmd)})
	}
}

// handleSet serves {"cmd":"set"} (PLAN.md §7/§8, TUI.md §9.6): a live,
// session-only tune of one allowlisted field. One-shot, same shape as
// status — decode, apply via s.Tune, reply once, connection ends. line is
// the same raw bytes already scanned in handle (re-decoded here into the
// richer SetRequest shape, since Request only has Cmd).
func (s *Server) handleSet(conn net.Conn, line []byte) {
	var req ipc.SetRequest
	if err := json.Unmarshal(line, &req); err != nil {
		writeJSONLine(conn, ipc.SetResponse{OK: false, Error: fmt.Sprintf("invalid set request: %v", err)})
		return
	}
	if s.Tune == nil {
		writeJSONLine(conn, ipc.SetResponse{OK: false, Error: "daemon not ready to accept tune requests"})
		return
	}
	warnings, err := s.Tune(req.Scope, req.App, req.Field, req.Value)
	if err != nil {
		writeJSONLine(conn, ipc.SetResponse{OK: false, Error: err.Error(), Warnings: warnings})
		return
	}
	writeJSONLine(conn, ipc.SetResponse{OK: true, Warnings: warnings})
}

func (s *Server) streamWatch(conn net.Conn) {
	ch, unsubscribe := s.Bus.Subscribe(subscriberBufferSize)
	defer unsubscribe()
	streamChannel(conn, ch)
}

// streamMetrics sends the buffered history first (oldest first), then
// live samples as they're published. Deliberately snapshots history
// *before* subscribing: a sample published in the tiny window between
// the two would then just be missed (a brief gap), rather than the
// reverse ordering risking that same sample appearing twice (a
// duplicate, which would show as a kink in the graph — worse than a gap
// for this use case).
func (s *Server) streamMetrics(conn net.Conn) {
	for _, sample := range s.MetricHistory.Snapshot() {
		if err := writeJSONLine(conn, sample); err != nil {
			return
		}
	}

	ch, unsubscribe := s.MetricBus.Subscribe(subscriberBufferSize)
	defer unsubscribe()
	streamChannel(conn, ch)
}

// streamChannel writes every value from ch to conn as one NDJSON line
// each, stopping (silently — the client just disconnected) the first
// time a write fails. Shared by streamWatch and streamMetrics' live-
// sample loop, which are otherwise identical but for payload type.
// streamDashboard's two-channel fan-in select loop can't reuse this (one
// channel per call here), so it stays hand-rolled.
func streamChannel[T any](conn net.Conn, ch <-chan T) {
	for v := range ch {
		if err := writeJSONLine(conn, v); err != nil {
			return
		}
	}
}

func writeJSONLine(w io.Writer, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = w.Write(data)
	return err
}

func lookupGroupGID(name string) (int, bool) {
	g, err := user.LookupGroup(name)
	if err != nil {
		return 0, false
	}
	gid, err := strconv.Atoi(g.Gid)
	if err != nil {
		return 0, false
	}
	return gid, true
}
