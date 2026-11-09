package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/DevDevil/laravel-queue-governor/internal/config"
	"github.com/DevDevil/laravel-queue-governor/internal/runner"
)

// TestLoop_ReloadDuringInFlightRunDoesNotMisattributeCompletion is the
// end-to-end regression test for the exact hazard ticket 14 exists to
// fix: dispatch a run, reload the config so that app is removed from
// Scheduler.Apps while the run is still in flight, then let the run
// complete. The completion must land on the correct (now-orphaned)
// Machine, not panic, and not corrupt the surviving app's state — proving
// runResult's *scheduler.App pointer (not an index) is what makes this
// safe.
func TestLoop_ReloadDuringInFlightRunDoesNotMisattributeCompletion(t *testing.T) {
	release := make(chan struct{})
	fr := &fakeRunner{release: release, result: runner.RunResult{ExitCode: 0, Duration: time.Second}}

	global := testGlobal()
	// Budget 1 with two equal-priority, never-dispatched apps: fair
	// dispatch's stable sort picks "doomed" (index 0) this tick and
	// leaves "survivor" not_scheduled — so only one goroutine ever calls
	// the shared fakeRunner, and there's no ambiguity about which
	// result "release" unblocks.
	global.MaxConcurrentJobs = 1
	l, sched := newTestLoop(t, global, []config.AppConfig{testApp("doomed"), testApp("survivor")}, fakeSource{}, fr)

	results := make(chan runResult, 4)
	starts := make(chan runStart, 4)
	jobEvents := make(chan runJobEvent, 8)
	l.tick(context.Background(), results, starts, jobEvents) // dispatches "doomed"; its run blocks on release

	doomed := sched.Apps[0]
	if doomed.Config.Name != "doomed" || doomed.Machine.RunningCount() != 1 {
		t.Fatalf("expected 'doomed' dispatched and running, got %+v", doomed)
	}

	// Reload: "doomed" is removed entirely; "survivor" stays.
	l.applyReload(reloadRequest{global: global, apps: []config.AppConfig{testApp("survivor")}})

	for _, a := range sched.Apps {
		if a.Config.Name == "doomed" {
			t.Fatal("'doomed' should be gone from Scheduler.Apps after reload")
		}
	}

	// Now let "doomed"'s run finish and flow through the normal
	// results-channel path, same as Run's own select loop would.
	close(release)
	var r runResult
	select {
	case r = <-results:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the orphaned run to complete")
	}
	if r.app != doomed {
		t.Fatalf("runResult.app = %v, want the orphaned 'doomed' *App", r.app.Config.Name)
	}

	l.complete(r) // must not panic, must not touch "survivor"

	if doomed.Machine.RunningCount() != 0 {
		t.Errorf("orphaned app's RunningCount = %d, want 0 after completion", doomed.Machine.RunningCount())
	}
	survivor := sched.Apps[0] // only app left
	if survivor.Config.Name != "survivor" || survivor.Machine.RunningCount() != 0 {
		t.Errorf("survivor state corrupted by the orphaned completion: %+v", survivor)
	}
}
