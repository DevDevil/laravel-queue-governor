package monitor

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/DevDevil/laravel-queue-governor/internal/config"
	"github.com/DevDevil/laravel-queue-governor/internal/ipc"
)

func newTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "lqg.sock")

	s := &Server{Snapshots: NewSnapshotStore(), Bus: NewBus(), MetricHistory: NewMetricHistory(10), MetricBus: NewSampleBus(), Refresh: NewRefreshBus()}
	if err := s.Listen(path); err != nil {
		t.Fatalf("Listen: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	go s.Serve()
	return s, path
}

func dial(t *testing.T, path string) net.Conn {
	t.Helper()
	conn, err := net.Dial("unix", path)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

func TestServer_SocketPermissions(t *testing.T) {
	_, path := newTestServer(t)

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o660 {
		t.Errorf("socket mode = %o, want 0660", perm)
	}
}

func TestServer_Status(t *testing.T) {
	s, path := newTestServer(t)

	reason := "app_load_high"
	snap := ipc.StatusSnapshot{
		Ts:   base,
		Apps: []ipc.AppStatus{{Name: "wonen31", State: "throttled", Reason: &reason}},
	}
	s.Snapshots.Update(snap)

	conn := dial(t, path)
	if _, err := conn.Write([]byte(`{"cmd":"status"}` + "\n")); err != nil {
		t.Fatal(err)
	}

	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		t.Fatalf("reading response: %v", err)
	}

	var got ipc.StatusSnapshot
	if err := json.Unmarshal([]byte(line), &got); err != nil {
		t.Fatalf("unmarshal response %q: %v", line, err)
	}
	if len(got.Apps) != 1 || got.Apps[0].Name != "wonen31" || got.Apps[0].Reason == nil || *got.Apps[0].Reason != "app_load_high" {
		t.Errorf("StatusSnapshot = %+v, want the updated snapshot", got)
	}
}

func TestServer_Metrics_HistoryThenLive(t *testing.T) {
	s, path := newTestServer(t)

	// Pre-populate history, as if the sampler had already been running a
	// while before this client connected.
	s.MetricHistory.Add(ipc.MetricSample{Load: 1})
	s.MetricHistory.Add(ipc.MetricSample{Load: 2})
	s.MetricHistory.Add(ipc.MetricSample{Load: 3})

	conn := dial(t, path)
	if _, err := conn.Write([]byte(`{"cmd":"metrics"}` + "\n")); err != nil {
		t.Fatal(err)
	}
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	reader := bufio.NewReader(conn)

	// The three buffered history samples must arrive first, in order.
	for _, want := range []float64{1, 2, 3} {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("reading history sample: %v", err)
		}
		var got ipc.MetricSample
		if err := json.Unmarshal([]byte(line), &got); err != nil {
			t.Fatalf("unmarshal %q: %v", line, err)
		}
		if got.Load != want {
			t.Fatalf("history sample Load = %v, want %v (wrong order or missing)", got.Load, want)
		}
	}

	// Now a live sample, published after history was already sent.
	time.Sleep(50 * time.Millisecond) // let the server finish sending history and subscribe
	s.MetricBus.Publish(ipc.MetricSample{Load: 4})

	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("reading live sample: %v", err)
	}
	var got ipc.MetricSample
	if err := json.Unmarshal([]byte(line), &got); err != nil {
		t.Fatalf("unmarshal %q: %v", line, err)
	}
	if got.Load != 4 {
		t.Errorf("live sample Load = %v, want 4", got.Load)
	}
}

func TestServer_Metrics_EmptyHistoryGoesStraightToLive(t *testing.T) {
	s, path := newTestServer(t)

	conn := dial(t, path)
	if _, err := conn.Write([]byte(`{"cmd":"metrics"}` + "\n")); err != nil {
		t.Fatal(err)
	}

	time.Sleep(50 * time.Millisecond)
	s.MetricBus.Publish(ipc.MetricSample{Load: 9})

	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		t.Fatalf("reading live sample: %v", err)
	}
	var got ipc.MetricSample
	if err := json.Unmarshal([]byte(line), &got); err != nil {
		t.Fatalf("unmarshal %q: %v", line, err)
	}
	if got.Load != 9 {
		t.Errorf("Load = %v, want 9", got.Load)
	}
}

func TestServer_Watch(t *testing.T) {
	s, path := newTestServer(t)

	conn := dial(t, path)
	if _, err := conn.Write([]byte(`{"cmd":"watch"}` + "\n")); err != nil {
		t.Fatal(err)
	}

	// Give the server a moment to register the subscription before
	// publishing, so the event isn't dropped by the bus's non-blocking send.
	time.Sleep(50 * time.Millisecond)
	s.Bus.Publish([]ipc.DecisionEvent{{App: "wonen31", Decision: "dispatched", State: "running"}})

	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		t.Fatalf("reading streamed event: %v", err)
	}

	var got ipc.DecisionEvent
	if err := json.Unmarshal([]byte(line), &got); err != nil {
		t.Fatalf("unmarshal event %q: %v", line, err)
	}
	if got.App != "wonen31" || got.Decision != "dispatched" {
		t.Errorf("DecisionEvent = %+v, want the published event", got)
	}
}

func TestServer_Dashboard_HandshakeThenLive(t *testing.T) {
	s, path := newTestServer(t)
	s.SetGlobalInfo(8, config.GlobalConfig{MaxSystemLoad: 6.0, MaxIOWait: 25, MaxConcurrentJobs: 2})
	s.MetricHistory.Add(ipc.MetricSample{Load: 1})

	reason := "app_load_high"
	s.Snapshots.Update(ipc.StatusSnapshot{
		Ts:   base,
		Apps: []ipc.AppStatus{{Name: "wonen31", State: "throttled", Reason: &reason, Mode: "per_job"}},
	})

	conn := dial(t, path)
	if _, err := conn.Write([]byte(`{"cmd":"dashboard"}` + "\n")); err != nil {
		t.Fatal(err)
	}
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	reader := bufio.NewReader(conn)

	readEnvelope := func() (ipc.Envelope, string) {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("reading frame: %v", err)
		}
		var env ipc.Envelope
		if err := json.Unmarshal([]byte(line), &env); err != nil {
			t.Fatalf("unmarshal envelope %q: %v", line, err)
		}
		return env, line
	}

	if env, line := readEnvelope(); env.Type != ipc.FrameHello {
		t.Fatalf("first frame type = %q, want hello (line %q)", env.Type, line)
	} else {
		var hello ipc.HelloFrame
		json.Unmarshal([]byte(line), &hello)
		if hello.Cores != 8 || hello.MaxSystemLoad != 6.0 || hello.MaxIOWait != 25 || hello.MaxConcurrentJobs != 2 {
			t.Errorf("HelloFrame = %+v, want cores=8 max_system_load=6.0 max_iowait=25 max_concurrent_jobs=2", hello)
		}
	}

	if env, line := readEnvelope(); env.Type != ipc.FrameHistory {
		t.Fatalf("second frame type = %q, want history (line %q)", env.Type, line)
	} else {
		var hist ipc.HistoryFrame
		json.Unmarshal([]byte(line), &hist)
		if len(hist.Samples) != 1 || hist.Samples[0].Load != 1 {
			t.Errorf("HistoryFrame.Samples = %+v, want one sample with Load=1", hist.Samples)
		}
	}

	if env, line := readEnvelope(); env.Type != ipc.FrameSnapshot {
		t.Fatalf("third frame type = %q, want snapshot (line %q)", env.Type, line)
	} else {
		var snap ipc.SnapshotFrame
		json.Unmarshal([]byte(line), &snap)
		if len(snap.Apps) != 1 || snap.Apps[0].Name != "wonen31" || snap.Apps[0].Mode != "per_job" {
			t.Errorf("SnapshotFrame.Apps = %+v, want wonen31/per_job", snap.Apps)
		}
	}

	time.Sleep(50 * time.Millisecond) // let the server subscribe before publishing
	s.MetricBus.Publish(ipc.MetricSample{Load: 4})
	s.Bus.Publish([]ipc.DecisionEvent{{App: "wonen31", Decision: "dispatched", State: "running"}})

	gotSample, gotDecision := false, false
	for i := 0; i < 2; i++ {
		env, line := readEnvelope()
		switch env.Type {
		case ipc.FrameSample:
			var f ipc.SampleFrame
			json.Unmarshal([]byte(line), &f)
			if f.Load != 4 {
				t.Errorf("SampleFrame.Load = %v, want 4", f.Load)
			}
			gotSample = true
		case ipc.FrameDecision:
			var f ipc.DecisionFrame
			json.Unmarshal([]byte(line), &f)
			if f.App != "wonen31" || f.Decision != "dispatched" {
				t.Errorf("DecisionFrame = %+v, want wonen31/dispatched", f)
			}
			gotDecision = true
		default:
			t.Fatalf("unexpected live frame type %q (line %q)", env.Type, line)
		}
	}
	if !gotSample || !gotDecision {
		t.Errorf("gotSample=%v gotDecision=%v, want both", gotSample, gotDecision)
	}
}

// TestServer_Set_CallsTunerAndRoundTripsResponse drives {"cmd":"set"}
// through the real socket/handle path with a fake Tuner recording what it
// was called with — proving the request unmarshals correctly, Tune's
// scope/app/field/value args arrive intact, and the response (including
// warnings) round-trips.
func TestServer_Set_CallsTunerAndRoundTripsResponse(t *testing.T) {
	s, path := newTestServer(t)

	var gotScope, gotApp, gotField, gotValue string
	s.Tune = func(scope, app, field, value string) ([]string, error) {
		gotScope, gotApp, gotField, gotValue = scope, app, field, value
		return []string{"apps[0]: max_load (5.00) exceeds global.max_system_load (4.00)"}, nil
	}

	conn := dial(t, path)
	req := ipc.SetRequest{Cmd: "set", Scope: "app", App: "wonen31", Field: "max_load", Value: "5.0"}
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write(append(data, '\n')); err != nil {
		t.Fatal(err)
	}

	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}

	var resp ipc.SetResponse
	if err := json.Unmarshal([]byte(line), &resp); err != nil {
		t.Fatalf("unmarshal %q: %v", line, err)
	}

	if gotScope != "app" || gotApp != "wonen31" || gotField != "max_load" || gotValue != "5.0" {
		t.Errorf("Tune called with scope=%q app=%q field=%q value=%q, want app/wonen31/max_load/5.0", gotScope, gotApp, gotField, gotValue)
	}
	if !resp.OK {
		t.Errorf("SetResponse.OK = false, want true (error=%q)", resp.Error)
	}
	if len(resp.Warnings) != 1 {
		t.Errorf("SetResponse.Warnings = %v, want 1 warning round-tripped", resp.Warnings)
	}
}

// TestServer_Set_TuneErrorRoundTrips proves a Tune error (e.g. an invalid
// value rejected by config.Validate) comes back as OK:false with the
// error message, rather than being swallowed or crashing the connection.
func TestServer_Set_TuneErrorRoundTrips(t *testing.T) {
	s, path := newTestServer(t)
	s.Tune = func(scope, app, field, value string) ([]string, error) {
		return nil, fmt.Errorf("field %q: %q is not a valid number", field, value)
	}

	conn := dial(t, path)
	if _, err := conn.Write([]byte(`{"cmd":"set","scope":"global","field":"max_system_load","value":"nope"}` + "\n")); err != nil {
		t.Fatal(err)
	}
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}

	var resp ipc.SetResponse
	if err := json.Unmarshal([]byte(line), &resp); err != nil {
		t.Fatalf("unmarshal %q: %v", line, err)
	}
	if resp.OK {
		t.Error("SetResponse.OK = true, want false for a Tune error")
	}
	if resp.Error == "" {
		t.Error("SetResponse.Error is empty, want the Tune error message")
	}
}

// TestServer_Set_NilTunerRejected proves the nil-Tune guard in handleSet
// (defensive: Serve() isn't reachable until run.go finishes wiring
// srv.Tune, but a misconfigured Server shouldn't panic on a "set" request).
func TestServer_Set_NilTunerRejected(t *testing.T) {
	_, path := newTestServer(t) // newTestServer never sets Tune

	conn := dial(t, path)
	if _, err := conn.Write([]byte(`{"cmd":"set","scope":"global","field":"nice","value":"5"}` + "\n")); err != nil {
		t.Fatal(err)
	}
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}

	var resp ipc.SetResponse
	if err := json.Unmarshal([]byte(line), &resp); err != nil {
		t.Fatalf("unmarshal %q: %v", line, err)
	}
	if resp.OK {
		t.Error("SetResponse.OK = true, want false when Server.Tune is nil")
	}
}

// TestServer_Dashboard_RefreshPushesFreshHelloAndSnapshot proves
// RefreshBus.Publish() causes an already-open dashboard connection to
// receive a fresh hello+snapshot pair unprompted — the fix for the
// pre-existing "already-connected client never sees a post-connect config
// change" gap (TUI.md §9.6/§12.1), now exercised by any Refresh trigger
// (SIGHUP reload or a live tune both call this in production).
func TestServer_Dashboard_RefreshPushesFreshHelloAndSnapshot(t *testing.T) {
	s, path := newTestServer(t)
	s.SetGlobalInfo(8, config.GlobalConfig{MaxSystemLoad: 6.0, MaxIOWait: 25, MaxConcurrentJobs: 2})

	conn := dial(t, path)
	if _, err := conn.Write([]byte(`{"cmd":"dashboard"}` + "\n")); err != nil {
		t.Fatal(err)
	}
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	reader := bufio.NewReader(conn)

	// Drain the handshake: hello, history, snapshot.
	for i := 0; i < 3; i++ {
		if _, err := reader.ReadString('\n'); err != nil {
			t.Fatalf("reading handshake frame %d: %v", i, err)
		}
	}

	// Change global info (as a real reload/tune would, via SetGlobalInfo)
	// and publish a refresh — no sample/decision event involved.
	s.SetGlobalInfo(8, config.GlobalConfig{MaxSystemLoad: 9.5, MaxIOWait: 25, MaxConcurrentJobs: 2})
	time.Sleep(50 * time.Millisecond) // let the server subscribe before publishing
	s.Refresh.Publish()

	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("reading post-refresh frame: %v", err)
	}
	var env ipc.Envelope
	if err := json.Unmarshal([]byte(line), &env); err != nil {
		t.Fatalf("unmarshal envelope %q: %v", line, err)
	}
	if env.Type != ipc.FrameHello {
		t.Fatalf("post-refresh frame type = %q, want hello", env.Type)
	}
	var hello ipc.HelloFrame
	json.Unmarshal([]byte(line), &hello)
	if hello.MaxSystemLoad != 9.5 {
		t.Errorf("post-refresh HelloFrame.MaxSystemLoad = %v, want 9.5", hello.MaxSystemLoad)
	}

	line, err = reader.ReadString('\n')
	if err != nil {
		t.Fatalf("reading post-refresh snapshot frame: %v", err)
	}
	if err := json.Unmarshal([]byte(line), &env); err != nil {
		t.Fatalf("unmarshal envelope %q: %v", line, err)
	}
	if env.Type != ipc.FrameSnapshot {
		t.Fatalf("frame after refreshed hello = %q, want snapshot", env.Type)
	}
}

func TestServer_UnknownCmd(t *testing.T) {
	_, path := newTestServer(t)
	conn := dial(t, path)

	if _, err := conn.Write([]byte(`{"cmd":"bogus"}` + "\n")); err != nil {
		t.Fatal(err)
	}
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}

	var got ipc.ErrorResponse
	if err := json.Unmarshal([]byte(line), &got); err != nil {
		t.Fatalf("unmarshal %q: %v", line, err)
	}
	if got.Error == "" {
		t.Error("expected a non-empty error message for an unknown cmd")
	}
}

func TestServer_InvalidJSON(t *testing.T) {
	_, path := newTestServer(t)
	conn := dial(t, path)

	if _, err := conn.Write([]byte("not json\n")); err != nil {
		t.Fatal(err)
	}
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}

	var got ipc.ErrorResponse
	if err := json.Unmarshal([]byte(line), &got); err != nil {
		t.Fatalf("unmarshal %q: %v", line, err)
	}
	if got.Error == "" {
		t.Error("expected a non-empty error message for invalid JSON")
	}
}

func TestServer_ListenRemovesStaleSocket(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lqg.sock")

	s1 := &Server{Snapshots: NewSnapshotStore(), Bus: NewBus()}
	if err := s1.Listen(path); err != nil {
		t.Fatalf("first Listen: %v", err)
	}
	s1.Close()

	// The socket file from s1 is still on disk (Close doesn't unlink on
	// all platforms) — a second Listen on the same path must still work.
	s2 := &Server{Snapshots: NewSnapshotStore(), Bus: NewBus()}
	if err := s2.Listen(path); err != nil {
		t.Fatalf("second Listen on same path: %v", err)
	}
	s2.Close()
}
