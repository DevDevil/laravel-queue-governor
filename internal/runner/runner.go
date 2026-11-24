// Package runner spawns artisan and reports what happened. ExecRunner (in
// exec_runner.go) execs an explicit argv with SysProcAttr.Credential for
// privilege drop — no sh -c, ever. The Runner interface below is what lets
// the scheduler be built and tested against a mock instead of a real
// process.
package runner

import (
	"context"
	"io"
	"time"
)

// RunSpec is an explicit argv for a single artisan invocation, built only
// from validated config — job/queue content never touches a shell.
type RunSpec struct {
	PHPBinary string
	Dir       string   // app path; artisan runs from here
	Args      []string // e.g. ["artisan", "queue:work", "redis", "--queue=default", "--once"]
	UID       uint32
	GID       uint32

	// Nice is the OS scheduling niceness to apply to the child process
	// (-20 highest .. 19 lowest priority; 0 leaves it untouched) —
	// already resolved to its effective value (per-app override or
	// global default) by BuildSpec, so ExecRunner never needs to know
	// about precedence.
	Nice int
}

// RunResult is what the scheduler/FSM sees after a run completes. Duration
// feeds the queue_idle heuristic (PLAN.md §6): a near-instant return with
// no evidence of work is inferred, post-hoc, as an empty queue.
type RunResult struct {
	ExitCode int
	Duration time.Duration
}

// RunHooks are optional callbacks a Runner invokes during a run, before
// it returns. Callers may leave either field nil — implementations must
// only call a hook that's set.
type RunHooks struct {
	// OnStart is called once, synchronously, as soon as the child process
	// has actually started (before Run blocks waiting for it to exit) —
	// the caller uses it to learn the PID for display purposes (TUI.md
	// apps panel's per-job sub-items); never called at all if the
	// process fails to start.
	OnStart func(pid int)

	// OnJobEvent is called once per recognized `--json` line the child
	// prints to stdout (TUI.md apps panel's per-job status) — never
	// called at all for an app that isn't configured with json_output,
	// since its stdout won't parse as a JobEvent.
	OnJobEvent func(JobEvent)

	// Output, if set, is called once right after the child starts, for
	// each of its two streams ("stdout", "stderr"), and every line the
	// child prints on that stream is written to the returned writer
	// (one Write per line, newline included) — the per-app log file
	// (PLAN.md §8.1). Nil keeps the old behaviour: stdout is only parsed
	// for --json events, stderr goes to /dev/null.
	Output func(pid int, stream string) io.Writer
}

// Runner is implemented by the real setuid-exec runner in production and by
// a mock in scheduler tests that records dispatches and returns canned
// results.
type Runner interface {
	Run(ctx context.Context, spec RunSpec, hooks RunHooks) (RunResult, error)
}
