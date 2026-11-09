package cli

import (
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/DevDevil/laravel-queue-governor/internal/ipc"
)

// dashboardEditMode is the edit overlay's two-step flow: pick a field,
// then type its new value.
type dashboardEditMode int

const (
	editClosed dashboardEditMode = iota
	editFieldList
	editValueInput
)

// dashboardEditState is the `e`/`g` edit overlay's view state (TUI.md
// §9.6) — entirely client-side, mirrors showHelp's pattern. Deliberately
// does NOT cache current field values: the overlay reads them live off
// m.hello (global scope) / m.appRows[appName] (app scope) on every render
// (currentFieldValueText), so a server-pushed hello/snapshot refresh after
// a successful submit updates what's displayed with no special-casing.
type dashboardEditState struct {
	mode     dashboardEditMode
	scope    string // "global" | "app"
	appName  string // set when scope == "app"
	fields   []string
	selected int
	input    string // raw text buffer while mode == editValueInput
	applying bool
	err      string
	warnings []string
}

// dashboardSetResultMsg carries a completed {"cmd":"set"} round trip back
// into Update.
type dashboardSetResultMsg struct {
	resp ipc.SetResponse
	err  error
}

// openAppEdit opens the edit overlay (TUI.md §9.6) on the currently
// selected app's 8 tunable fields (PLAN.md §7). No-op if no app is
// selected/configured.
func (m dashboardModel) openAppEdit() (tea.Model, tea.Cmd) {
	rows := m.sortedAppRows()
	if len(rows) == 0 {
		return m, nil
	}
	idx := clampInt(m.selectedIdx, 0, len(rows)-1)
	m.edit = dashboardEditState{mode: editFieldList, scope: "app", appName: rows[idx].Name, fields: ipc.AppTunableFields}
	return m, nil
}

// openGlobalEdit opens the edit overlay on the 5 global tunable fields.
func (m dashboardModel) openGlobalEdit() (tea.Model, tea.Cmd) {
	m.edit = dashboardEditState{mode: editFieldList, scope: "global", fields: ipc.GlobalTunableFields}
	return m, nil
}

// updateEdit handles every key while the edit overlay is open (TUI.md
// §9.6): field-list navigation, entering/leaving value-input mode, a
// hand-rolled numeric input buffer (no bubbles/textinput dependency — see
// CLAUDE.md/TUI.md's "no other UI deps" rule), and submitting.
func (m dashboardModel) updateEdit(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.edit.mode {
	case editFieldList:
		switch msg.String() {
		case "esc":
			m.edit = dashboardEditState{}
		case "up", "k":
			if m.edit.selected > 0 {
				m.edit.selected--
			}
		case "down", "j":
			if m.edit.selected < len(m.edit.fields)-1 {
				m.edit.selected++
			}
		case "enter":
			m.edit.mode = editValueInput
			m.edit.input = m.currentFieldValueText(m.edit.fields[m.edit.selected])
			m.edit.err = ""
			m.edit.warnings = nil
		}
	case editValueInput:
		if m.edit.applying {
			return m, nil // ignore keys while a request is in flight
		}
		switch msg.String() {
		case "esc":
			m.edit.mode = editFieldList
			m.edit.input = ""
			m.edit.err = ""
			m.edit.warnings = nil
		case "enter":
			field := m.edit.fields[m.edit.selected]
			req := ipc.SetRequest{Scope: m.edit.scope, App: m.edit.appName, Field: field, Value: m.edit.input}
			m.edit.applying = true
			socketPath := m.socketPath
			return m, sendSetCmd(socketPath, req)
		case "backspace":
			if n := len(m.edit.input); n > 0 {
				m.edit.input = m.edit.input[:n-1]
			}
		default:
			if r := msg.Runes; len(r) == 1 && isValueChar(r[0], m.edit.input) {
				m.edit.input += string(r[0])
			}
		}
	}
	return m, nil
}

// isValueChar is the hand-rolled numeric input filter: digits anywhere,
// one leading '-', at most one '.'. Every tunable field (PLAN.md §7) is
// either an int or a float64, so this covers all of them without a real
// text-input widget.
func isValueChar(r rune, soFar string) bool {
	if r >= '0' && r <= '9' {
		return true
	}
	if r == '-' {
		return len(soFar) == 0
	}
	if r == '.' {
		return !strings.Contains(soFar, ".")
	}
	return false
}

func sendSetCmd(socketPath string, req ipc.SetRequest) tea.Cmd {
	return func() tea.Msg {
		resp, err := SendSet(socketPath, req)
		return dashboardSetResultMsg{resp: resp, err: err}
	}
}

// applySetResult handles a completed submit (Update's dashboardSetResultMsg
// case). On success the overlay returns to the field list — the
// *displayed* value updates via the ordinary hello/snapshot-frame refresh
// path (monitor.RefreshBus, TUI.md §9.6/§12.1), not an optimistic local
// patch here, so there's exactly one source of truth for "current value".
func (m dashboardModel) applySetResult(msg dashboardSetResultMsg) dashboardModel {
	m.edit.applying = false
	switch {
	case msg.err != nil:
		m.edit.err = msg.err.Error()
	case !msg.resp.OK:
		m.edit.err = msg.resp.Error
		m.edit.warnings = msg.resp.Warnings
	default:
		m.edit.mode = editFieldList
		m.edit.input = ""
		m.edit.err = ""
		m.edit.warnings = msg.resp.Warnings
	}
	return m
}

// currentFieldValueText reads a tunable field's live current value off
// model state — m.hello for global scope, the selected app's row for app
// scope — formatted exactly as the user would type it back in.
func (m dashboardModel) currentFieldValueText(field string) string {
	if m.edit.scope == "global" {
		switch field {
		case ipc.FieldCheckInterval:
			return strconv.Itoa(m.hello.CheckInterval)
		case ipc.FieldMaxSystemLoad:
			return formatEditFloat(m.hello.MaxSystemLoad)
		case ipc.FieldMaxIOWait:
			return strconv.Itoa(m.hello.MaxIOWait)
		case ipc.FieldMaxConcurrentJobs:
			return strconv.Itoa(m.hello.MaxConcurrentJobs)
		case ipc.FieldNice:
			return strconv.Itoa(m.hello.Nice)
		}
		return ""
	}

	row, ok := m.appRows[m.edit.appName]
	if !ok {
		return ""
	}
	switch field {
	case ipc.FieldMaxLoad:
		return formatEditFloat(row.MaxLoadCeiling)
	case ipc.FieldMaxIOWait:
		return strconv.Itoa(row.MaxIOWaitCeiling)
	case ipc.FieldMaxConcurrent:
		return strconv.Itoa(row.MaxConcurrent)
	case ipc.FieldMinInterval:
		return strconv.Itoa(row.MinInterval)
	case ipc.FieldSpikeCooldown:
		return strconv.Itoa(row.SpikeCooldown)
	case ipc.FieldPriority:
		return strconv.Itoa(row.Priority)
	case ipc.FieldNice:
		if row.Nice == nil {
			return "" // inherits global.nice — no explicit override to show
		}
		return strconv.Itoa(*row.Nice)
	case ipc.FieldBatchSize:
		return strconv.Itoa(row.BatchSize)
	}
	return ""
}

func formatEditFloat(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}

// editOverlayWidth is fixed (not terminal-width-relative), same rationale
// as helpOverlayWidth (dashboard_help.go): the content is short, fixed
// field/value/description text, comfortably under any reasonable terminal
// width, so there's no need to reflow it.
const editOverlayWidth = 64

// editOverlayContentWidth is how much of editOverlayWidth is left for
// wrapped text after the panel's border and padding — used to word-wrap
// descriptions/errors/warnings so they don't get truncated with "…" the
// way table cells do.
const editOverlayContentWidth = editOverlayWidth - 4

// editFieldLabelWidth is the field-name column's width in the field list —
// wide enough for the longest tunable field name ("max_concurrent_jobs").
const editFieldLabelWidth = 20

// fieldMeta is one tunable field's overlay copy: a short unit suffix shown
// next to its value (e.g. "%", "s" — "" for plain numbers, since load
// values are absolute like `uptime` and counts/priority/nice need none),
// and a one-line plain-English description of what it actually controls —
// the answer to "everyone should be able to tell what this does" without
// having to already know PLAN.md §7 by heart.
type fieldMeta struct {
	unit string
	desc string
}

var globalFieldMeta = map[string]fieldMeta{
	ipc.FieldCheckInterval:     {"s", "seconds between load polls — how fast the governor reacts"},
	ipc.FieldMaxSystemLoad:     {"", "hard load ceiling across ALL apps combined (absolute, like `uptime`)"},
	ipc.FieldMaxIOWait:         {"%", "global I/O-wait ceiling — catches disk/DB thrashing CPU alone misses"},
	ipc.FieldMaxConcurrentJobs: {"", "max worker processes running at once, across every app"},
	ipc.FieldNice:              {"", "default OS scheduling priority for every worker (-20 high … 19 low)"},
}

var appFieldMeta = map[string]fieldMeta{
	ipc.FieldMaxLoad:       {"", "this app's own load ceiling (must stay ≤ the global ceiling)"},
	ipc.FieldMaxIOWait:     {"%", "this app's own I/O-wait ceiling"},
	ipc.FieldMaxConcurrent: {"", "max simultaneous worker processes for this app alone"},
	ipc.FieldMinInterval:   {"s", "minimum gap between this app's dispatches"},
	ipc.FieldSpikeCooldown: {"s", "extra pause tacked on after a load spike is observed"},
	ipc.FieldPriority:      {"", "fair-dispatch weight — higher gets preferred when apps compete"},
	ipc.FieldNice:          {"", "OS scheduling priority for this app's worker (-20 high … 19 low)"},
	ipc.FieldBatchSize:     {"", "max jobs per batch (only used when mode is \"batch\")"},
}

// fieldMetaFor looks up a field's unit/description for the overlay's
// current scope — max_iowait and nice exist in both scopes with different
// wording, so this can't be one flat map keyed by field name alone.
func fieldMetaFor(scope, field string) fieldMeta {
	table := appFieldMeta
	if scope == "global" {
		table = globalFieldMeta
	}
	return table[field] // zero value (empty unit/desc) if somehow unknown
}

// renderEditOverlay renders the field list (or the value-input prompt for
// the selected field), a live description of whichever field is currently
// selected, plus any error/warnings, using the same centered-overlay
// chrome as the help overlay (TUI.md §6/§9.5/§9.6).
func renderEditOverlay(m dashboardModel) string {
	title := "edit: global tunables"
	if m.edit.scope == "app" {
		title = "edit: " + m.edit.appName
	}

	subtitleStyle := lipgloss.NewStyle().Foreground(colTextMuted)
	descStyle := lipgloss.NewStyle().Foreground(colTextSecond)
	labelStyle := lipgloss.NewStyle().Foreground(colTextPrimary)
	selectedLabelStyle := lipgloss.NewStyle().Foreground(colAccentKeys)
	valueStyle := lipgloss.NewStyle().Foreground(colTextSecond)

	lines := []string{
		subtitleStyle.Render("performance tweaks only · session-only — resets on restart/SIGHUP"),
		"",
	}

	for i, f := range m.edit.fields {
		meta := fieldMetaFor(m.edit.scope, f)

		label := labelStyle.Render(padCell(f, editFieldLabelWidth, false))
		marker := "  "
		if i == m.edit.selected {
			marker = selectedLabelStyle.Render("▸ ")
			label = selectedLabelStyle.Render(padCell(f, editFieldLabelWidth, false))
		}

		value := m.currentFieldValueText(f)
		if i == m.edit.selected && m.edit.mode == editValueInput {
			value = m.edit.input + "▏"
		} else if value != "" && meta.unit != "" {
			value += meta.unit
		} else if value == "" && f == ipc.FieldNice {
			value = "(inherits global.nice)"
		}

		lines = append(lines, marker+label+valueStyle.Render(value))
	}

	lines = append(lines, "")
	if sel := m.edit.selected; sel >= 0 && sel < len(m.edit.fields) {
		meta := fieldMetaFor(m.edit.scope, m.edit.fields[sel])
		if meta.desc != "" {
			lines = append(lines, descStyle.Render(wrapText(meta.desc, editOverlayContentWidth)))
		}
	}

	if m.edit.applying {
		lines = append(lines, "", "applying…")
	}
	if m.edit.err != "" {
		lines = append(lines, "", lipgloss.NewStyle().Foreground(colStateThrottled).Render(wrapText(m.edit.err, editOverlayContentWidth)))
	}
	for _, w := range m.edit.warnings {
		lines = append(lines, lipgloss.NewStyle().Foreground(colStateCooldown).Render(wrapText(w, editOverlayContentWidth)))
	}

	hint := "↑↓/jk select   enter edit   esc close"
	if m.edit.mode == editValueInput {
		hint = "type a new value   enter submit   esc back"
	}
	lines = append(lines, "", lipgloss.NewStyle().Foreground(colTextMuted).Render(hint))

	return titledPanel(title, editOverlayWidth, colAccentKeys, strings.Join(lines, "\n"))
}

// wrapText breaks s into width-limited lines on word boundaries, joined
// with "\n" — the overlay's descriptions/errors are plain-English
// sentences that can run longer than a field row and shouldn't get
// truncated with "…" the way table cells do (padCell), since the whole
// point here is to actually explain something.
func wrapText(s string, width int) string {
	if width <= 0 || len([]rune(s)) <= width {
		return s
	}
	words := strings.Fields(s)
	var lines []string
	cur := ""
	for _, w := range words {
		if cur == "" {
			cur = w
			continue
		}
		if len([]rune(cur))+1+len([]rune(w)) > width {
			lines = append(lines, cur)
			cur = w
			continue
		}
		cur += " " + w
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return strings.Join(lines, "\n")
}
