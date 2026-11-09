package runner

import "testing"

func TestParseJobEvent_ValidLine(t *testing.T) {
	// attempts/duration are real fields Laravel's --json line carries but
	// this daemon doesn't parse (JobEvent's own doc comment) — included
	// here to confirm unrecognized fields are simply ignored, not rejected.
	line := []byte(`{"status":"success","job":"App\\Jobs\\ExampleJob","attempts":2,"duration":1.5}`)
	e, ok := parseJobEvent(line)
	if !ok {
		t.Fatal("parseJobEvent ok = false, want true for a valid --json line")
	}
	if e.Status != "success" || e.Job != `App\Jobs\ExampleJob` {
		t.Errorf("event = %+v, want {success App\\Jobs\\ExampleJob}", e)
	}
}

func TestParseJobEvent_PlainTextIsNotAMatch(t *testing.T) {
	if _, ok := parseJobEvent([]byte("   INFO  Processing jobs from the [default] queue.")); ok {
		t.Error("parseJobEvent ok = true for plain text, want false")
	}
}

func TestParseJobEvent_EmptyLineIsNotAMatch(t *testing.T) {
	if _, ok := parseJobEvent([]byte("")); ok {
		t.Error("parseJobEvent ok = true for an empty line, want false")
	}
}

func TestParseJobEvent_JSONWithoutStatusIsNotAMatch(t *testing.T) {
	// Valid JSON, but not our expected shape (e.g. some other tool's
	// unrelated JSON output) — status is the field we require.
	if _, ok := parseJobEvent([]byte(`{"foo":"bar"}`)); ok {
		t.Error("parseJobEvent ok = true for JSON missing status, want false")
	}
}
