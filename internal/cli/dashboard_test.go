package cli

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/DevDevil/laravel-queue-governor/internal/ipc"
)

func TestDashboardModel_Update_QuitsOnQKey(t *testing.T) {
	m := newDashboardModel(nil, nil, nil, "")
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if cmd == nil {
		t.Fatal("expected a Cmd (tea.Quit) for 'q', got nil")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Errorf("Cmd() = %T, want tea.QuitMsg", cmd())
	}
}

func TestDashboardModel_Update_WindowSizeMsgSetsDimensions(t *testing.T) {
	m := newDashboardModel(nil, nil, nil, "")
	newModel, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	dm := newModel.(dashboardModel)
	if dm.width != 100 || dm.height != 30 {
		t.Errorf("width/height = %d/%d, want 100/30", dm.width, dm.height)
	}
}

func TestDashboardModel_Apply_EachFrameTypeUpdatesState(t *testing.T) {
	m := newDashboardModel(nil, nil, nil, "")

	m.apply(dashboardFrame{Hello: &ipc.HelloFrame{Cores: 8}})
	if m.hello.Cores != 8 {
		t.Errorf("after Hello frame: cores=%d, want 8", m.hello.Cores)
	}

	m.apply(dashboardFrame{History: &ipc.HistoryFrame{Samples: []ipc.MetricSample{{Load: 1}, {Load: 2}}}})
	if len(m.loadHistory) != 2 {
		t.Errorf("loadHistory = %v, want 2 samples", m.loadHistory)
	}

	m.apply(dashboardFrame{Snapshot: &ipc.SnapshotFrame{Apps: []ipc.AppStatus{{Name: "wonen31"}}}})
	if len(m.appOrder) != 1 || m.appRows["wonen31"] == nil {
		t.Errorf("after Snapshot frame: appOrder=%v appRows=%v, want one row for wonen31", m.appOrder, m.appRows)
	}

	m.apply(dashboardFrame{Sample: &ipc.SampleFrame{}})
	m.apply(dashboardFrame{Sample: &ipc.SampleFrame{}})
	if len(m.loadHistory) != 4 {
		t.Errorf("loadHistory len = %d, want 4 (2 from history + 2 live samples)", len(m.loadHistory))
	}

	m.apply(dashboardFrame{Decision: &ipc.DecisionFrame{}})
	if len(m.logEntries) != 1 {
		t.Errorf("logEntries len = %d, want 1", len(m.logEntries))
	}
}

func TestDashboardModel_Update_FrameMsgReissuesWait(t *testing.T) {
	frames := make(chan dashboardFrame, 1)
	m := newDashboardModel(frames, nil, nil, "")

	newModel, cmd := m.Update(dashboardFrameMsg{Hello: &ipc.HelloFrame{Cores: 4}})
	dm := newModel.(dashboardModel)
	if dm.hello.Cores != 4 {
		t.Errorf("after dashboardFrameMsg: cores=%d, want 4", dm.hello.Cores)
	}
	if cmd == nil {
		t.Fatal("expected a follow-up Cmd to keep waiting for the next frame")
	}
}

func TestDashboardModel_Update_DisconnectShowsBannerButDoesNotQuit(t *testing.T) {
	// cmd is intentionally not invoked here: it's waitForDashboardFrame,
	// which blocks on the (empty, in this test) frames/disconnectCh
	// channels by design — the same reason TestDashboardModel_Update_
	// FrameMsgReissuesWait only checks cmd != nil rather than calling it.
	m := newDashboardModel(nil, nil, nil, "")
	newModel, cmd := m.Update(dashboardDisconnectMsg{err: errors.New("boom")})
	dm := newModel.(dashboardModel)
	if dm.connected {
		t.Error("expected connected=false after a disconnect message")
	}
	if dm.lastErr == nil || dm.lastErr.Error() != "boom" {
		t.Errorf("lastErr = %v, want boom", dm.lastErr)
	}
	if cmd == nil {
		t.Fatal("expected a follow-up Cmd to keep waiting (auto-reconnect keeps the program running, unlike the old quit-on-error behavior)")
	}
}

func TestDashboardModel_Update_HelloAfterDisconnectClearsBanner(t *testing.T) {
	m := newDashboardModel(nil, nil, nil, "")
	newModel, _ := m.Update(dashboardDisconnectMsg{err: errors.New("boom")})
	m = newModel.(dashboardModel)
	if m.connected {
		t.Fatal("expected connected=false after disconnect")
	}

	newModel, _ = m.Update(dashboardFrameMsg{Hello: &ipc.HelloFrame{Cores: 4}})
	m = newModel.(dashboardModel)
	if !m.connected {
		t.Error("expected a Hello frame to clear the disconnected state (reconnect succeeded)")
	}
}

func TestDashboardModel_View_EmptyBeforeFirstWindowSize(t *testing.T) {
	m := newDashboardModel(nil, nil, nil, "")
	if got := m.View(); got != "" {
		t.Errorf("View() before any WindowSizeMsg = %q, want empty (nothing to size panels against)", got)
	}
}

func TestDashboardModel_View_RendersAllFourPanelsAndFooter(t *testing.T) {
	m := newDashboardModel(nil, nil, nil, "")
	newModel, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	dm := newModel.(dashboardModel)

	out := stripANSI(dm.View())
	if !containsAll(out, "load", "iowait", "apps", "log", "quit") {
		t.Errorf("View() missing an expected panel/footer: %q", out)
	}
}

func TestDashboardModel_View_ReflowsGraphWidthOnResize(t *testing.T) {
	m := newDashboardModel(nil, nil, nil, "")

	narrow, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	wide, _ := m.Update(tea.WindowSizeMsg{Width: 160, Height: 24})

	narrowLines := strings.Split(stripANSI(narrow.(dashboardModel).View()), "\n")
	wideLines := strings.Split(stripANSI(wide.(dashboardModel).View()), "\n")

	if len(narrowLines[0]) >= len(wideLines[0]) {
		t.Errorf("graph row width didn't grow with terminal width: narrow=%d wide=%d",
			len(narrowLines[0]), len(wideLines[0]))
	}
}

func TestDashboardModel_Update_SortKeyCyclesMode(t *testing.T) {
	m := newDashboardModel(nil, nil, nil, "")
	if m.sortModeName() != "name" {
		t.Fatalf("initial sortModeName() = %q, want name", m.sortModeName())
	}

	seq := []string{"state", "load", "jobs", "name"}
	for _, want := range seq {
		newModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
		m = newModel.(dashboardModel)
		if m.sortModeName() != want {
			t.Errorf("sortModeName() = %q, want %q", m.sortModeName(), want)
		}
	}
}

func TestDashboardModel_Update_SortKeyAcceptsDigit2Too(t *testing.T) {
	m := newDashboardModel(nil, nil, nil, "")
	newModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("2")})
	if newModel.(dashboardModel).sortModeName() != "state" {
		t.Errorf("'2' should cycle sort just like 's'")
	}
}

func TestDashboardModel_SortedAppRows_OrdersByCurrentMode(t *testing.T) {
	m := newDashboardModel(nil, nil, nil, "")
	m.apply(dashboardFrame{Snapshot: &ipc.SnapshotFrame{Apps: []ipc.AppStatus{
		{Name: "b", State: "running", Load: 1, JobsSession: 5},
		{Name: "a", State: "throttled", Load: 9, JobsSession: 1},
	}}})

	byName := m.sortedAppRows()
	if byName[0].Name != "a" || byName[1].Name != "b" {
		t.Errorf("default (name) sort = %v, want [a b]", []string{byName[0].Name, byName[1].Name})
	}

	m.sortMode = dashSortState
	byState := m.sortedAppRows()
	if byState[0].Name != "b" { // running ranks before throttled
		t.Errorf("state sort = %v, want b (running) first", byState[0].Name)
	}

	m.sortMode = dashSortLoad
	byLoad := m.sortedAppRows()
	if byLoad[0].Name != "a" { // load 9 > load 1
		t.Errorf("load sort = %v, want a (load=9) first", byLoad[0].Name)
	}

	m.sortMode = dashSortJobs
	byJobs := m.sortedAppRows()
	if byJobs[0].Name != "b" { // jobs 5 > jobs 1
		t.Errorf("jobs sort = %v, want b (jobs=5) first", byJobs[0].Name)
	}
}

func TestDashboardModel_RunningCount_CountsOnlyRunningState(t *testing.T) {
	m := newDashboardModel(nil, nil, nil, "")
	m.apply(dashboardFrame{Snapshot: &ipc.SnapshotFrame{Apps: []ipc.AppStatus{
		{Name: "a", State: "running"},
		{Name: "b", State: "idle"},
		{Name: "c", State: "running"},
	}}})
	if got := m.runningCount(); got != 2 {
		t.Errorf("runningCount() = %d, want 2", got)
	}
}

func TestDashboardModel_View_RendersRealAppRow(t *testing.T) {
	m := newDashboardModel(nil, nil, nil, "")
	newModel, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = newModel.(dashboardModel)
	m.apply(dashboardFrame{Snapshot: &ipc.SnapshotFrame{Apps: []ipc.AppStatus{
		{Name: "saas_seed_spa", State: "running", Mode: "per_job"},
	}}})

	out := stripANSI(m.View())
	if !containsAll(out, "saas_seed_spa", "running", "per_job", "running 1/0") {
		t.Errorf("View() missing expected app row / running-count content: %q", out)
	}
}

func TestDashboardModel_Apply_HistoryFrameSeedsHistoryAndLastSample(t *testing.T) {
	m := newDashboardModel(nil, nil, nil, "")
	m.apply(dashboardFrame{History: &ipc.HistoryFrame{Samples: []ipc.MetricSample{
		{Load: 1, IOWait: 2}, {Load: 3, IOWait: 4},
	}}})

	if len(m.loadHistory) != 2 || m.loadHistory[1] != 3 {
		t.Errorf("loadHistory = %v, want [1 3]", m.loadHistory)
	}
	if len(m.iowaitHistory) != 2 || m.iowaitHistory[1] != 4 {
		t.Errorf("iowaitHistory = %v, want [2 4]", m.iowaitHistory)
	}
	if !m.haveSample || m.lastSample.Load != 3 {
		t.Errorf("haveSample=%v lastSample.Load=%v, want true/3 (last history sample)", m.haveSample, m.lastSample.Load)
	}
}

func TestDashboardModel_Apply_SampleFrameAppendsToHistory(t *testing.T) {
	m := newDashboardModel(nil, nil, nil, "")
	m.apply(dashboardFrame{Sample: &ipc.SampleFrame{MetricSample: ipc.MetricSample{Load: 5, IOWait: 6}}})

	if len(m.loadHistory) != 1 || m.loadHistory[0] != 5 {
		t.Errorf("loadHistory = %v, want [5]", m.loadHistory)
	}
	if !m.haveSample || m.lastSample.IOWait != 6 {
		t.Errorf("haveSample=%v lastSample.IOWait=%v, want true/6", m.haveSample, m.lastSample.IOWait)
	}
}

func TestDashboardModel_View_RendersGraphReadoutFromLiveSample(t *testing.T) {
	m := newDashboardModel(nil, nil, nil, "")
	newModel, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = newModel.(dashboardModel)
	m.apply(dashboardFrame{Hello: &ipc.HelloFrame{Cores: 4, MaxSystemLoad: 6.0, MaxIOWait: 25}})
	m.apply(dashboardFrame{Sample: &ipc.SampleFrame{MetricSample: ipc.MetricSample{Load: 2.0, IOWait: 12}}})

	out := stripANSI(m.View())
	if !containsAll(out, "2.00", "6.00", "0.50/core", "12%", "25%") {
		t.Errorf("View() missing expected graph readout content: %q", out)
	}
}

func TestDashboardModel_Update_ArrowAndVimKeysMoveSelection(t *testing.T) {
	m := newDashboardModel(nil, nil, nil, "")
	m.apply(dashboardFrame{Snapshot: &ipc.SnapshotFrame{Apps: []ipc.AppStatus{
		{Name: "a"}, {Name: "b"}, {Name: "c"},
	}}})

	newModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = newModel.(dashboardModel)
	if m.selectedIdx != 1 {
		t.Fatalf("selectedIdx after down = %d, want 1", m.selectedIdx)
	}

	newModel, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	m = newModel.(dashboardModel)
	if m.selectedIdx != 2 {
		t.Fatalf("selectedIdx after j = %d, want 2", m.selectedIdx)
	}

	// Clamped at the bottom — one more "down" past the last row must not
	// go out of bounds.
	newModel, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = newModel.(dashboardModel)
	if m.selectedIdx != 2 {
		t.Errorf("selectedIdx clamped-down = %d, want 2 (last index)", m.selectedIdx)
	}

	newModel, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = newModel.(dashboardModel)
	if m.selectedIdx != 1 {
		t.Fatalf("selectedIdx after up = %d, want 1", m.selectedIdx)
	}

	newModel, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
	m = newModel.(dashboardModel)
	newModel, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = newModel.(dashboardModel)
	if m.selectedIdx != 0 {
		t.Errorf("selectedIdx clamped-up = %d, want 0", m.selectedIdx)
	}
}

func TestDashboardModel_Update_FocusKeyToggles(t *testing.T) {
	m := newDashboardModel(nil, nil, nil, "")
	newModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f")})
	m = newModel.(dashboardModel)
	if !m.focused {
		t.Fatal("expected focused=true after 'f'")
	}

	newModel, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("3")})
	m = newModel.(dashboardModel)
	if m.focused {
		t.Error("expected '3' to toggle focused back to false")
	}

	newModel, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f")})
	m = newModel.(dashboardModel)
	newModel, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = newModel.(dashboardModel)
	if m.focused {
		t.Error("expected esc to exit focus mode")
	}
}

func TestDashboardModel_View_FocusedHidesOtherAppsFiltersLogAndSwitchesCeiling(t *testing.T) {
	m := newDashboardModel(nil, nil, nil, "")
	newModel, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = newModel.(dashboardModel)
	m.apply(dashboardFrame{Hello: &ipc.HelloFrame{Cores: 4, MaxSystemLoad: 6.0, MaxIOWait: 25}})
	m.apply(dashboardFrame{Snapshot: &ipc.SnapshotFrame{Apps: []ipc.AppStatus{
		{Name: "appone", State: "running", MaxLoadCeiling: 2.0, MaxIOWaitCeiling: 10},
		{Name: "apptwo", State: "idle", MaxLoadCeiling: 9.0, MaxIOWaitCeiling: 90},
	}}})
	m.apply(dashboardFrame{Sample: &ipc.SampleFrame{MetricSample: ipc.MetricSample{Load: 3.0, IOWait: 15}}})
	m.logEntries = append(m.logEntries, dashboardLogEntry{App: "appone", Verb: "dispatched", Detail: "job #1"})
	m.logEntries = append(m.logEntries, dashboardLogEntry{App: "apptwo", Verb: "dispatched", Detail: "job #2"})

	// selectedIdx=0 -> "appone" (sorted by name: appone, apptwo)
	m.focused = true

	out := stripANSI(m.View())
	if strings.Contains(out, "apptwo") {
		t.Errorf("focused view should hide apptwo entirely (apps table + log): %q", out)
	}
	if !strings.Contains(out, "appone") {
		t.Errorf("focused view should show appone: %q", out)
	}
	// appone's own ceiling (2.0) replaces the global one (6.0) for the
	// load graph's readout/threshold reference.
	if !strings.Contains(out, "2.00") {
		t.Errorf("focused view should show appone's own load ceiling (2.00), not the global one: %q", out)
	}
}

func TestDashboardModel_Update_PauseQueuesFramesAndResumeAppliesThemInOrder(t *testing.T) {
	m := newDashboardModel(nil, nil, nil, "")
	newModel, _ := m.Update(tea.KeyMsg{Type: tea.KeySpace})
	m = newModel.(dashboardModel)
	if !m.paused {
		t.Fatal("expected paused=true after space")
	}

	newModel, _ = m.Update(dashboardFrameMsg{Snapshot: &ipc.SnapshotFrame{Apps: []ipc.AppStatus{{Name: "wonen31"}}}})
	m = newModel.(dashboardModel)
	if len(m.appOrder) != 0 {
		t.Errorf("appOrder = %v, want untouched while paused (data buffered, not applied)", m.appOrder)
	}
	if len(m.pausedQueue) != 1 {
		t.Fatalf("pausedQueue len = %d, want 1", len(m.pausedQueue))
	}

	newModel, _ = m.Update(dashboardFrameMsg{Decision: &ipc.DecisionFrame{DecisionEvent: ipc.DecisionEvent{App: "wonen31", Decision: "dispatched"}}})
	m = newModel.(dashboardModel)
	if len(m.pausedQueue) != 2 {
		t.Fatalf("pausedQueue len = %d, want 2 after a second frame while still paused", len(m.pausedQueue))
	}

	// Resume ("p" this time, not space, to also confirm both keys work).
	newModel, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("p")})
	m = newModel.(dashboardModel)
	if m.paused {
		t.Fatal("expected paused=false after resuming")
	}
	if len(m.pausedQueue) != 0 {
		t.Errorf("pausedQueue = %v, want drained on resume", m.pausedQueue)
	}
	if len(m.appOrder) != 1 || m.appRows["wonen31"] == nil {
		t.Errorf("expected the queued snapshot to be applied on resume: appOrder=%v", m.appOrder)
	}
	if len(m.logEntries) != 1 {
		t.Errorf("expected the queued decision to also be applied on resume (one log entry), got %d", len(m.logEntries))
	}
}

func TestDashboardModel_Update_HelloWhilePausedIsNotQueued(t *testing.T) {
	// Connection status (unlike the data view) is not gated by pause —
	// see the comment in Update()'s dashboardFrameMsg case.
	m := newDashboardModel(nil, nil, nil, "")
	newModel, _ := m.Update(tea.KeyMsg{Type: tea.KeySpace})
	m = newModel.(dashboardModel)

	newModel, _ = m.Update(dashboardDisconnectMsg{err: errors.New("boom")})
	m = newModel.(dashboardModel)
	if m.connected {
		t.Fatal("expected connected=false after disconnect")
	}

	newModel, _ = m.Update(dashboardFrameMsg{Hello: &ipc.HelloFrame{Cores: 4}})
	m = newModel.(dashboardModel)
	if !m.connected {
		t.Error("expected Hello to clear the disconnected banner immediately, even while paused")
	}
	if m.hello.Cores != 0 {
		t.Error("expected Hello's own data effects (m.hello) to still be queued, not applied until resume")
	}
	if len(m.pausedQueue) != 1 {
		t.Errorf("pausedQueue len = %d, want 1 (the Hello frame, queued for its data effects)", len(m.pausedQueue))
	}
}

func TestDashboardModel_Update_HelpKeyOpensAndAnyKeyCloses(t *testing.T) {
	m := newDashboardModel(nil, nil, nil, "")
	newModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("1")})
	m = newModel.(dashboardModel)
	if !m.showHelp {
		t.Fatal("expected showHelp=true after '1'")
	}

	// Any key closes it — including one that would otherwise do
	// something else (here, 's' would normally cycle sort).
	newModel, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	m = newModel.(dashboardModel)
	if m.showHelp {
		t.Error("expected any key to close the help overlay")
	}
	if m.sortMode != dashSortName {
		t.Error("the key that closed help should not have also cycled sort")
	}
}

func TestDashboardModel_View_ShowsHelpOverlayContentWhenOpen(t *testing.T) {
	m := newDashboardModel(nil, nil, nil, "")
	newModel, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = newModel.(dashboardModel)
	m.showHelp = true

	out := stripANSI(m.View())
	if !containsAll(out, "select row", "cycle sort", "quit") {
		t.Errorf("View() with showHelp=true missing expected help content: %q", out)
	}
}

func TestFooterLine_DisconnectedShowsBannerInsteadOfKeybinds(t *testing.T) {
	m := newDashboardModel(nil, nil, nil, "")
	m.connected = false
	out := stripANSI(footerLine(m, nil, 0))
	if !strings.Contains(out, "disconnected") {
		t.Errorf("footerLine while disconnected = %q, want the disconnect banner", out)
	}
}

func TestDashboardModel_View_BelowMinSizeShowsOnlyTheTooSmallMessage(t *testing.T) {
	m := newDashboardModel(nil, nil, nil, "")
	m.apply(dashboardFrame{Snapshot: &ipc.SnapshotFrame{Apps: []ipc.AppStatus{{Name: "saas_seed_spa", State: "running"}}}})

	for _, size := range []tea.WindowSizeMsg{{Width: 79, Height: 24}, {Width: 80, Height: 23}, {Width: 40, Height: 12}} {
		newModel, _ := m.Update(size)
		dm := newModel.(dashboardModel)
		out := stripANSI(dm.View())
		if !strings.Contains(out, "terminal too small") {
			t.Errorf("size=%+v: View() = %q, want the too-small message", size, out)
		}
		if strings.Contains(out, "saas_seed_spa") {
			t.Errorf("size=%+v: below-minimum View() must show nothing else, got %q", size, out)
		}
		if got := len(strings.Split(dm.View(), "\n")); got != size.Height {
			t.Errorf("size=%+v: too-small message has %d lines, want exactly %d", size, got, size.Height)
		}
	}
}

func TestDashboardModel_View_AtExactMinSizeRendersNormally(t *testing.T) {
	m := newDashboardModel(nil, nil, nil, "")
	m.apply(dashboardFrame{Snapshot: &ipc.SnapshotFrame{Apps: []ipc.AppStatus{{Name: "saas_seed_spa", State: "running"}}}})

	newModel, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	out := stripANSI(newModel.(dashboardModel).View())
	if strings.Contains(out, "terminal too small") {
		t.Errorf("View() at exactly 80x24 = %q, want normal rendering (80x24 is the minimum, not below it)", out)
	}
	if !strings.Contains(out, "saas_seed_spa") {
		t.Errorf("View() at 80x24 missing the app row: %q", out)
	}
}

func TestDashboardModel_View_GrowingBackAboveMinSizeResumesNormalRendering(t *testing.T) {
	m := newDashboardModel(nil, nil, nil, "")
	m.apply(dashboardFrame{Snapshot: &ipc.SnapshotFrame{Apps: []ipc.AppStatus{{Name: "saas_seed_spa", State: "running"}}}})

	small, _ := m.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
	if out := stripANSI(small.(dashboardModel).View()); !strings.Contains(out, "terminal too small") {
		t.Fatalf("expected the too-small message at 40x12, got %q", out)
	}

	grown, _ := small.(dashboardModel).Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	out := stripANSI(grown.(dashboardModel).View())
	if strings.Contains(out, "terminal too small") {
		t.Errorf("expected normal rendering after growing back above the minimum, got %q", out)
	}
	if !strings.Contains(out, "saas_seed_spa") {
		t.Errorf("expected the app row back after resuming normal rendering: %q", out)
	}
}

func TestDashboardModel_View_ResizeWhilePausedKeepsFrozenData(t *testing.T) {
	m := newDashboardModel(nil, nil, nil, "")
	newModel, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = newModel.(dashboardModel)
	m.apply(dashboardFrame{Snapshot: &ipc.SnapshotFrame{Apps: []ipc.AppStatus{{Name: "appone", State: "running"}}}})

	newModel, _ = m.Update(tea.KeyMsg{Type: tea.KeySpace}) // pause
	m = newModel.(dashboardModel)

	// New data arrives while paused: must not change the visible state.
	newModel, _ = m.Update(dashboardFrameMsg{Snapshot: &ipc.SnapshotFrame{Apps: []ipc.AppStatus{{Name: "apptwo", State: "running"}}}})
	m = newModel.(dashboardModel)

	// Resize while still paused (TUI.md §14: "Paused + resize -> recompute
	// layout but keep the frozen data").
	newModel, _ = m.Update(tea.WindowSizeMsg{Width: 130, Height: 32})
	m = newModel.(dashboardModel)

	out := stripANSI(m.View())
	if strings.Contains(out, "apptwo") {
		t.Errorf("resize while paused must not apply queued data: %q", out)
	}
	if !strings.Contains(out, "appone") {
		t.Errorf("resize while paused must keep showing the frozen (pre-pause) data: %q", out)
	}
	if !m.paused {
		t.Error("resize must not itself clear paused")
	}
}

// TestDashboardModel_View_TotalLinesMatchTerminalHeight guards against the
// real overflow bug found while building the graph panels (see
// TestBuildGraphBox_DenseHistoryDoesNotOverflowOuterHeight): a taller-
// than-intended View() output scrolls the top line (the graphs row's
// title border) off a real terminal, since bubbletea doesn't reposition
// for content that overflows the window. Uses varied (non-flat, non-
// empty) history so the braille graphs actually exercise their dense
// per-character ANSI styling, which is what triggered the bug.
func TestDashboardModel_View_TotalLinesMatchTerminalHeight(t *testing.T) {
	m := newDashboardModel(nil, nil, nil, "")
	newModel, _ := m.Update(tea.WindowSizeMsg{Width: 130, Height: 32})
	m = newModel.(dashboardModel)
	m.apply(dashboardFrame{Hello: &ipc.HelloFrame{Cores: 4, MaxSystemLoad: 6.0, MaxIOWait: 25}})
	m.apply(dashboardFrame{Snapshot: &ipc.SnapshotFrame{Apps: []ipc.AppStatus{{Name: "saas_seed_spa", State: "running", Mode: "per_job"}}}})
	for i := 0; i < 300; i++ {
		m.apply(dashboardFrame{Sample: &ipc.SampleFrame{MetricSample: ipc.MetricSample{Load: float64(i%10) + 0.5, IOWait: float64(i % 25)}}})
	}

	out := m.View()
	if got := len(strings.Split(out, "\n")); got != 32 {
		t.Errorf("View() produced %d lines for a 32-row terminal, want exactly 32", got)
	}
}

// TestDashboardModel_View_TotalLinesMatchTerminalHeight_WithActiveRuns is
// the same invariant as the test above, but with an app that has multiple
// concurrent runs (ActiveRuns sub-item lines) — guards buildAppsBox's
// hand-computed outerHeight against drifting out of sync with what
// renderAppsTable actually emits now that rows are no longer 1:1 with
// lines.
func TestDashboardModel_View_TotalLinesMatchTerminalHeight_WithActiveRuns(t *testing.T) {
	m := newDashboardModel(nil, nil, nil, "")
	newModel, _ := m.Update(tea.WindowSizeMsg{Width: 130, Height: 32})
	m = newModel.(dashboardModel)
	m.apply(dashboardFrame{Hello: &ipc.HelloFrame{Cores: 4, MaxSystemLoad: 6.0, MaxIOWait: 25}})
	now := time.Now()
	m.apply(dashboardFrame{Snapshot: &ipc.SnapshotFrame{Apps: []ipc.AppStatus{
		{Name: "saas_seed_spa", State: "running", Mode: "per_job", ActiveRuns: []ipc.ActiveRun{
			{PID: 100, Start: now.Add(-12 * time.Second)},
			{PID: 200, Start: now.Add(-4 * time.Second)},
		}},
		{Name: "wonen31", State: "idle", Mode: "per_job"},
	}}})

	out := m.View()
	if got := len(strings.Split(out, "\n")); got != 32 {
		t.Errorf("View() produced %d lines for a 32-row terminal, want exactly 32", got)
	}
}

// TestDashboardModel_View_TotalLinesMatchTerminalHeight_NoApps is the
// regression test for a real off-by-one: buildAppsBox used to add an
// extra "+1 for symmetry" to outerHeight for the zero-apps case that
// didn't correspond to anything titledPanelWithLabel actually renders,
// making View() one line short of the terminal height whenever no apps
// were configured. Deliberately never applies a Snapshot frame, so
// m.appOrder stays empty (TUI.md §14: "no apps configured" placeholder).
func TestDashboardModel_View_TotalLinesMatchTerminalHeight_NoApps(t *testing.T) {
	m := newDashboardModel(nil, nil, nil, "")
	newModel, _ := m.Update(tea.WindowSizeMsg{Width: 130, Height: 32})
	m = newModel.(dashboardModel)
	m.apply(dashboardFrame{Hello: &ipc.HelloFrame{Cores: 4, MaxSystemLoad: 6.0, MaxIOWait: 25}})

	out := m.View()
	if got := len(strings.Split(out, "\n")); got != 32 {
		t.Errorf("View() produced %d lines for a 32-row terminal with zero apps configured, want exactly 32", got)
	}
}

func TestDashboardModel_Apply_DecisionFrameAppendsLogEntry(t *testing.T) {
	m := newDashboardModel(nil, nil, nil, "")
	m.apply(dashboardFrame{Snapshot: &ipc.SnapshotFrame{Apps: []ipc.AppStatus{{Name: "wonen31", Mode: "per_job"}}}})

	seq := 7
	m.apply(dashboardFrame{Decision: &ipc.DecisionFrame{DecisionEvent: ipc.DecisionEvent{
		App: "wonen31", Decision: "dispatched", JobSeq: &seq, Load: 1, IOWait: 2,
	}}})

	if len(m.logEntries) != 1 {
		t.Fatalf("len(logEntries) = %d, want 1", len(m.logEntries))
	}
	if m.logEntries[0].App != "wonen31" || m.logEntries[0].Verb != "dispatched" {
		t.Errorf("logEntries[0] = %+v, want wonen31/dispatched", m.logEntries[0])
	}
}

func TestDashboardModel_View_RendersLogEntry(t *testing.T) {
	m := newDashboardModel(nil, nil, nil, "")
	newModel, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = newModel.(dashboardModel)
	m.apply(dashboardFrame{Snapshot: &ipc.SnapshotFrame{Apps: []ipc.AppStatus{{Name: "saas_seed_spa", Mode: "per_job"}}}})
	m.apply(dashboardFrame{Decision: &ipc.DecisionFrame{DecisionEvent: ipc.DecisionEvent{
		App: "saas_seed_spa", Decision: "dispatched", Load: 1, IOWait: 2,
	}}})

	out := stripANSI(m.View())
	if !containsAll(out, "saas_seed_spa", "dispatched", "job #0") {
		t.Errorf("View() missing expected log line content: %q", out)
	}
}
