package scheduler

import (
	"testing"

	"github.com/DevDevil/laravel-queue-governor/internal/config"
)

func TestReload_ExistingAppKeepsMachineState(t *testing.T) {
	s := New(testGlobal(), []config.AppConfig{testApp("a")}, fakeSource{})
	original := s.Apps[0]
	original.Machine.OnDispatch(base)

	if original.Machine.RunningCount() != 1 {
		t.Fatalf("RunningCount before reload = %d, want 1", original.Machine.RunningCount())
	}

	newApp := testApp("a")
	newApp.Priority = 99 // config change
	s.Reload(testGlobal(), []config.AppConfig{newApp})

	if len(s.Apps) != 1 {
		t.Fatalf("len(Apps) after reload = %d, want 1", len(s.Apps))
	}
	if s.Apps[0] != original {
		t.Error("existing app's *App should be the same pointer after reload — state must be preserved, not reset")
	}
	if s.Apps[0].Machine.RunningCount() != 1 {
		t.Errorf("RunningCount after reload = %d, want 1 (preserved)", s.Apps[0].Machine.RunningCount())
	}
	if s.Apps[0].Config.Priority != 99 {
		t.Errorf("Config.Priority after reload = %d, want 99 (updated)", s.Apps[0].Config.Priority)
	}
}

func TestReload_NewAppGetsFreshMachine(t *testing.T) {
	s := New(testGlobal(), []config.AppConfig{testApp("a")}, fakeSource{})
	s.Reload(testGlobal(), []config.AppConfig{testApp("a"), testApp("b")})

	if len(s.Apps) != 2 {
		t.Fatalf("len(Apps) = %d, want 2", len(s.Apps))
	}
	r := s.Apps[1].Machine.Evaluate(base, false, false)
	if !r.Eligible {
		t.Errorf("new app's Machine.Evaluate = %+v, want a fresh, eligible Machine", r)
	}
}

func TestReload_RemovedAppDroppedFromApps(t *testing.T) {
	s := New(testGlobal(), []config.AppConfig{testApp("a"), testApp("b")}, fakeSource{})
	s.Reload(testGlobal(), []config.AppConfig{testApp("a")})

	if len(s.Apps) != 1 || s.Apps[0].Config.Name != "a" {
		t.Errorf("Apps after reload = %+v, want just [a]", s.Apps)
	}
}

func TestReload_RemovedAppsMachineStillUsableViaHeldPointer(t *testing.T) {
	// This is the regression test for the exact hazard ticket 14 exists to
	// fix: the daemon loop holds a *App pointer for an in-flight run, not
	// an index. Reload must not invalidate or corrupt that pointer even
	// though the app it points to is no longer in Apps.
	s := New(testGlobal(), []config.AppConfig{testApp("a"), testApp("b")}, fakeSource{})
	inFlight := s.Apps[1] // "b" — simulates a run dispatched before reload
	inFlight.Machine.OnDispatch(base)

	s.Reload(testGlobal(), []config.AppConfig{testApp("a")}) // "b" removed

	for _, a := range s.Apps {
		if a.Config.Name == "b" {
			t.Fatal("removed app should not be in Apps after reload")
		}
	}

	// Completing the held-onto run must not panic and must affect the
	// right (now-orphaned) Machine, not some other app's.
	inFlight.Machine.OnRunComplete(base, true, false, true)
	if inFlight.Machine.RunningCount() != 0 {
		t.Errorf("RunningCount on the orphaned app = %d, want 0 after completion", inFlight.Machine.RunningCount())
	}
	if s.Apps[0].Machine.RunningCount() != 0 {
		t.Errorf("surviving app's RunningCount = %d, want 0 (unaffected)", s.Apps[0].Machine.RunningCount())
	}
}

func TestReload_UpdatesGlobal(t *testing.T) {
	s := New(testGlobal(), []config.AppConfig{testApp("a")}, fakeSource{})

	newGlobal := testGlobal()
	newGlobal.MaxSystemLoad = 2.0
	s.Reload(newGlobal, []config.AppConfig{testApp("a")})

	if s.Global.MaxSystemLoad != 2.0 {
		t.Errorf("Global.MaxSystemLoad after reload = %v, want 2.0", s.Global.MaxSystemLoad)
	}
}
