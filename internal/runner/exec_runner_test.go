package runner

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func writeScript(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// selfCredential runs the child as the current process's own uid/gid —
// setting Credential to yourself is always permitted, so these tests
// exercise the real setuid-on-exec code path without needing root.
func selfCredential() (uint32, uint32) {
	return uint32(os.Getuid()), uint32(os.Getgid())
}

func TestExecRunner_Success(t *testing.T) {
	dir := t.TempDir()
	script := writeScript(t, dir, "ok.sh", "#!/bin/sh\npwd > out.txt\necho \"$@\" >> out.txt\nexit 0\n")
	uid, gid := selfCredential()

	r := ExecRunner{}
	result, err := r.Run(context.Background(), RunSpec{
		PHPBinary: script,
		Dir:       dir,
		Args:      []string{"artisan", "queue:work", "--once"},
		UID:       uid,
		GID:       gid,
	}, RunHooks{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", result.ExitCode)
	}
	if result.Duration <= 0 {
		t.Errorf("Duration = %v, want > 0", result.Duration)
	}

	out, err := os.ReadFile(filepath.Join(dir, "out.txt"))
	if err != nil {
		t.Fatalf("reading out.txt: %v", err)
	}
	got := string(out)
	if !strings.Contains(got, dir) {
		t.Errorf("script did not run with Dir=%s, got %q", dir, got)
	}
	if !strings.Contains(got, "artisan queue:work --once") {
		t.Errorf("script did not receive expected args, got %q", got)
	}
}

func TestExecRunner_OnStartReceivesRealPID(t *testing.T) {
	dir := t.TempDir()
	script := writeScript(t, dir, "pid.sh", "#!/bin/sh\necho $$ > pid.txt\n")
	uid, gid := selfCredential()

	var gotPID int
	r := ExecRunner{}
	_, err := r.Run(context.Background(), RunSpec{PHPBinary: script, Dir: dir, UID: uid, GID: gid}, RunHooks{OnStart: func(pid int) {
		gotPID = pid
	}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if gotPID <= 0 {
		t.Fatalf("onStart pid = %d, want a positive pid", gotPID)
	}

	out, err := os.ReadFile(filepath.Join(dir, "pid.txt"))
	if err != nil {
		t.Fatalf("reading pid.txt: %v", err)
	}
	want := strings.TrimSpace(string(out))
	if strconv.Itoa(gotPID) != want {
		t.Errorf("onStart pid = %d, want %s (the child's own $$)", gotPID, want)
	}
}

func TestExecRunner_OnJobEventParsesJSONLines(t *testing.T) {
	dir := t.TempDir()
	// Mirrors the two lines a real `queue:work --json` run produces for
	// one job: a "starting" event with no duration, then a terminal
	// "success" event with one (Laravel's WorkCommand::writeOutputAsJson).
	script := writeScript(t, dir, "json.sh", `#!/bin/sh
printf '%s\n' '{"status":"starting","job":"App\\Jobs\\ExampleJob","attempts":1,"timestamp":"2026-01-01T00:00:00.000000+00:00"}'
printf '%s\n' '{"status":"success","job":"App\\Jobs\\ExampleJob","attempts":1,"duration":0.123,"timestamp":"2026-01-01T00:00:00.123000+00:00"}'
`)
	uid, gid := selfCredential()

	var events []JobEvent
	r := ExecRunner{}
	_, err := r.Run(context.Background(), RunSpec{PHPBinary: script, Dir: dir, UID: uid, GID: gid}, RunHooks{
		OnJobEvent: func(e JobEvent) { events = append(events, e) },
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	want := []JobEvent{
		{Status: "starting", Job: `App\Jobs\ExampleJob`},
		{Status: "success", Job: `App\Jobs\ExampleJob`},
	}
	if len(events) != 2 || events[0] != want[0] || events[1] != want[1] {
		t.Fatalf("events = %+v, want %+v", events, want)
	}
}

func TestExecRunner_PlainOutputNeverTriggersOnJobEvent(t *testing.T) {
	dir := t.TempDir()
	// An app without json_output configured: ordinary human-readable
	// queue:work-style output, no --json — none of this should ever
	// parse as a JobEvent.
	script := writeScript(t, dir, "plain.sh", `#!/bin/sh
echo "   INFO  Processing jobs from the [default] queue."
echo "  2026-01-01 00:00:00 App\\Jobs\\ExampleJob ........... RUNNING"
`)
	uid, gid := selfCredential()

	called := false
	r := ExecRunner{}
	_, err := r.Run(context.Background(), RunSpec{PHPBinary: script, Dir: dir, UID: uid, GID: gid}, RunHooks{
		OnJobEvent: func(JobEvent) { called = true },
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if called {
		t.Error("OnJobEvent fired for plain (non-JSON) output, want it to stay silent")
	}
}

func TestExecRunner_NonZeroExitIsNotAnError(t *testing.T) {
	dir := t.TempDir()
	script := writeScript(t, dir, "fail.sh", "#!/bin/sh\nexit 7\n")
	uid, gid := selfCredential()

	r := ExecRunner{}
	result, err := r.Run(context.Background(), RunSpec{PHPBinary: script, Dir: dir, UID: uid, GID: gid}, RunHooks{})
	if err != nil {
		t.Fatalf("Run: unexpected error for a normal non-zero exit: %v", err)
	}
	if result.ExitCode != 7 {
		t.Errorf("ExitCode = %d, want 7", result.ExitCode)
	}
}

func TestExecRunner_AppliesNiceToChild(t *testing.T) {
	dir := t.TempDir()
	// /proc/self/stat's 19th space-separated field is the child's own
	// niceness — reading it from inside the child is the only way to
	// confirm Setpriority actually reached the right process, since by
	// the time Run() returns the process is gone and /proc/<pid> with
	// it. Positive nice only: a non-root test process can raise its own
	// child's niceness (0 -> 10) without CAP_SYS_NICE, unlike lowering it.
	script := writeScript(t, dir, "nice.sh", "#!/bin/sh\ncut -d ' ' -f19 /proc/self/stat > nice.txt\n")
	uid, gid := selfCredential()

	r := ExecRunner{}
	_, err := r.Run(context.Background(), RunSpec{PHPBinary: script, Dir: dir, UID: uid, GID: gid, Nice: 10}, RunHooks{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	out, err := os.ReadFile(filepath.Join(dir, "nice.txt"))
	if err != nil {
		t.Fatalf("reading nice.txt: %v", err)
	}
	if got := strings.TrimSpace(string(out)); got != "10" {
		t.Errorf("child's own reported niceness = %q, want 10", got)
	}
}

func TestExecRunner_NiceZeroIsAppliedExplicitlyNotJustInherited(t *testing.T) {
	dir := t.TempDir()
	script := writeScript(t, dir, "nice.sh", "#!/bin/sh\ncut -d ' ' -f19 /proc/self/stat > nice.txt\n")
	uid, gid := selfCredential()

	r := ExecRunner{}
	// Nice: 0 (the zero value) must still call Setpriority explicitly, not
	// be skipped — config.AppConfig.Nice is a pointer specifically so an
	// app can opt back into an *absolute* nice 0 even when global.nice is
	// non-zero (PLAN.md §7); silently leaving the child at whatever it
	// inherited would only coincidentally match 0 when the daemon process
	// itself happens to also be nice 0.
	_, err := r.Run(context.Background(), RunSpec{PHPBinary: script, Dir: dir, UID: uid, GID: gid, Nice: 0}, RunHooks{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	out, err := os.ReadFile(filepath.Join(dir, "nice.txt"))
	if err != nil {
		t.Fatalf("reading nice.txt: %v", err)
	}
	if got := strings.TrimSpace(string(out)); got != "0" {
		t.Errorf("child's own reported niceness = %q, want \"0\" (Setpriority(0) explicitly applied)", got)
	}
}

func TestExecRunner_BinaryNotFound(t *testing.T) {
	dir := t.TempDir()
	uid, gid := selfCredential()

	r := ExecRunner{}
	_, err := r.Run(context.Background(), RunSpec{
		PHPBinary: filepath.Join(dir, "does-not-exist"),
		Dir:       dir,
		UID:       uid,
		GID:       gid,
	}, RunHooks{})
	if err == nil {
		t.Fatal("Run: expected error for missing binary, got nil")
	}
}

func TestExecRunner_ContextCancellation(t *testing.T) {
	dir := t.TempDir()
	script := writeScript(t, dir, "sleep.sh", "#!/bin/sh\nsleep 5\n")
	uid, gid := selfCredential()

	ctx, cancel := context.WithCancel(context.Background())
	r := ExecRunner{}

	done := make(chan struct{})
	var result RunResult
	var runErr error
	go func() {
		result, runErr = r.Run(ctx, RunSpec{PHPBinary: script, Dir: dir, UID: uid, GID: gid}, RunHooks{})
		close(done)
	}()

	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return promptly after context cancellation")
	}

	// A killed-by-signal process still surfaces via *exec.ExitError (Go
	// reports ExitCode() -1 for it), which ExecRunner treats the same as
	// any other "process ran, exited non-zero" outcome — err stays nil.
	if runErr != nil {
		t.Fatalf("Run: unexpected error after context cancellation: %v", runErr)
	}
	if result.ExitCode != -1 {
		t.Errorf("ExitCode = %d, want -1 (killed by signal)", result.ExitCode)
	}
	if result.Duration >= 5*time.Second {
		t.Errorf("Duration = %v, want well under the 5s sleep (should have been killed)", result.Duration)
	}
}
