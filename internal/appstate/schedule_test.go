package appstate

import (
	"testing"
	"time"
)

func TestScheduleState_FirstDueIsNextBoundaryPlusOffset(t *testing.T) {
	cases := []struct {
		name   string
		now    time.Time
		offset time.Duration
		want   time.Time
	}{
		{"offset still ahead this minute", at(12, 0, 5), 10 * time.Second, at(12, 0, 10)},
		{"offset already passed", at(12, 0, 30), 10 * time.Second, at(12, 1, 10)},
		{"exactly on due time", at(12, 0, 10), 10 * time.Second, at(12, 0, 10)},
		{"no offset, mid-minute", at(12, 0, 30), 0, at(12, 1, 0)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := NewScheduleState(c.offset)
			if got := s.NextDue(c.now); !got.Equal(c.want) {
				t.Errorf("NextDue = %v, want %v", got, c.want)
			}
		})
	}
}

func TestScheduleState_DueOncePerMinute(t *testing.T) {
	s := NewScheduleState(10 * time.Second)
	s.NextDue(at(12, 0, 0))

	if s.Due(at(12, 0, 9)) {
		t.Fatal("Due before offset")
	}
	if !s.Due(at(12, 0, 10)) {
		t.Fatal("not Due at offset")
	}
	s.OnStart(at(12, 0, 10))
	if s.Due(at(12, 0, 11)) {
		t.Fatal("Due again within the same minute after OnStart")
	}
	if got := s.NextDue(at(12, 0, 11)); !got.Equal(at(12, 1, 10)) {
		t.Errorf("NextDue after start = %v, want 12:01:10", got)
	}
	if !s.Running() {
		t.Error("Running = false after OnStart")
	}

	s.OnComplete(0, 1500*time.Millisecond)
	start, dur, exit, completed, ok := s.LastRun()
	if !ok || !completed || exit != 0 || dur != 1500*time.Millisecond || !start.Equal(at(12, 0, 10)) {
		t.Errorf("LastRun = %v %v %d completed=%v ok=%v", start, dur, exit, completed, ok)
	}
}

func TestScheduleState_StalledAcrossMinutesRunsOnce(t *testing.T) {
	s := NewScheduleState(0)
	s.NextDue(at(12, 0, 30)) // due 12:01:00

	// Daemon stalled until 12:04:20: one due minute resolves, next is 12:05:00.
	if !s.Due(at(12, 4, 20)) {
		t.Fatal("not Due after stall")
	}
	s.OnSkip(at(12, 4, 20), ReasonScheduleOverlap)
	if s.Due(at(12, 4, 21)) {
		t.Error("still Due after resolving the stalled minute")
	}
	if got := s.NextDue(at(12, 4, 21)); !got.Equal(at(12, 5, 0)) {
		t.Errorf("NextDue = %v, want 12:05:00", got)
	}
	if _, reason, ok := s.LastSkip(); !ok || reason != ReasonScheduleOverlap {
		t.Errorf("LastSkip reason = %q ok=%v", reason, ok)
	}
}

func TestScheduleState_SetOffsetRecomputesNextDue(t *testing.T) {
	s := NewScheduleState(0)
	s.NextDue(at(12, 0, 30)) // 12:01:00
	s.SetOffset(45 * time.Second)
	if got := s.NextDue(at(12, 0, 30)); !got.Equal(at(12, 0, 45)) {
		t.Errorf("NextDue after SetOffset = %v, want 12:00:45", got)
	}
}

func TestScheduleState_LastRunNotCompletedWhileRunning(t *testing.T) {
	s := NewScheduleState(0)
	s.OnStart(at(12, 0, 0))
	if _, _, _, completed, ok := s.LastRun(); !ok || completed {
		t.Errorf("completed=%v ok=%v, want ok but not completed", completed, ok)
	}
}

func at(h, m, sec int) time.Time {
	return time.Date(2026, 1, 1, h, m, sec, 0, time.UTC)
}
