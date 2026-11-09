package appstate

import (
	"testing"
	"time"
)

var base = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func TestMachine_InitialStateIsIdleAndEligible(t *testing.T) {
	m := New(1, 2*time.Second, 30*time.Second, false)

	r := m.Evaluate(base, false, false)
	if r.State != Idle || !r.Eligible || r.Reason != ReasonNone {
		t.Fatalf("Evaluate = %+v, want {Idle true \"\"}", r)
	}
}

func TestMachine_AlreadyRunning_SingleFlight(t *testing.T) {
	m := New(1, 2*time.Second, 30*time.Second, false)
	m.OnDispatch(base)

	r := m.Evaluate(base, false, false)
	if r.State != Running || r.Eligible || r.Reason != ReasonAlreadyRunning {
		t.Fatalf("Evaluate = %+v, want {Running false already_running}", r)
	}
}

func TestMachine_AppConcurrency_MultiSlot(t *testing.T) {
	m := New(2, 2*time.Second, 30*time.Second, false)
	m.OnDispatch(base)
	m.OnDispatch(base)

	r := m.Evaluate(base, false, false)
	if r.State != Running || r.Eligible || r.Reason != ReasonAppConcurrency {
		t.Fatalf("Evaluate = %+v, want {Running false app_concurrency}", r)
	}
}

func TestMachine_MultiSlot_FreeSlotStillEligible(t *testing.T) {
	m := New(2, 2*time.Second, 30*time.Second, false)
	m.OnDispatch(base) // 1 of 2 slots used

	r := m.Evaluate(base, false, false)
	if r.State != Running || !r.Eligible {
		t.Fatalf("Evaluate = %+v, want {Running true} (one slot still free)", r)
	}
}

func TestMachine_UnconfirmedWork_BlocksAdditionalDispatchWhenJSONOutputEnabled(t *testing.T) {
	m := New(2, 0, 0, true)
	m.OnDispatch(base)
	m.RecordRunStarted(100, base) // no UpdateActiveRunJob yet — status still ""

	r := m.Evaluate(base, false, false)
	if r.State != Running || r.Eligible || r.Reason != ReasonUnconfirmedWork {
		t.Fatalf("Evaluate = %+v, want {Running false unconfirmed_work}", r)
	}
}

func TestMachine_UnconfirmedWork_AllowsAdditionalDispatchOnceConfirmed(t *testing.T) {
	m := New(2, 0, 0, true)
	m.OnDispatch(base)
	m.RecordRunStarted(100, base)
	m.UpdateActiveRunJob(100, `App\Jobs\ExampleJob`, "starting")

	r := m.Evaluate(base, false, false)
	if r.State != Running || !r.Eligible {
		t.Fatalf("Evaluate = %+v, want {Running true} (confirmed work in progress)", r)
	}
}

func TestMachine_UnconfirmedWork_NotAppliedWithoutJSONOutput(t *testing.T) {
	m := New(2, 0, 0, false) // json_output disabled — old behavior preserved
	m.OnDispatch(base)
	m.RecordRunStarted(100, base)

	r := m.Evaluate(base, false, false)
	if r.State != Running || !r.Eligible {
		t.Fatalf("Evaluate = %+v, want {Running true} (no json_output, no confirmation signal to gate on)", r)
	}
}

func TestMachine_UnconfirmedWork_NeverBlocksFirstDispatch(t *testing.T) {
	m := New(2, 0, 0, true)
	// runningCount == 0: nothing dispatched yet.

	r := m.Evaluate(base, false, false)
	if !r.Eligible {
		t.Fatalf("Evaluate = %+v, want eligible (the very first dispatch is never gated on confirmation)", r)
	}
}

func TestMachine_UnconfirmedWork_TerminalStatusDoesNotCountAsConfirmed(t *testing.T) {
	m := New(2, 0, 0, true)
	m.OnDispatch(base)
	m.RecordRunStarted(100, base)
	m.UpdateActiveRunJob(100, `App\Jobs\ExampleJob`, "success") // job finished, not "starting"

	r := m.Evaluate(base, false, false)
	if r.Eligible || r.Reason != ReasonUnconfirmedWork {
		t.Fatalf("Evaluate = %+v, want unconfirmed_work (a terminal status alone doesn't count as confirmation)", r)
	}
}

func TestMachine_UnconfirmedWork_YieldsToAppConcurrencyWhenFull(t *testing.T) {
	m := New(1, 0, 0, true) // max_concurrent: 1 — no room for a 2nd slot at all
	m.OnDispatch(base)
	m.RecordRunStarted(100, base)

	r := m.Evaluate(base, false, false)
	if r.Eligible || r.Reason != ReasonAlreadyRunning {
		t.Fatalf("Evaluate = %+v, want already_running (capacity check wins outright, unconfirmed_work never reached)", r)
	}
}

func TestMachine_MinIntervalAfterRealWork(t *testing.T) {
	m := New(1, 2*time.Second, 30*time.Second, false)
	m.OnDispatch(base)
	m.OnRunComplete(base, true /* didWork */, false, true)

	r := m.Evaluate(base.Add(1*time.Second), false, false)
	if r.State != Cooldown || r.Eligible || r.Reason != ReasonMinInterval {
		t.Fatalf("Evaluate = %+v, want {Cooldown false min_interval}", r)
	}
}

func TestMachine_QueueIdleAfterEmptyRun(t *testing.T) {
	m := New(1, 2*time.Second, 30*time.Second, false)
	m.OnDispatch(base)
	m.OnRunComplete(base, false /* didWork */, false, true)

	r := m.Evaluate(base.Add(1*time.Second), false, false)
	if r.State != Cooldown || r.Eligible || r.Reason != ReasonQueueIdle {
		t.Fatalf("Evaluate = %+v, want {Cooldown false queue_idle}", r)
	}
}

func TestMachine_EligibleAfterMinIntervalElapses_NoSpike(t *testing.T) {
	m := New(1, 2*time.Second, 30*time.Second, false)
	m.OnDispatch(base)
	m.OnRunComplete(base, true, false /* no spike */, true)

	r := m.Evaluate(base.Add(2*time.Second), false, false)
	if !r.Eligible || r.State != Idle {
		t.Fatalf("Evaluate = %+v, want eligible/idle once min_interval has elapsed", r)
	}
}

func TestMachine_SpikeCooldownExtendsAfterMinInterval(t *testing.T) {
	m := New(1, 2*time.Second, 30*time.Second, false)
	m.OnDispatch(base)
	m.OnRunComplete(base, true, true /* spike observed */, true)

	// min_interval (2s) has elapsed, but spike_cooldown (30s more) hasn't.
	r := m.Evaluate(base.Add(3*time.Second), false, false)
	if r.State != Cooldown || r.Eligible || r.Reason != ReasonSpikeCooldown {
		t.Fatalf("Evaluate = %+v, want {Cooldown false spike_cooldown}", r)
	}
}

func TestMachine_EligibleAfterSpikeCooldownElapses(t *testing.T) {
	m := New(1, 2*time.Second, 30*time.Second, false)
	m.OnDispatch(base)
	m.OnRunComplete(base, true, true, true)

	r := m.Evaluate(base.Add(2*time.Second+30*time.Second), false, false)
	if !r.Eligible || r.State != Idle {
		t.Fatalf("Evaluate = %+v, want eligible/idle once spike_cooldown has elapsed", r)
	}
}

func TestMachine_OwnLoadHigh(t *testing.T) {
	m := New(1, 0, 0, false)

	r := m.Evaluate(base, true, false)
	if r.State != Throttled || r.Eligible || r.Reason != ReasonAppLoadHigh {
		t.Fatalf("Evaluate = %+v, want {Throttled false app_load_high}", r)
	}
}

func TestMachine_OwnIOWaitHigh(t *testing.T) {
	m := New(1, 0, 0, false)

	r := m.Evaluate(base, false, true)
	if r.State != Throttled || r.Eligible || r.Reason != ReasonAppIOWaitHigh {
		t.Fatalf("Evaluate = %+v, want {Throttled false app_iowait_high}", r)
	}
}

func TestMachine_LoadPrecedesIOWait(t *testing.T) {
	m := New(1, 0, 0, false)

	r := m.Evaluate(base, true, true)
	if r.Reason != ReasonAppLoadHigh {
		t.Fatalf("Evaluate.Reason = %v, want app_load_high (load checked before iowait)", r.Reason)
	}
}

func TestMachine_ZeroMinIntervalNoSpike_ImmediatelyEligible(t *testing.T) {
	m := New(1, 0, 0, false)
	m.OnDispatch(base)
	m.OnRunComplete(base, true, false, true)

	r := m.Evaluate(base, false, false)
	if !r.Eligible {
		t.Fatalf("Evaluate = %+v, want eligible immediately (min_interval=0, no spike)", r)
	}
}

func TestMachine_UpdateTunablesPreservesRunningStateButChangesTiming(t *testing.T) {
	m := New(1, 10*time.Second, 30*time.Second, false)
	m.OnDispatch(base)
	if m.RunningCount() != 1 {
		t.Fatalf("RunningCount = %d, want 1", m.RunningCount())
	}

	// A config reload (PLAN.md §14) shortens min_interval to 1s — must
	// not reset RunningCount, and the new duration must take effect.
	m.UpdateTunables(1, time.Second, 30*time.Second, false)
	if m.RunningCount() != 1 {
		t.Errorf("RunningCount after UpdateTunables = %d, want 1 (preserved)", m.RunningCount())
	}

	m.OnRunComplete(base, true, false, true)
	r := m.Evaluate(base.Add(2*time.Second), false, false)
	if !r.Eligible {
		t.Errorf("Evaluate = %+v, want eligible after the new (shorter) min_interval has elapsed", r)
	}
}

func TestMachine_UpdateTunablesClampsMaxConcurrentBelowOne(t *testing.T) {
	m := New(3, 0, 0, false)
	m.UpdateTunables(0, 0, 0, false)
	m.OnDispatch(base)

	r := m.Evaluate(base, false, false)
	if r.Eligible || r.Reason != ReasonAlreadyRunning {
		t.Errorf("Evaluate = %+v, want already_running (maxConcurrent clamped to 1)", r)
	}
}

func TestMachine_ActiveRuns_EmptyInitially(t *testing.T) {
	m := New(2, 0, 0, false)
	if got := m.ActiveRuns(); len(got) != 0 {
		t.Fatalf("ActiveRuns = %v, want empty", got)
	}
}

func TestMachine_ActiveRuns_DispatchAloneAddsNoEntry(t *testing.T) {
	// OnDispatch alone (before the runner reports a PID) must not create
	// an ActiveRuns entry yet — RecordRunStarted is what does that, since
	// the PID isn't known at dispatch time.
	m := New(2, 0, 0, false)
	m.OnDispatch(base)

	if got := m.ActiveRuns(); len(got) != 0 {
		t.Fatalf("ActiveRuns after OnDispatch alone = %v, want empty until RecordRunStarted", got)
	}
}

func TestMachine_ActiveRuns_RecordRunStartedAddsPIDAndStartTime(t *testing.T) {
	m := New(2, 0, 0, false)
	m.OnDispatch(base)
	m.RecordRunStarted(1234, base)

	got := m.ActiveRuns()
	want := []ActiveRun{{PID: 1234, Start: base}}
	if len(got) != 1 || got[0].PID != want[0].PID || !got[0].Start.Equal(want[0].Start) {
		t.Fatalf("ActiveRuns = %v, want %v", got, want)
	}
}

func TestMachine_ActiveRuns_MultipleRunsRecordInOrder(t *testing.T) {
	m := New(2, 0, 0, false)
	t1 := base
	t2 := base.Add(5 * time.Second)
	m.OnDispatch(t1)
	m.RecordRunStarted(100, t1)
	m.OnDispatch(t2)
	m.RecordRunStarted(200, t2)

	got := m.ActiveRuns()
	if len(got) != 2 || got[0].PID != 100 || !got[0].Start.Equal(t1) || got[1].PID != 200 || !got[1].Start.Equal(t2) {
		t.Fatalf("ActiveRuns = %v, want [{100 %v} {200 %v}]", got, t1, t2)
	}
}

func TestMachine_ActiveRuns_OnRunCompletePopsOldestFIFO(t *testing.T) {
	m := New(2, 0, 0, false)
	t1 := base
	t2 := base.Add(5 * time.Second)
	m.OnDispatch(t1)
	m.RecordRunStarted(100, t1)
	m.OnDispatch(t2)
	m.RecordRunStarted(200, t2)

	m.OnRunComplete(base.Add(10*time.Second), true, false, true)

	got := m.ActiveRuns()
	if len(got) != 1 || got[0].PID != 200 || !got[0].Start.Equal(t2) {
		t.Fatalf("ActiveRuns after one completion = %v, want [{200 %v}] (oldest popped)", got, t2)
	}
}

// TestMachine_OnRunComplete_UnstartedRunDoesNotPopGenuinelyRunningSibling
// is the regression test for a real bug: a run that never actually
// started (exec failed before RecordRunStarted was ever called for it —
// started=false) must not pop a sibling's real ActiveRuns entry when it
// "completes." Before the fix, OnRunComplete popped activeRuns[0]
// unconditionally, so this exact sequence discarded the genuinely-running
// sibling's PID from ActiveRuns().
func TestMachine_OnRunComplete_UnstartedRunDoesNotPopGenuinelyRunningSibling(t *testing.T) {
	m := New(2, 0, 0, false)
	t1 := base
	m.OnDispatch(t1)
	m.RecordRunStarted(100, t1) // run A actually started

	t2 := base.Add(5 * time.Second)
	m.OnDispatch(t2) // run B dispatched, but its exec fails — RecordRunStarted never called for it

	m.OnRunComplete(base.Add(6*time.Second), false, false, false) // run B "completes" with started=false

	got := m.ActiveRuns()
	if len(got) != 1 || got[0].PID != 100 {
		t.Fatalf("ActiveRuns after the unstarted run's completion = %v, want run A (pid 100) still present", got)
	}
	if m.RunningCount() != 1 {
		t.Errorf("RunningCount = %d, want 1 (run A still in flight)", m.RunningCount())
	}
}

func TestMachine_UpdateActiveRunJob_UpdatesMatchingPID(t *testing.T) {
	m := New(2, 0, 0, false)
	t1 := base
	t2 := base.Add(5 * time.Second)
	m.OnDispatch(t1)
	m.RecordRunStarted(100, t1)
	m.OnDispatch(t2)
	m.RecordRunStarted(200, t2)

	m.UpdateActiveRunJob(200, `App\Jobs\ExampleJob`, "starting")

	got := m.ActiveRuns()
	if got[0].Job != "" || got[0].Status != "" {
		t.Errorf("ActiveRuns[0] (pid 100) = %+v, want untouched (empty Job/Status)", got[0])
	}
	if got[1].Job != `App\Jobs\ExampleJob` || got[1].Status != "starting" {
		t.Errorf("ActiveRuns[1] (pid 200) = %+v, want job/status set", got[1])
	}
}

func TestMachine_UpdateActiveRunJob_LaterEventOverwritesEarlier(t *testing.T) {
	// Simulates a batch-mode run's sequence: starting, then success.
	m := New(1, 0, 0, false)
	m.OnDispatch(base)
	m.RecordRunStarted(100, base)

	m.UpdateActiveRunJob(100, `App\Jobs\ExampleJob`, "starting")
	m.UpdateActiveRunJob(100, `App\Jobs\ExampleJob`, "success")

	got := m.ActiveRuns()
	if got[0].Status != "success" {
		t.Errorf("ActiveRuns[0].Status = %q, want %q (latest event wins)", got[0].Status, "success")
	}
}

func TestMachine_UpdateActiveRunJob_UnknownPIDIsNoop(t *testing.T) {
	m := New(1, 0, 0, false)
	m.OnDispatch(base)
	m.RecordRunStarted(100, base)

	m.UpdateActiveRunJob(9999, "SomeJob", "starting")

	got := m.ActiveRuns()
	if len(got) != 1 || got[0].Job != "" || got[0].Status != "" {
		t.Errorf("ActiveRuns = %+v, want the existing entry untouched (unknown pid must not panic or mutate it)", got)
	}
}

func TestMachine_ActiveRuns_OnRunCompleteOnEmptyIsNoop(t *testing.T) {
	m := New(1, 0, 0, false)
	m.OnRunComplete(base, true, false, true)

	if got := m.ActiveRuns(); len(got) != 0 {
		t.Fatalf("ActiveRuns = %v, want empty (OnRunComplete on no active runs must not panic or underflow)", got)
	}
}

func TestMachine_ActiveRuns_ReturnsDefensiveCopy(t *testing.T) {
	m := New(1, 0, 0, false)
	m.OnDispatch(base)
	m.RecordRunStarted(1234, base)

	got := m.ActiveRuns()
	got[0] = ActiveRun{PID: 9999, Start: base.Add(time.Hour)}

	again := m.ActiveRuns()
	if again[0].PID != 1234 || !again[0].Start.Equal(base) {
		t.Fatalf("mutating ActiveRuns() result affected internal state: got %v, want {1234 %v}", again[0], base)
	}
}
