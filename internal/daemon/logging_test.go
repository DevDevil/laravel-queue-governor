package daemon

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DevDevil/laravel-queue-governor/internal/config"
	"github.com/DevDevil/laravel-queue-governor/internal/logsink"
	"github.com/DevDevil/laravel-queue-governor/internal/metrics"
	"github.com/DevDevil/laravel-queue-governor/internal/runner"
)

func newLoggingLoop(t *testing.T, src metrics.Source, fr *fakeRunner) (*Loop, string) {
	t.Helper()
	dir := t.TempDir()
	logs := logsink.New(&bytes.Buffer{})
	if err := logs.Configure(logsink.Resolve(dir, map[string]string{"wonen31": ""})); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(logs.Close)
	l, _ := newTestLoop(t, testGlobal(), []config.AppConfig{testApp("wonen31")}, src, fr)
	l.Logs = logs
	l.Stderr = logs.Daemon()
	return l, dir
}

func readLog(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func runOneTick(t *testing.T, l *Loop) {
	t.Helper()
	results := make(chan runResult, 4)
	l.tick(context.Background(), results, make(chan runStart, 4), make(chan runJobEvent, 16))
	select {
	case r := <-results:
		l.complete(r)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for run result")
	}
}

func TestLogging_AppLogGetsDecisionsOutputAndResult(t *testing.T) {
	fr := &fakeRunner{result: runner.RunResult{ExitCode: 0, Duration: 1500 * time.Millisecond}}
	l, dir := newLoggingLoop(t, fakeSource{m: metrics.Metrics{Load1: 1, IOWaitPct: 5}}, fr)

	runOneTick(t, l)
	// Fixed clock: the app now sits in its cooldown — the same skip
	// reason three ticks running must be logged once.
	for i := 0; i < 3; i++ {
		l.tick(context.Background(), make(chan runResult, 1), make(chan runStart, 1), make(chan runJobEvent, 1))
	}

	got := readLog(t, filepath.Join(dir, "wonen31.log"))
	for _, want := range []string{
		"dispatched job #1 (load 1.00, iowait 5.0%)",
		"[queue:work pid 1 out] worker says hi",
		"[queue:work pid 1 err] worker warns",
		"run finished: exit 0 after 1.5s",
		"skipped: ",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("wonen31.log missing %q:\n%s", want, got)
		}
	}
	if n := strings.Count(got, "skipped: "); n != 1 {
		t.Errorf("skip logged %d times, want once while the reason doesn't change:\n%s", n, got)
	}
	if d := readLog(t, filepath.Join(dir, "lqg.log")); d != "" {
		t.Errorf("lqg.log = %q, want no per-app detail in the daemon log", d)
	}
}

func TestLogging_RunErrorGoesToBothLogs(t *testing.T) {
	fr := &fakeRunner{failBeforeStart: true, err: os.ErrNotExist}
	l, dir := newLoggingLoop(t, fakeSource{m: metrics.Metrics{Load1: 1, IOWaitPct: 5}}, fr)
	runOneTick(t, l)

	for _, name := range []string{"wonen31.log", "lqg.log"} {
		if got := readLog(t, filepath.Join(dir, name)); !strings.Contains(got, "run failed for wonen31") {
			t.Errorf("%s = %q, want the run error", name, got)
		}
	}
}

func TestLogging_GlobalGateTransitionsInDaemonLog(t *testing.T) {
	src := &mutableSource{}
	src.set(metrics.Metrics{Load1: 9, IOWaitPct: 5})
	l, dir := newLoggingLoop(t, src, &fakeRunner{})
	noop := func() {
		l.tick(context.Background(), make(chan runResult, 4), make(chan runStart, 4), make(chan runJobEvent, 16))
	}

	noop()
	noop()
	src.set(metrics.Metrics{Load1: 1, IOWaitPct: 5})
	noop()

	got := readLog(t, filepath.Join(dir, "lqg.log"))
	if n := strings.Count(got, "above max_system_load"); n != 1 {
		t.Errorf("load-high logged %d times, want once:\n%s", n, got)
	}
	if !strings.Contains(got, "global ceiling clear again") {
		t.Errorf("lqg.log missing the clear line:\n%s", got)
	}
}

func TestLogging_WithoutLogsNothingNewOnStderr(t *testing.T) {
	fr := &fakeRunner{result: runner.RunResult{ExitCode: 0, Duration: time.Second}}
	l, _ := newTestLoop(t, testGlobal(), []config.AppConfig{testApp("wonen31")}, fakeSource{m: metrics.Metrics{Load1: 1, IOWaitPct: 5}}, fr)
	var stderr bytes.Buffer
	l.Stderr = &stderr

	l.Logs = logsink.New(&stderr) // production always has a Manager, just unconfigured
	runOneTick(t, l)
	l.tick(context.Background(), make(chan runResult, 1), make(chan runStart, 1), make(chan runJobEvent, 1))
	l.Scheduler.Global.MaxSystemLoad = 0.5 // global gate now closed
	l.tick(context.Background(), make(chan runResult, 1), make(chan runStart, 1), make(chan runJobEvent, 1))

	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want unchanged (silent) behaviour without log files", stderr.String())
	}
}
