package appstate

import "time"

// ReasonScheduleOverlap is the schedule:run-only skip reason (PLAN.md
// §6.1/§8): this app's previous `schedule:run` is still running when its
// next minute comes due, so this minute is skipped rather than starting a
// second one. Laravel's own withoutOverlapping() only protects individual
// tasks, not the schedule:run process itself.
const ReasonScheduleOverlap Reason = "schedule_overlap"

// ScheduleState is one app's `schedule:run` bookkeeping (PLAN.md §6.1):
// when it is next due, whether a run is in flight, and the outcome of the
// last run/skip for status display. Like Machine, it never reads a clock —
// callers pass `now` — and is not safe for concurrent use: only the daemon
// loop's own goroutine drives it.
//
// Due times are minute boundaries plus a fixed per-app offset (0 when
// stagger is off). A due minute is always resolved exactly once — started
// or skipped — and the next due time is then computed from `now`, so a
// daemon that was stalled across several minutes runs once, not once per
// missed minute (schedule:run only evaluates the current minute anyway).
type ScheduleState struct {
	offset  time.Duration
	nextDue time.Time // zero until first computed

	running      bool
	lastStart    time.Time
	lastDuration time.Duration
	lastExitCode int
	haveLastRun  bool

	lastSkipAt     time.Time
	lastSkipReason Reason
}

// NewScheduleState builds a ScheduleState whose runs fall at `offset`
// past each minute boundary. offset is clamped into [0, 1m).
func NewScheduleState(offset time.Duration) *ScheduleState {
	return &ScheduleState{offset: clampOffset(offset)}
}

func clampOffset(d time.Duration) time.Duration {
	if d < 0 || d >= time.Minute {
		return 0
	}
	return d
}

// SetOffset changes the per-minute offset (e.g. stagger toggled by a
// config reload). The next due time is recomputed from the new offset;
// in-flight and last-run state is kept.
func (s *ScheduleState) SetOffset(offset time.Duration) {
	offset = clampOffset(offset)
	if offset == s.offset {
		return
	}
	s.offset = offset
	s.nextDue = time.Time{}
}

// Offset is this app's fixed offset within each minute.
func (s *ScheduleState) Offset() time.Duration { return s.offset }

// nextAfter is the first `minute boundary + offset` that is not before now.
func (s *ScheduleState) nextAfter(now time.Time) time.Time {
	due := now.Truncate(time.Minute).Add(s.offset)
	if due.Before(now) {
		due = due.Add(time.Minute)
	}
	return due
}

// NextDue is when this app's schedule:run is next due, computing it on
// first use (so a daemon starting at :30 with offset :10 first runs at the
// next minute's :10, not immediately).
func (s *ScheduleState) NextDue(now time.Time) time.Time {
	if s.nextDue.IsZero() {
		s.nextDue = s.nextAfter(now)
	}
	return s.nextDue
}

// Due reports whether this app's current minute has come due and hasn't
// been resolved (started or skipped) yet.
func (s *ScheduleState) Due(now time.Time) bool {
	return !now.Before(s.NextDue(now))
}

// advance moves nextDue past the minute just resolved at now.
func (s *ScheduleState) advance(now time.Time) {
	next := now.Truncate(time.Minute).Add(s.offset)
	if !next.After(now) {
		next = next.Add(time.Minute)
	}
	s.nextDue = next
}

// OnStart records that schedule:run was started for the due minute.
func (s *ScheduleState) OnStart(now time.Time) {
	s.running = true
	s.lastStart = now
	s.advance(now)
}

// OnSkip records that the due minute was skipped, and why.
func (s *ScheduleState) OnSkip(now time.Time, reason Reason) {
	s.lastSkipAt = now
	s.lastSkipReason = reason
	s.advance(now)
}

// OnComplete records the outcome of the in-flight run.
func (s *ScheduleState) OnComplete(exitCode int, duration time.Duration) {
	s.running = false
	s.lastExitCode = exitCode
	s.lastDuration = duration
	s.haveLastRun = true
}

// Running reports whether a schedule:run is currently in flight.
func (s *ScheduleState) Running() bool { return s.running }

// LastRun returns the last started run's start time, and — once it has
// completed — its duration and exit code. ok is false if no run has been
// started yet; completed is false while the first run is still in flight.
func (s *ScheduleState) LastRun() (start time.Time, duration time.Duration, exitCode int, completed, ok bool) {
	return s.lastStart, s.lastDuration, s.lastExitCode, s.haveLastRun && !s.running, !s.lastStart.IsZero()
}

// LastSkip returns when the most recent skip happened and why; ok is
// false if no minute has been skipped yet.
func (s *ScheduleState) LastSkip() (at time.Time, reason Reason, ok bool) {
	return s.lastSkipAt, s.lastSkipReason, !s.lastSkipAt.IsZero()
}
