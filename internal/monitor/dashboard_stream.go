package monitor

import (
	"net"
	"sync"

	"github.com/DevDevil/laravel-queue-governor/internal/config"
	"github.com/DevDevil/laravel-queue-governor/internal/ipc"
)

// globalInfo is the daemon-wide facts a dashboard connection's hello
// frame needs — cores and the configured ceilings/tunables. Mutex-guarded
// rather than read directly off config on every new connection: whatever
// calls SetGlobalInfo after a config change runs on a different goroutine
// than the one accepting connections.
type globalInfo struct {
	mu                sync.Mutex
	cores             int
	maxLoad           float64
	maxIOWait         int
	maxConcurrentJobs int
	checkInterval     int
	nice              int
}

func (g *globalInfo) set(cores int, cfg config.GlobalConfig) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.cores = cores
	g.maxLoad = cfg.MaxSystemLoad
	g.maxIOWait = cfg.MaxIOWait
	g.maxConcurrentJobs = cfg.MaxConcurrentJobs
	g.checkInterval = cfg.CheckInterval
	g.nice = cfg.Nice
}

// get returns an ipc.HelloFrame with every field but Type already filled
// in — a typed struct rather than a positional tuple, so a future field
// reorder here is a compile error instead of a silently wrong hello frame.
func (g *globalInfo) get() ipc.HelloFrame {
	g.mu.Lock()
	defer g.mu.Unlock()
	return ipc.HelloFrame{
		Cores:             g.cores,
		MaxSystemLoad:     g.maxLoad,
		MaxIOWait:         g.maxIOWait,
		MaxConcurrentJobs: g.maxConcurrentJobs,
		CheckInterval:     g.checkInterval,
		Nice:              g.nice,
	}
}

// SetGlobalInfo records the daemon's core count and configured
// ceilings/tunables for the dashboard's hello frame (TUI.md §12.1.1).
// Called once at startup and again on every valid config change (SIGHUP
// reload or a live tune).
func (s *Server) SetGlobalInfo(cores int, cfg config.GlobalConfig) {
	s.global.set(cores, cfg)
}

// sendHello writes a fresh hello frame (TUI.md §12.1.1) from the current
// globalInfo. Used both for a dashboard connection's initial handshake and
// to re-push current ceilings/tunables to an already-open connection after
// a config change (see the refresh case in streamDashboard below).
func (s *Server) sendHello(conn net.Conn) error {
	frame := s.global.get()
	frame.Type = ipc.FrameHello
	return writeJSONLine(conn, frame)
}

// sendSnapshot writes a fresh snapshot frame (TUI.md §12.1.3) of current
// per-app state. Same reuse rationale as sendHello.
func (s *Server) sendSnapshot(conn net.Conn) error {
	return writeJSONLine(conn, ipc.SnapshotFrame{
		Type: ipc.FrameSnapshot,
		Apps: s.Snapshots.Current().Apps,
	})
}

// sendHelloAndSnapshot re-pushes both frames together — the shape a
// refresh signal needs (no history frame; only hello/snapshot carry
// static-until-changed data, TUI.md §9.6/§12.1).
func (s *Server) sendHelloAndSnapshot(conn net.Conn) error {
	if err := s.sendHello(conn); err != nil {
		return err
	}
	return s.sendSnapshot(conn)
}

// streamDashboard serves {"cmd":"dashboard"} (TUI.md §12): hello, then
// the buffered metric history, then a snapshot of current per-app state,
// then live sample/decision frames fanned in from MetricBus/Bus until
// the client disconnects. Unlike streamWatch/streamMetrics (one payload
// shape per connection), every frame here carries an explicit "type" so
// the client can tell them apart on one connection. A fresh hello+snapshot
// pair is also re-sent, unprompted, whenever Refresh fires (a SIGHUP reload
// or a live tune applied elsewhere) — TUI.md §9.6/§12.1: clients must treat
// hello/snapshot as replace-on-arrival, not connect-only.
func (s *Server) streamDashboard(conn net.Conn) {
	if err := s.sendHello(conn); err != nil {
		return
	}

	if err := writeJSONLine(conn, ipc.HistoryFrame{
		Type:    ipc.FrameHistory,
		Samples: s.MetricHistory.Snapshot(),
	}); err != nil {
		return
	}

	if err := s.sendSnapshot(conn); err != nil {
		return
	}

	sampleCh, unsubSamples := s.MetricBus.Subscribe(subscriberBufferSize)
	defer unsubSamples()
	decisionCh, unsubDecisions := s.Bus.Subscribe(subscriberBufferSize)
	defer unsubDecisions()
	refreshCh, unsubRefresh := s.Refresh.Subscribe(subscriberBufferSize)
	defer unsubRefresh()

	for {
		select {
		case sample, ok := <-sampleCh:
			if !ok {
				return
			}
			if err := writeJSONLine(conn, ipc.SampleFrame{Type: ipc.FrameSample, MetricSample: sample}); err != nil {
				return
			}
		case decision, ok := <-decisionCh:
			if !ok {
				return
			}
			if err := writeJSONLine(conn, ipc.DecisionFrame{Type: ipc.FrameDecision, DecisionEvent: decision}); err != nil {
				return
			}
		case _, ok := <-refreshCh:
			if !ok {
				return
			}
			if err := s.sendHelloAndSnapshot(conn); err != nil {
				return
			}
		}
	}
}
