package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/DevDevil/laravel-queue-governor/internal/config"
	"github.com/DevDevil/laravel-queue-governor/internal/ipc"
	"github.com/DevDevil/laravel-queue-governor/internal/metrics"
	"github.com/DevDevil/laravel-queue-governor/internal/runner"
)

func schedTestApp(name string) config.AppConfig {
	a := testApp(name)
	a.Scheduler.Enabled = true
	return a
}

func TestLoop_ScheduleTimerNilWithoutScheduler(t *testing.T) {
	l, _ := newTestLoop(t, testGlobal(), []config.AppConfig{testApp("a")}, fakeSource{}, &fakeRunner{})
	if l.scheduleTimer() != nil {
		t.Error("scheduleTimer != nil with no app having scheduler enabled")
	}
}

func TestLoop_ScheduleTickRunsScheduleAndCompletes(t *testing.T) {
	fr := &fakeRunner{result: runner.RunResult{ExitCode: 0, Duration: 2 * time.Second}}
	l, sched := newTestLoop(t, testGlobal(), []config.AppConfig{schedTestApp("a")}, fakeSource{m: metrics.Metrics{Load1: 1}}, fr)
	if l.scheduleTimer() == nil {
		t.Fatal("scheduleTimer nil with scheduler enabled")
	}

	ch, unsubscribe := l.Bus.Subscribe(8)
	defer unsubscribe()

	results := make(chan scheduleResult, 1)
	l.scheduleTick(context.Background(), results) // base is 00:00:00, offset 0 → due

	ev := <-ch
	if ev.Kind != ipc.KindSchedule || ev.Decision != "dispatched" || ev.Schedule == nil || !ev.Schedule.Running {
		t.Fatalf("event = %+v, want schedule dispatched with running status", ev)
	}

	r := <-results
	if fr.callCount() != 1 {
		t.Fatalf("runner calls = %d, want 1", fr.callCount())
	}
	if got := fr.calls[0].Args; len(got) != 2 || got[1] != "schedule:run" {
		t.Errorf("Args = %v, want artisan schedule:run", got)
	}
	if !sched.Apps[0].Schedule.Running() {
		t.Error("schedule not running before completion is processed")
	}

	l.completeSchedule(r)
	if sched.Apps[0].Schedule.Running() {
		t.Error("schedule still running after completion")
	}
	done := <-ch
	if done.Decision != "completed" || done.Schedule == nil || done.Schedule.LastExitCode == nil || *done.Schedule.LastExitCode != 0 {
		t.Errorf("completed event = %+v", done)
	}
}

func TestLoop_CompleteScheduleAfterDisableIsDropped(t *testing.T) {
	fr := &fakeRunner{}
	l, sched := newTestLoop(t, testGlobal(), []config.AppConfig{schedTestApp("a")}, fakeSource{}, fr)
	results := make(chan scheduleResult, 1)
	l.scheduleTick(context.Background(), results)
	r := <-results

	l.applyReload(reloadRequest{global: testGlobal(), apps: []config.AppConfig{testApp("a")}})
	l.completeSchedule(r) // must not panic on the now-nil Schedule
	if sched.Apps[0].Schedule != nil {
		t.Error("Schedule resurrected by a late completion")
	}
}
