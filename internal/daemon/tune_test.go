package daemon

import (
	"context"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"testing"
	"time"

	"github.com/DevDevil/laravel-queue-governor/internal/clock"
	"github.com/DevDevil/laravel-queue-governor/internal/config"
	"github.com/DevDevil/laravel-queue-governor/internal/ipc"
	"github.com/DevDevil/laravel-queue-governor/internal/monitor"
	"github.com/DevDevil/laravel-queue-governor/internal/runner"
	"github.com/DevDevil/laravel-queue-governor/internal/scheduler"
)

// tuneValidGlobal/tuneValidApp build a config that passes the REAL
// config.Validate — unlike loop_test.go's testGlobal/testApp (which skip
// required fields since those tests never call Validate), Tune always
// re-validates the whole derived config, so these tests need fixtures
// that clear every rule (mirrors internal/config/validate_test.go's
// validAppFixture).
func tuneValidGlobal() config.GlobalConfig {
	return config.GlobalConfig{
		SocketPath: "/run/lqg/lqg.sock", CheckInterval: 5, MaxSystemLoad: 6.0,
		MaxIOWait: 25, MaxConcurrentJobs: 2, LogLevel: "info",
	}
}

func tuneValidApp(t *testing.T, name string) config.AppConfig {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "artisan"), []byte("#!/usr/bin/env php\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	php := filepath.Join(dir, "php")
	if err := os.WriteFile(php, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	me, err := user.Current()
	if err != nil {
		t.Fatalf("user.Current: %v", err)
	}
	return config.AppConfig{
		Name: name, Path: dir, User: me.Username, PHPBinary: php,
		Connection: "redis", Queues: []string{"default"}, Mode: config.ModePerJob,
		MaxLoad: 4.0, MaxIOWait: 20, MaxConcurrent: 1, MinInterval: 2, SpikeCooldown: 30,
	}
}

// startTuneTestLoop builds a Loop and runs it on a background goroutine
// (stopped via t.Cleanup) — Tune's ConfigSnapshot always round-trips
// through Run's own select loop, so unlike loop_test.go's newTestLoop
// (which returns an idle Loop for direct tick()/applyReload() calls),
// every Tune test needs Run actually alive. checkInterval defaults to an
// hour so the ticker never fires during a test; pass a short one only
// when a test specifically wants real dispatching (mirrors run_test.go's
// real-time pattern).
func startTuneTestLoop(t *testing.T, global config.GlobalConfig, apps []config.AppConfig, run runner.Runner, checkInterval time.Duration) *Loop {
	t.Helper()
	sched := scheduler.New(global, apps, fakeSource{})
	l := New(sched, run, monitor.NewBus(), monitor.NewSnapshotStore(), clock.RealClock{}, io.Discard, checkInterval, monitor.NewRefreshBus())

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go l.Run(ctx)
	return l
}

// waitForConfig polls ConfigSnapshot (Tune's own safe, channel-
// synchronized cross-goroutine read) until want is satisfied or timeout —
// Tune's Reload call is fire-and-forget (buffered channel), so the
// mutation lands on Run's goroutine asynchronously relative to Tune
// returning.
func waitForConfig(t *testing.T, l *Loop, timeout time.Duration, want func(config.Config) bool) config.Config {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var cfg config.Config
	for time.Now().Before(deadline) {
		cfg = l.ConfigSnapshot()
		if want(cfg) {
			return cfg
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for config condition; last snapshot global=%+v apps=%+v", cfg.Global, cfg.Apps)
	return cfg
}

func TestTune_GlobalFieldValidChangeApplies(t *testing.T) {
	app := tuneValidApp(t, "wonen31")
	l := startTuneTestLoop(t, tuneValidGlobal(), []config.AppConfig{app}, &fakeRunner{}, time.Hour)

	warnings, err := l.Tune("global", "", ipc.FieldMaxSystemLoad, "9.5")
	if err != nil {
		t.Fatalf("Tune: unexpected error: %v (warnings=%v)", err, warnings)
	}

	waitForConfig(t, l, time.Second, func(c config.Config) bool { return c.Global.MaxSystemLoad == 9.5 })
}

func TestTune_CheckIntervalChangeUpdatesLoopCheckInterval(t *testing.T) {
	app := tuneValidApp(t, "wonen31")
	l := startTuneTestLoop(t, tuneValidGlobal(), []config.AppConfig{app}, &fakeRunner{}, time.Hour)

	if _, err := l.Tune("global", "", ipc.FieldCheckInterval, "10"); err != nil {
		t.Fatalf("Tune: unexpected error: %v", err)
	}

	// Synchronizes on the same happens-before chain ConfigSnapshot relies
	// on: applyReload sets Scheduler.Global.CheckInterval and l.CheckInterval
	// sequentially, on Run's own goroutine, before that goroutine loops
	// back to select and can service a later snapshot request — so once
	// this poll observes the Global-side update, l.CheckInterval was
	// already set too, making the direct field read below race-free.
	waitForConfig(t, l, time.Second, func(c config.Config) bool { return c.Global.CheckInterval == 10 })

	if got := l.CheckInterval; got != 10*time.Second {
		t.Errorf("l.CheckInterval = %v, want 10s", got)
	}
}

// TestTune_AppFieldValidChangeAppliesWithoutDisruptingInFlightRun proves a
// live tune of one app field doesn't disrupt that same app's in-flight
// run — the daemon-level analog of scheduler/reload_test.go's
// TestReload_ExistingAppKeepsMachineState, exercised through Tune's async
// path instead of a direct Scheduler.Reload call. Observes app state only
// through the daemon's existing safe cross-goroutine reads (Snapshots,
// mutex-guarded; ConfigSnapshot, channel-synchronized) — never a direct
// unsynchronized read of appstate.Machine, which is single-writer-only
// (CLAUDE.md).
func TestTune_AppFieldValidChangeAppliesWithoutDisruptingInFlightRun(t *testing.T) {
	app := tuneValidApp(t, "wonen31")
	app.MinInterval = 0
	app.SpikeCooldown = 0

	release := make(chan struct{})
	fr := &fakeRunner{release: release, result: runner.RunResult{ExitCode: 0, Duration: time.Second}}
	t.Cleanup(func() { close(release) })

	global := tuneValidGlobal()
	l := startTuneTestLoop(t, global, []config.AppConfig{app}, fr, 20*time.Millisecond)

	waitForSnapshotState(t, l, "wonen31", "running", time.Second)

	warnings, err := l.Tune("app", "wonen31", ipc.FieldPriority, "99")
	if err != nil {
		t.Fatalf("Tune: unexpected error: %v (warnings=%v)", err, warnings)
	}

	waitForConfig(t, l, time.Second, func(c config.Config) bool {
		return len(c.Apps) == 1 && c.Apps[0].Priority == 99
	})

	// The run must still be in flight (Machine/RunningCount preserved by
	// Scheduler.Reload matching the app by name), not reset to idle by
	// the reload that just applied the tune.
	snap := l.Snapshots.Current()
	if len(snap.Apps) != 1 || snap.Apps[0].State != "running" {
		t.Fatalf("after tune, snapshot = %+v, want app still running", snap.Apps)
	}
}

// waitForSnapshotState polls the daemon's existing mutex-guarded snapshot
// store (the same one lqg status reads) until the named app reports
// state, or fails the test after timeout.
func waitForSnapshotState(t *testing.T, l *Loop, name, state string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		snap := l.Snapshots.Current()
		for _, a := range snap.Apps {
			if a.Name == name && a.State == state {
				return
			}
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for app %q to reach state %q", name, state)
}

func TestTune_InvalidGlobalValueRejectedAndPreviousConfigKept(t *testing.T) {
	app := tuneValidApp(t, "wonen31")
	l := startTuneTestLoop(t, tuneValidGlobal(), []config.AppConfig{app}, &fakeRunner{}, time.Hour)

	if _, err := l.Tune("global", "", ipc.FieldMaxSystemLoad, "-1"); err == nil {
		t.Fatal("Tune: expected an error for max_system_load <= 0, got nil")
	}
	if _, err := l.Tune("global", "", ipc.FieldMaxSystemLoad, "not-a-number"); err == nil {
		t.Fatal("Tune: expected an error for a non-numeric value, got nil")
	}

	// Neither call above ever reached Reload (both fail before it), so
	// the running config is provably untouched — no polling needed.
	cfg := l.ConfigSnapshot()
	if cfg.Global.MaxSystemLoad != 6.0 {
		t.Errorf("Global.MaxSystemLoad = %v, want unchanged 6.0", cfg.Global.MaxSystemLoad)
	}
}

func TestTune_InvalidAppValueRejectedAndPreviousConfigKept(t *testing.T) {
	app := tuneValidApp(t, "wonen31")
	l := startTuneTestLoop(t, tuneValidGlobal(), []config.AppConfig{app}, &fakeRunner{}, time.Hour)

	if _, err := l.Tune("app", "wonen31", ipc.FieldMaxConcurrent, "0"); err == nil {
		t.Fatal("Tune: expected an error for max_concurrent <= 0, got nil")
	}

	cfg := l.ConfigSnapshot()
	if len(cfg.Apps) != 1 || cfg.Apps[0].MaxConcurrent != 1 {
		t.Errorf("Apps = %+v, want unchanged max_concurrent=1", cfg.Apps)
	}
}

func TestTune_UnknownFieldRejected(t *testing.T) {
	app := tuneValidApp(t, "wonen31")
	l := startTuneTestLoop(t, tuneValidGlobal(), []config.AppConfig{app}, &fakeRunner{}, time.Hour)

	// "path" is an identity/main-app field (PLAN.md §7) and must be
	// structurally unreachable via Tune, not merely absent from the TUI's
	// menu (ipc.GlobalTunableFields/AppTunableFields).
	if _, err := l.Tune("app", "wonen31", "path", "/somewhere/else"); err == nil {
		t.Fatal("Tune: expected an error for field \"path\", got nil")
	}
	if _, err := l.Tune("global", "", "socket_path", "/tmp/other.sock"); err == nil {
		t.Fatal("Tune: expected an error for field \"socket_path\", got nil")
	}
}

func TestTune_UnknownAppRejected(t *testing.T) {
	app := tuneValidApp(t, "wonen31")
	l := startTuneTestLoop(t, tuneValidGlobal(), []config.AppConfig{app}, &fakeRunner{}, time.Hour)

	if _, err := l.Tune("app", "does-not-exist", ipc.FieldMaxLoad, "3.0"); err == nil {
		t.Fatal("Tune: expected an error for an unknown app name, got nil")
	}
}

func TestTune_UnknownScopeRejected(t *testing.T) {
	app := tuneValidApp(t, "wonen31")
	l := startTuneTestLoop(t, tuneValidGlobal(), []config.AppConfig{app}, &fakeRunner{}, time.Hour)

	if _, err := l.Tune("bogus", "", ipc.FieldMaxSystemLoad, "5.0"); err == nil {
		t.Fatal("Tune: expected an error for an unknown scope, got nil")
	}
}
