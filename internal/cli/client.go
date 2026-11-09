// Package cli is the status/watch client: dialing the daemon's unix
// socket and rendering what comes back. Not part of the daemon loop —
// free to depend on whatever's convenient (PLAN.md §11).
package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"time"

	"github.com/DevDevil/laravel-queue-governor/internal/ipc"
)

// dialTimeout and readTimeout are generous but finite: a hung or
// unreachable daemon should fail the CLI command quickly rather than
// hang the terminal.
const (
	dialTimeout = 3 * time.Second
	readTimeout = 5 * time.Second
)

// sendRequest is the one-shot request/response shape shared by FetchStatus
// and SendSet: dial, marshal+write req as one NDJSON line, read exactly
// one line back, unmarshal into Resp. kind labels the request in error
// messages ("status", "set"). Unlike streamNDJSON (watch.go), this always
// closes the connection after one round trip and applies readTimeout —
// callers here want a single answer, not an indefinite stream.
func sendRequest[Req, Resp any](socketPath, kind string, req Req) (Resp, error) {
	var zero Resp

	conn, err := net.DialTimeout("unix", socketPath, dialTimeout)
	if err != nil {
		return zero, fmt.Errorf("connecting to %s: %w", socketPath, err)
	}
	defer conn.Close()

	data, err := json.Marshal(req)
	if err != nil {
		return zero, fmt.Errorf("encoding %s request: %w", kind, err)
	}
	if _, err := conn.Write(append(data, '\n')); err != nil {
		return zero, fmt.Errorf("sending %s request: %w", kind, err)
	}

	conn.SetReadDeadline(time.Now().Add(readTimeout))
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		return zero, fmt.Errorf("reading %s response: %w", kind, err)
	}

	var resp Resp
	if err := json.Unmarshal([]byte(line), &resp); err != nil {
		return zero, fmt.Errorf("parsing %s response: %w", kind, err)
	}
	return resp, nil
}

// FetchStatus connects to the daemon's unix socket, sends a status
// request (PLAN.md §8: {"cmd":"status"}), and returns the parsed
// snapshot.
func FetchStatus(socketPath string) (ipc.StatusSnapshot, error) {
	return sendRequest[ipc.Request, ipc.StatusSnapshot](socketPath, "status", ipc.Request{Cmd: "status"})
}

// SendSet sends a {"cmd":"set"} request (PLAN.md §7/§8, TUI.md §9.6: a
// live, session-only tune) and returns the parsed response. Mirrors
// FetchStatus's one-shot request/response shape — a fresh, short-lived
// connection per call, not the long-lived dashboard stream — deliberately,
// so neither the server's read-one-line-per-connection model
// (monitor.Server.handle) nor StreamDashboard's read-only loop needs to
// change.
func SendSet(socketPath string, req ipc.SetRequest) (ipc.SetResponse, error) {
	req.Cmd = "set"
	return sendRequest[ipc.SetRequest, ipc.SetResponse](socketPath, "set", req)
}
