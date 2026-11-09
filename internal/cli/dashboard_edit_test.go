package cli

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/DevDevil/laravel-queue-governor/internal/ipc"
)

func keyRune(r rune) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}} }

func TestDashboardModel_OpenAppEdit_NoAppsIsNoop(t *testing.T) {
	m := newDashboardModel(nil, nil, nil, "")
	newModel, cmd := m.Update(keyRune('e'))
	dm := newModel.(dashboardModel)
	if dm.edit.mode != editClosed {
		t.Errorf("edit.mode = %v, want editClosed when no apps are configured", dm.edit.mode)
	}
	if cmd != nil {
		t.Error("expected a nil Cmd for a no-op 'e' with no apps")
	}
}

func TestDashboardModel_OpenAppEdit_OpensFieldListForSelectedApp(t *testing.T) {
	m := newDashboardModel(nil, nil, nil, "")
	m.apply(dashboardFrame{Snapshot: &ipc.SnapshotFrame{Apps: []ipc.AppStatus{{Name: "app-a"}, {Name: "app-b"}}}})
	m.selectedIdx = 1 // sortedAppRows defaults to name order: app-a, app-b

	newModel, _ := m.Update(keyRune('e'))
	dm := newModel.(dashboardModel)

	if dm.edit.mode != editFieldList {
		t.Fatalf("edit.mode = %v, want editFieldList", dm.edit.mode)
	}
	if dm.edit.scope != "app" || dm.edit.appName != "app-b" {
		t.Errorf("edit scope/appName = %q/%q, want app/app-b", dm.edit.scope, dm.edit.appName)
	}
	if len(dm.edit.fields) != len(ipc.AppTunableFields) {
		t.Errorf("edit.fields = %v, want ipc.AppTunableFields", dm.edit.fields)
	}
}

func TestDashboardModel_OpenGlobalEdit(t *testing.T) {
	m := newDashboardModel(nil, nil, nil, "")
	newModel, _ := m.Update(keyRune('g'))
	dm := newModel.(dashboardModel)

	if dm.edit.mode != editFieldList || dm.edit.scope != "global" {
		t.Fatalf("edit state = %+v, want an open global field list", dm.edit)
	}
	if len(dm.edit.fields) != len(ipc.GlobalTunableFields) {
		t.Errorf("edit.fields = %v, want ipc.GlobalTunableFields", dm.edit.fields)
	}
}

func TestDashboardModel_UpdateEdit_FieldListNavigationStaysInBounds(t *testing.T) {
	m := newDashboardModel(nil, nil, nil, "")
	m, _ = asModel(m.Update(keyRune('g')))

	// Up at the top is a no-op.
	m, _ = asModel(m.Update(tea.KeyMsg{Type: tea.KeyUp}))
	if m.edit.selected != 0 {
		t.Errorf("selected = %d, want 0 (clamped)", m.edit.selected)
	}

	for range ipc.GlobalTunableFields {
		m, _ = asModel(m.Update(tea.KeyMsg{Type: tea.KeyDown}))
	}
	want := len(ipc.GlobalTunableFields) - 1
	if m.edit.selected != want {
		t.Errorf("selected = %d, want %d (clamped at last field)", m.edit.selected, want)
	}
}

// asModel adapts Update's (tea.Model, tea.Cmd) return to (dashboardModel,
// tea.Cmd) so tests can chain Update calls without a type assertion at
// every call site.
func asModel(tm tea.Model, cmd tea.Cmd) (dashboardModel, tea.Cmd) {
	return tm.(dashboardModel), cmd
}

func TestDashboardModel_UpdateEdit_EnterPrefillsValueInputWithCurrentValue(t *testing.T) {
	m := newDashboardModel(nil, nil, nil, "")
	m.hello = ipc.HelloFrame{MaxSystemLoad: 6.5}
	m, _ = asModel(m.Update(keyRune('g')))

	for i, f := range ipc.GlobalTunableFields {
		if f == ipc.FieldMaxSystemLoad {
			m.edit.selected = i
		}
	}

	m, _ = asModel(m.Update(tea.KeyMsg{Type: tea.KeyEnter}))
	if m.edit.mode != editValueInput {
		t.Fatalf("edit.mode = %v, want editValueInput", m.edit.mode)
	}
	if m.edit.input != "6.5" {
		t.Errorf("edit.input = %q, want prefilled \"6.5\"", m.edit.input)
	}
}

func TestDashboardModel_UpdateEdit_ValueInputAcceptsDigitsMinusDotAndBackspace(t *testing.T) {
	m := newDashboardModel(nil, nil, nil, "")
	m, _ = asModel(m.Update(keyRune('g')))
	m, _ = asModel(m.Update(tea.KeyMsg{Type: tea.KeyEnter})) // -> editValueInput, input starts as current value ("0")
	m.edit.input = ""

	for _, r := range "-4.5" {
		m, _ = asModel(m.Update(keyRune(r)))
	}
	if m.edit.input != "-4.5" {
		t.Errorf("edit.input = %q, want \"-4.5\"", m.edit.input)
	}

	// A second '-' or '.' is rejected; a letter is rejected.
	m, _ = asModel(m.Update(keyRune('-')))
	m, _ = asModel(m.Update(keyRune('.')))
	m, _ = asModel(m.Update(keyRune('x')))
	if m.edit.input != "-4.5" {
		t.Errorf("edit.input = %q, want unchanged \"-4.5\" after rejected chars", m.edit.input)
	}

	m, _ = asModel(m.Update(tea.KeyMsg{Type: tea.KeyBackspace}))
	if m.edit.input != "-4." {
		t.Errorf("edit.input = %q, want \"-4.\" after backspace", m.edit.input)
	}
}

func TestDashboardModel_UpdateEdit_EscFromValueInputReturnsToFieldList(t *testing.T) {
	m := newDashboardModel(nil, nil, nil, "")
	m, _ = asModel(m.Update(keyRune('g')))
	m, _ = asModel(m.Update(tea.KeyMsg{Type: tea.KeyEnter}))
	m.edit.input = "123"

	m, _ = asModel(m.Update(tea.KeyMsg{Type: tea.KeyEsc}))
	if m.edit.mode != editFieldList {
		t.Errorf("edit.mode = %v, want editFieldList after esc", m.edit.mode)
	}
	if m.edit.input != "" {
		t.Errorf("edit.input = %q, want cleared after esc", m.edit.input)
	}
}

// TestDashboardModel_UpdateEdit_EscFromValueInputClearsStaleWarnings is the
// regression test for a real bug: a warning from a previous field's submit
// stayed displayed after backing out of value-input with esc, making it
// look like it applied to whichever field the user is now looking at.
func TestDashboardModel_UpdateEdit_EscFromValueInputClearsStaleWarnings(t *testing.T) {
	m := newDashboardModel(nil, nil, nil, "")
	m, _ = asModel(m.Update(keyRune('g')))
	m, _ = asModel(m.Update(tea.KeyMsg{Type: tea.KeyEnter}))
	m.edit.warnings = []string{"exceeds global.max_system_load"}

	m, _ = asModel(m.Update(tea.KeyMsg{Type: tea.KeyEsc}))
	if len(m.edit.warnings) != 0 {
		t.Errorf("edit.warnings = %v, want cleared after esc back to the field list", m.edit.warnings)
	}
}

// TestDashboardModel_UpdateEdit_EnterOnNewFieldClearsStaleWarnings is the
// same regression as above, for the other place warnings could linger:
// selecting a different field and pressing enter to edit it.
func TestDashboardModel_UpdateEdit_EnterOnNewFieldClearsStaleWarnings(t *testing.T) {
	m := newDashboardModel(nil, nil, nil, "")
	m, _ = asModel(m.Update(keyRune('g')))
	m.edit.warnings = []string{"exceeds global.max_system_load"}

	m, _ = asModel(m.Update(tea.KeyMsg{Type: tea.KeyEnter}))
	if len(m.edit.warnings) != 0 {
		t.Errorf("edit.warnings = %v, want cleared after entering value-input on a (possibly different) field", m.edit.warnings)
	}
}

func TestDashboardModel_UpdateEdit_EscFromFieldListClosesOverlay(t *testing.T) {
	m := newDashboardModel(nil, nil, nil, "")
	m, _ = asModel(m.Update(keyRune('g')))

	m, _ = asModel(m.Update(tea.KeyMsg{Type: tea.KeyEsc}))
	if m.edit.mode != editClosed {
		t.Errorf("edit.mode = %v, want editClosed after esc from the field list", m.edit.mode)
	}
}

func TestDashboardModel_UpdateEdit_EnterSubmitsAndSetsApplying(t *testing.T) {
	m := newDashboardModel(nil, nil, nil, "unused.sock")
	m, _ = asModel(m.Update(keyRune('g')))
	m, _ = asModel(m.Update(tea.KeyMsg{Type: tea.KeyEnter}))
	m.edit.input = "9.5"

	m, cmd := asModel(m.Update(tea.KeyMsg{Type: tea.KeyEnter}))
	if !m.edit.applying {
		t.Error("edit.applying = false, want true immediately after submit")
	}
	if cmd == nil {
		t.Fatal("expected a non-nil Cmd (the async SendSet call)")
	}
}

func TestDashboardModel_UpdateEdit_IgnoresKeysWhileApplying(t *testing.T) {
	m := newDashboardModel(nil, nil, nil, "")
	m, _ = asModel(m.Update(keyRune('g')))
	m, _ = asModel(m.Update(tea.KeyMsg{Type: tea.KeyEnter}))
	m.edit.applying = true
	m.edit.input = "9.5"

	m, cmd := asModel(m.Update(keyRune('1')))
	if m.edit.input != "9.5" {
		t.Errorf("edit.input = %q, want unchanged while applying", m.edit.input)
	}
	if cmd != nil {
		t.Error("expected a nil Cmd while a request is already in flight")
	}
}

func TestDashboardModel_ApplySetResult_SuccessReturnsToFieldList(t *testing.T) {
	m := newDashboardModel(nil, nil, nil, "")
	m, _ = asModel(m.Update(keyRune('g')))
	m, _ = asModel(m.Update(tea.KeyMsg{Type: tea.KeyEnter}))
	m.edit.applying = true

	got := m.applySetResult(dashboardSetResultMsg{resp: ipc.SetResponse{OK: true, Warnings: []string{"exceeds ceiling"}}})
	if got.edit.applying {
		t.Error("edit.applying = true, want false after a result arrives")
	}
	if got.edit.mode != editFieldList {
		t.Errorf("edit.mode = %v, want editFieldList after a successful submit", got.edit.mode)
	}
	if got.edit.err != "" {
		t.Errorf("edit.err = %q, want empty on success", got.edit.err)
	}
	if len(got.edit.warnings) != 1 {
		t.Errorf("edit.warnings = %v, want the 1 non-fatal warning preserved", got.edit.warnings)
	}
}

func TestDashboardModel_ApplySetResult_ErrorKeepsOverlayOpenWithMessage(t *testing.T) {
	m := newDashboardModel(nil, nil, nil, "")
	m, _ = asModel(m.Update(keyRune('g')))
	m, _ = asModel(m.Update(tea.KeyMsg{Type: tea.KeyEnter}))
	m.edit.applying = true

	got := m.applySetResult(dashboardSetResultMsg{resp: ipc.SetResponse{OK: false, Error: "max_system_load must be > 0"}})
	if got.edit.mode != editValueInput {
		t.Errorf("edit.mode = %v, want editValueInput to stay open so the user can correct the value", got.edit.mode)
	}
	if got.edit.err != "max_system_load must be > 0" {
		t.Errorf("edit.err = %q, want the server's error message", got.edit.err)
	}
}

func TestDashboardModel_EditMode_InterceptsQuitKey(t *testing.T) {
	m := newDashboardModel(nil, nil, nil, "")
	m, _ = asModel(m.Update(keyRune('g')))

	_, cmd := m.Update(keyRune('q'))
	if cmd != nil {
		if _, ok := cmd().(tea.QuitMsg); ok {
			t.Error("'q' should not quit while the edit overlay is open")
		}
	}
}
