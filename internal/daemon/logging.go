package daemon

import (
	"fmt"
	"io"
	"time"

	"github.com/DevDevil/laravel-queue-governor/internal/runner"
	"github.com/DevDevil/laravel-queue-governor/internal/scheduler"
)

// Per-app and global log lines (PLAN.md §8.1). Without log files
// configured (Loop.Logs nil, or no file for that app) nothing here writes
// anything new: the only per-app lines that existed before — errors and
// schedule:run skips/failures — keep going to l.Stderr exactly as they
// did. With a file for the app, those lines go to its file instead, plus
// its decisions and worker output.

// hasAppLog reports whether app has a log file of its own.
func (l *Loop) hasAppLog(app *scheduler.App) bool {
	return l.Logs != nil && l.Logs.HasApp(app.Config.Name)
}

// appLogf writes one line about app: to its own log when it has one,
// otherwise to l.Stderr prefixed as before. alsoDaemon additionally puts
// it in the daemon log when it went to the app's file — used for errors,
// which an operator watching only lqg.log should still see.
func (l *Loop) appLogf(app *scheduler.App, alsoDaemon bool, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	if !l.hasAppLog(app) {
		fmt.Fprintf(l.Stderr, "%s\n", msg)
		return
	}
	fmt.Fprintf(l.Logs.App(app.Config.Name, ""), "%s\n", msg)
	if alsoDaemon {
		fmt.Fprintf(l.Stderr, "%s\n", msg)
	}
}

// appOnlyf writes one line to app's own log, and nowhere if it has none —
// for the new per-app detail (decisions, run results) that never went to
// stderr before and would flood the journal if it did.
func (l *Loop) appOnlyf(app *scheduler.App, format string, args ...any) {
	if l.hasAppLog(app) {
		fmt.Fprintf(l.Logs.App(app.Config.Name, ""), format+"\n", args...)
	}
}

// workerOutput is the runner hook that sends a worker's stdout/stderr to
// app's log, tagged with its pid and what it is. nil when app has no log
// (the runner then keeps stderr on /dev/null, as before).
func (l *Loop) workerOutput(app *scheduler.App, what string) func(pid int, stream string) io.Writer {
	if !l.hasAppLog(app) {
		return nil
	}
	name := app.Config.Name
	return func(pid int, stream string) io.Writer {
		tag := "out"
		if stream == "stderr" {
			tag = "err"
		}
		return l.Logs.App(name, fmt.Sprintf("[%s pid %d %s] ", what, pid, tag))
	}
}

// logDecisions writes this tick's decisions to each app's log: every
// dispatch, but a skip only when its reason differs from the last one
// logged for that app — every app gets a decision every tick, and
// "skipped: queue_idle" every 5s forever is noise, not a log.
func (l *Loop) logDecisions(decisions []scheduler.Decision) {
	for i, d := range decisions {
		app := l.Scheduler.Apps[i]
		if !l.hasAppLog(app) {
			continue
		}
		if d.Dispatched {
			l.lastSkip[app] = ""
			l.appOnlyf(app, "dispatched job #%d (load %.2f, iowait %.1f%%)", d.JobSeq, d.Load, d.IOWait)
			continue
		}
		if l.lastSkip[app] == d.Reason {
			continue
		}
		l.lastSkip[app] = d.Reason
		l.appOnlyf(app, "skipped: %s (state %s, load %.2f, iowait %.1f%%)", d.Reason, d.State, d.Load, d.IOWait)
	}
}

// logGlobalGate writes a line to the daemon log whenever the global load
// or iowait ceiling starts or stops holding back every app — the global
// decisions lqg.log is for. Same comparison as the scheduler's gate; only
// the kind (load / iowait / clear) is tracked, so a moving load average
// doesn't produce a line every tick.
//
// Only with lqg.log configured: without log files the journal output
// stays exactly what it was.
func (l *Loop) logGlobalGate(decisions []scheduler.Decision) {
	if len(decisions) == 0 || l.Logs == nil || !l.Logs.HasDaemonFile() {
		return
	}
	load, iowait := decisions[0].Load, decisions[0].IOWait
	g := l.Scheduler.Global
	kind := ""
	switch {
	case load > g.MaxSystemLoad:
		kind = "load"
	case iowait > float64(g.MaxIOWait):
		kind = "iowait"
	}
	if kind == l.globalGate {
		return
	}
	l.globalGate = kind
	switch kind {
	case "load":
		fmt.Fprintf(l.Stderr, "global load %.2f above max_system_load %.2f — holding all apps\n", load, g.MaxSystemLoad)
	case "iowait":
		fmt.Fprintf(l.Stderr, "global iowait %.1f%% above max_iowait %d%% — holding all apps\n", iowait, g.MaxIOWait)
	default:
		fmt.Fprintf(l.Stderr, "global ceiling clear again (load %.2f, iowait %.1f%%) — dispatching resumes\n", load, iowait)
	}
}

// logRunComplete writes a finished queue:work run's outcome to app's log.
func (l *Loop) logRunComplete(app *scheduler.App, r runner.RunResult) {
	l.appOnlyf(app, "run finished: exit %d after %s", r.ExitCode, r.Duration.Round(time.Millisecond))
}

// logSchedule writes a schedule:run decision to app's log. Skips keep
// going to stderr when the app has no log of its own, as they did before.
func (l *Loop) logSchedule(d scheduler.ScheduleDecision) {
	if d.Run {
		l.appOnlyf(d.App, "schedule:run started (load %.2f, iowait %.1f%%)", d.Load, d.IOWait)
		return
	}
	l.appLogf(d.App, false, "schedule:run for %s skipped: %s", d.App.Config.Name, d.Reason)
}
