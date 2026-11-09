package ipc

import (
	"encoding/json"
	"testing"
	"time"
)

// Locks in the PLAN.md §8 wire shape: reason/job_seq serialize as
// explicit null, not omitted, when inapplicable.
func TestDecisionEvent_JSONShape_Dispatched(t *testing.T) {
	seq := 42
	e := DecisionEvent{
		Timestamp: time.Date(2026, 7, 31, 14, 22, 5, 0, time.UTC),
		App:       "wonen31",
		Decision:  "dispatched",
		Reason:    nil,
		JobSeq:    &seq,
		Load:      3.8,
		IOWait:    12,
		State:     "running",
	}

	data, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}

	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}

	if got["reason"] != nil {
		t.Errorf("reason = %v, want explicit null", got["reason"])
	}
	if _, present := got["reason"]; !present {
		t.Error(`"reason" key missing from JSON, want present with null value`)
	}
	if got["job_seq"] != float64(42) {
		t.Errorf("job_seq = %v, want 42", got["job_seq"])
	}
	if got["ts"] != "2026-07-31T14:22:05Z" {
		t.Errorf(`ts = %v, want "2026-07-31T14:22:05Z"`, got["ts"])
	}
}

func TestDecisionEvent_JSONShape_Skipped(t *testing.T) {
	reason := "app_load_high"
	e := DecisionEvent{
		Timestamp: time.Now(),
		App:       "squaremanager",
		Decision:  "skipped",
		Reason:    &reason,
		JobSeq:    nil,
		State:     "throttled",
	}

	data, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}

	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}

	if got["reason"] != "app_load_high" {
		t.Errorf("reason = %v, want app_load_high", got["reason"])
	}
	if got["job_seq"] != nil {
		t.Errorf("job_seq = %v, want explicit null", got["job_seq"])
	}
	if _, present := got["job_seq"]; !present {
		t.Error(`"job_seq" key missing from JSON, want present with null value`)
	}
}
