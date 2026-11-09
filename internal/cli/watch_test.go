package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/DevDevil/laravel-queue-governor/internal/ipc"
)

func TestWriteWatchLine_ValidJSONWhenNotColorized(t *testing.T) {
	e := decisionEventFixture(true)

	var buf bytes.Buffer
	if err := WriteWatchLine(&buf, e, false); err != nil {
		t.Fatal(err)
	}

	line := strings.TrimSuffix(buf.String(), "\n")
	var got map[string]any
	if err := json.Unmarshal([]byte(line), &got); err != nil {
		t.Fatalf("output is not valid JSON when colorize=false: %v\noutput: %q", err, line)
	}
	if strings.Contains(line, "\033[") {
		t.Errorf("output contains an ANSI escape when colorize=false: %q", line)
	}
}

func TestWriteWatchLine_ColorizedDispatchedIsGreen(t *testing.T) {
	e := decisionEventFixture(true)

	var buf bytes.Buffer
	if err := WriteWatchLine(&buf, e, true); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(buf.String(), ansiGreen) {
		t.Errorf("dispatched event should start with the green ANSI code, got %q", buf.String())
	}
	if !strings.Contains(buf.String(), ansiReset) {
		t.Error("colorized output should contain the reset code")
	}
}

func TestWriteWatchLine_ColorizedSkippedIsYellow(t *testing.T) {
	e := decisionEventFixture(false)

	var buf bytes.Buffer
	if err := WriteWatchLine(&buf, e, true); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(buf.String(), ansiYellow) {
		t.Errorf("skipped event should start with the yellow ANSI code, got %q", buf.String())
	}
}

func decisionEventFixture(dispatched bool) ipc.DecisionEvent {
	e := ipc.DecisionEvent{
		Timestamp: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC),
		App:       "wonen31",
		Load:      3.8,
		IOWait:    12,
		State:     "running",
	}
	if dispatched {
		seq := 1
		e.Decision = "dispatched"
		e.JobSeq = &seq
	} else {
		reason := "app_load_high"
		e.Decision = "skipped"
		e.Reason = &reason
		e.State = "throttled"
	}
	return e
}
