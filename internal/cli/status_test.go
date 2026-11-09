package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/DevDevil/laravel-queue-governor/internal/ipc"
)

func TestRenderStatusTable(t *testing.T) {
	ts := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	reason := "app_load_high"

	snap := ipc.StatusSnapshot{
		Apps: []ipc.AppStatus{
			{Name: "wonen31", State: "running", Load: 3.8, IOWait: 12, JobsSession: 5, LastDispatch: &ts},
			{Name: "squaremanager", State: "throttled", Load: 6.1, IOWait: 30, JobsSession: 0, LastDispatch: nil, Reason: &reason},
		},
	}

	var buf bytes.Buffer
	RenderStatusTable(&buf, snap)
	out := buf.String()

	for _, want := range []string{
		"NAME", "STATE", "LOAD", "IOWAIT", "JOBS", "LAST DISPATCH", "REASON",
		"wonen31", "running", "3.80", "12.0%", "5", "2026-01-01T12:00:00Z",
		"squaremanager", "throttled", "6.10", "30.0%", "never", "app_load_high",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n--- output ---\n%s", want, out)
		}
	}
}

func TestRenderStatusTable_Empty(t *testing.T) {
	var buf bytes.Buffer
	RenderStatusTable(&buf, ipc.StatusSnapshot{})
	if !strings.Contains(buf.String(), "NAME") {
		t.Error("expected header row even for an empty snapshot")
	}
}
