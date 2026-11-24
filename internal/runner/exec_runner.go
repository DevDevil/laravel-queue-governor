package runner

import (
	"bufio"
	"bytes"
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

	// stderr is only piped when someone wants it (hooks.Output, i.e. a
	// per-app log file); otherwise it stays nil = /dev/null, as before.
	var stderr io.ReadCloser
	if hooks.Output != nil {
		stderr, err = cmd.StderrPipe()
		if err != nil {
			return RunResult{ExitCode: -1}, fmt.Errorf("stderr pipe for %s in %s: %w", spec.PHPBinary, spec.Dir, err)
		}
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

	// Drain stdout (and stderr, when piped) on its own goroutine, concurrently with the child
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
	var stdoutLog, stderrLog io.Writer
	if hooks.Output != nil {
		stdoutLog = hooks.Output(cmd.Process.Pid, "stdout")
		stderrLog = hooks.Output(cmd.Process.Pid, "stderr")
	}
	stdoutDone := make(chan struct{})
	go func() {
		defer close(stdoutDone)
		var stderrDone chan struct{}
		if stderr != nil {
			stderrDone = make(chan struct{})
			go func() {
				defer close(stderrDone)
				drainLines(stderr, func(line []byte) { writeLine(stderrLog, line) })
			}()
		}
		drainLines(stdout, func(line []byte) {
			writeLine(stdoutLog, line)
			if hooks.OnJobEvent == nil {
				return
			}
			if e, ok := parseJobEvent(line); ok {
				hooks.OnJobEvent(e)
			}
		})
		if stderrDone != nil {
			<-stderrDone
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

// maxLineChunk bounds one emitted line: a longer line is emitted in
// chunks rather than buffered without limit — and, unlike
// bufio.Scanner's ErrTooLong, never stops draining, which would leave the
// child blocked on a full pipe.
const maxLineChunk = 64 * 1024

// drainLines reads r to EOF (or a read error), calling fn with each line
// minus its trailing newline. A final unterminated line is still emitted.
func drainLines(r io.Reader, fn func([]byte)) {
	br := bufio.NewReaderSize(r, maxLineChunk)
	for {
		line, err := br.ReadSlice('\n')
		if len(line) > 0 {
			fn(bytes.TrimSuffix(line, []byte("\n")))
		}
		if err != nil && err != bufio.ErrBufferFull {
			return
		}
	}
}

// writeLine writes line plus a newline to w as a single Write; nil w is a
// no-op.
func writeLine(w io.Writer, line []byte) {
	if w == nil {
		return
	}
	buf := make([]byte, 0, len(line)+1)
	buf = append(buf, line...)
	w.Write(append(buf, '\n'))
}
