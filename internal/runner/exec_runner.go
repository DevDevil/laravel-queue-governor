package runner

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"syscall"
	"time"
)

// ExecRunner is the production Runner: it execs php_binary with an
// explicit argv — never sh -c, ever (CLAUDE.md) — and drops privileges to
// the configured user via SysProcAttr.Credential before the child process
// starts. uid/gid come only from validated config (ticket 2's
// AppConfig.ResolvedUID/ResolvedGID); ExecRunner never resolves a user
// itself.
//
// A context cancellation kills the child (SIGKILL, via exec.CommandContext)
// and surfaces the same way as any other signal-terminated process: no
// error, RunResult.ExitCode == -1. Distinguishing "killed by us" from
// "killed by something else" is left to the caller's own ctx.Err() check.
type ExecRunner struct {
	// Stderr, if set, receives a warning if applying spec.Nice fails —
	// non-fatal (the job still ran, just not at the intended scheduling
	// priority), same "log and continue" convention as the rest of the
	// daemon loop.
	Stderr io.Writer
}

var _ Runner = ExecRunner{}

func (r ExecRunner) Run(ctx context.Context, spec RunSpec, hooks RunHooks) (RunResult, error) {
	cmd := exec.CommandContext(ctx, spec.PHPBinary, spec.Args...)
	cmd.Dir = spec.Dir
	cmd.SysProcAttr = &syscall.SysProcAttr{
		// NoSetGroups: supplementary groups aren't resolved by config
		// validation (ticket 2 only resolves primary uid/gid), so we don't
		// ask the kernel to touch them — without this, Go always calls
		// setgroups(2) to clear them when Credential.Groups is nil, which
		// needs CAP_SETGID even when uid/gid already match the caller.
		Credential: &syscall.Credential{Uid: spec.UID, Gid: spec.GID, NoSetGroups: true},
	}

	// Piped (not left nil/discarded to /dev/null) so a json_output app's
	// --json lines can be parsed for real per-job status (TUI.md apps
	// panel). Harmless for an app that isn't configured with json_output
	// too — its plain-text output just never parses as a JobEvent below.
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return RunResult{ExitCode: -1}, fmt.Errorf("stdout pipe for %s in %s: %w", spec.PHPBinary, spec.Dir, err)
	}

	start := time.Now()
	if err := cmd.Start(); err != nil {
		return RunResult{ExitCode: -1, Duration: time.Since(start)}, fmt.Errorf("starting %s in %s: %w", spec.PHPBinary, spec.Dir, err)
	}
	if hooks.OnStart != nil {
		hooks.OnStart(cmd.Process.Pid)
	}

	// Set niceness on the already-started child by PID, from here (the
	// parent), rather than wrapping the argv in an external `nice`
	// binary or trying to change our own thread's priority before exec —
	// this needs no shell, no extra binary dependency, and works
	// regardless of which OS thread the runtime happened to fork from.
	// The brief window between Start() and here at the child's inherited
	// (pre-adjustment) niceness is inconsequential for a scheduling hint.
	// Always called, even for spec.Nice == 0: config.AppConfig.Nice is a
	// pointer specifically so an app can explicitly opt back into nice 0
	// even when global.nice is non-zero (PLAN.md §7) — skipping the call
	// at 0 would make that override indistinguishable from "unset" if the
	// daemon process itself were ever niced (it currently isn't, but
	// nothing should depend on that to stay correct).
	if err := syscall.Setpriority(syscall.PRIO_PROCESS, cmd.Process.Pid, spec.Nice); err != nil && r.Stderr != nil {
		fmt.Fprintf(r.Stderr, "warning: setting nice=%d for %s (pid %d): %v\n", spec.Nice, spec.Dir, cmd.Process.Pid, err)
	}

	// Drain stdout on its own goroutine, concurrently with the child
	// running, so a chatty --json batch run can't fill the pipe buffer
	// and stall the child on a blocked write. Waited for below before
	// calling cmd.Wait() in the normal case — needed to reliably capture
	// every line, since exec.Cmd's own docs call calling Wait() before
	// all reads complete "incorrect" (Wait() can close out from under an
	// in-flight read). But NOT waited for unconditionally: if the child
	// forks its own grandchild that inherits the stdout fd (arbitrary
	// PHP/Laravel code can do this) and that grandchild outlives the
	// child — which ctx cancellation's SIGKILL guarantees for the child
	// but says nothing about any grandchild — the pipe's write end stays
	// open long after the process we actually care about has exited, and
	// unconditionally waiting for EOF here would hang Run() (and so
	// daemon shutdown) for however long that orphan happens to keep
	// running. So: wait for either a clean drain or ctx being canceled,
	// whichever comes first; on cancellation the goroutine is simply left
	// to keep running in the background (same tolerance this package
	// already has for the results/starts channels outliving Run in
	// daemon.Loop) rather than blocking shutdown on it.
	stdoutDone := make(chan struct{})
	go func() {
		defer close(stdoutDone)
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			if hooks.OnJobEvent == nil {
				continue
			}
			if e, ok := parseJobEvent(scanner.Bytes()); ok {
				hooks.OnJobEvent(e)
			}
		}
	}()
	// Note this select can also resolve via ctx.Done() on a plain
	// cancellation, not just the grandchild scenario above: SIGKILL is
	// sent but the child's actual exit (and the scanner goroutine noticing
	// EOF) isn't synchronous with ctx.Done() firing. So cmd.Wait() below
	// can race the still-running scanner goroutine even without a
	// grandchild involved — accepted as the same "incorrect but benign in
	// practice" tradeoff described above, since correctness here would
	// mean blocking shutdown on an arbitrarily slow drain instead.
	select {
	case <-stdoutDone:
	case <-ctx.Done():
	}

	err = cmd.Wait()
	duration := time.Since(start)

	var exitErr *exec.ExitError
	switch {
	case err == nil:
		return RunResult{ExitCode: 0, Duration: duration}, nil
	case errors.As(err, &exitErr):
		// The process ran and exited non-zero — that's data for the
		// scheduler (a job/batch failure), not a runner-level error.
		return RunResult{ExitCode: exitErr.ExitCode(), Duration: duration}, nil
	default:
		// Never even ran (binary missing, permission denied, context
		// canceled, ...) — this is a real runner failure.
		return RunResult{ExitCode: -1, Duration: duration}, fmt.Errorf("running %s in %s: %w", spec.PHPBinary, spec.Dir, err)
	}
}
