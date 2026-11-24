// The TUI.md-spec full-screen dashboard: `lqg watch`'s default view on a
// real terminal. Confined to internal/cli per CLAUDE.md/TUI.md: the
// daemon never imports bubbletea/lipgloss.
package cli

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/DevDevil/laravel-queue-governor/internal/ipc"
)

const (
	dashMinWidth  = 80
	dashMinHeight = 24

	graphsBoxHeight = 9 // TUI.md §4: fixed, incl. borders
)

// Apps-table sort modes (TUI.md §9.2): name -> state (running first) ->
// load (desc) -> jobs (desc) -> back to name.
const (
	dashSortName = iota
	dashSortState
	dashSortLoad
	dashSortJobs
	dashSortModeCount // sentinel: count of sort modes above, not a mode itself
)

type dashboardFrameMsg dashboardFrame
type dashboardDisconnectMsg struct{ err error }
type dashboardDoneMsg struct{}

// dashboardModel is the bubbletea Model for the TUI.md dashboard.
type dashboardModel struct {
	frames       <-chan dashboardFrame
	disconnectCh <-chan error
	retryNow     chan<- struct{}
	socketPath   string

	width, height int

	connected bool
	lastErr   error

	hello ipc.HelloFrame

	lastSample    ipc.MetricSample
	haveSample    bool
	loadHistory   []float64
	iowaitHistory []float64

	appRows     map[string]*dashboardAppRow
	appOrder    []string
	sortMode    int
	selectedIdx int
	focused     bool

	logEntries []dashboardLogEntry

	paused      bool
	pausedQueue []dashboardFrame

	showHelp bool
	edit     dashboardEditState
}

func newDashboardModel(frames <-chan dashboardFrame, disconnectCh <-chan error, retryNow chan<- struct{}, socketPath string) dashboardModel {
	return dashboardModel{
		frames:       frames,
		disconnectCh: disconnectCh,
		retryNow:     retryNow,
		socketPath:   socketPath,
		appRows:      make(map[string]*dashboardAppRow),
		// Optimistic: the very first connection attempt hasn't failed
		// yet, so there's nothing to show a "disconnected" banner about.
		// A real failure flips this via dashboardDisconnectMsg.
		connected: true,
	}
}

func (m dashboardModel) sortModeName() string {
	switch m.sortMode {
	case dashSortState:
		return "state"
	case dashSortLoad:
		return "load"
	case dashSortJobs:
		return "jobs"
	default:
		return "name"
	}
}

func (m dashboardModel) Init() tea.Cmd {
	return waitForDashboardFrame(m.frames, m.disconnectCh)
}

func waitForDashboardFrame(frames <-chan dashboardFrame, disconnectCh <-chan error) tea.Cmd {
	return func() tea.Msg {
		select {
		case f, ok := <-frames:
			if !ok {
				return dashboardDoneMsg{}
			}
			return dashboardFrameMsg(f)
		case err := <-disconnectCh:
			return dashboardDisconnectMsg{err: err}
		}
	}
}

func (m dashboardModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	case tea.KeyMsg:
		// TUI.md §9.5: "Any key or Esc closes it" — intercept everything
		// while the overlay is open, rather than letting it also trigger
		// whatever that key normally does.
		if m.showHelp {
			m.showHelp = false
			return m, nil
		}
		if m.edit.mode != editClosed {
			return m.updateEdit(msg)
		}
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "2", "s":
			m.sortMode = (m.sortMode + 1) % dashSortModeCount
			return m, nil
		case "up", "k":
			if m.selectedIdx > 0 {
				m.selectedIdx--
			}
			return m, nil
		case "down", "j":
			if m.selectedIdx < len(m.appOrder)-1 {
				m.selectedIdx++
			}
			return m, nil
		case "3", "f":
			m.focused = !m.focused
			return m, nil
		case "esc":
			if m.focused {
				m.focused = false
			}
			return m, nil
		case "1", "?":
			m.showHelp = true
			return m, nil
		case "e":
			return m.openAppEdit()
		case "g":
			return m.openGlobalEdit()
		case " ", "p":
			m.paused = !m.paused
			if !m.paused {
				// Resume: apply everything that arrived while paused, in
				// order, catching up to live (TUI.md §9.3) rather than
				// jumping straight to whatever's most recent.
				queue := m.pausedQueue
				m.pausedQueue = nil
				for _, f := range queue {
					m.apply(f)
				}
			}
			return m, nil
		case "r":
			retryNow := m.retryNow
			return m, func() tea.Msg {
				select {
				case retryNow <- struct{}{}:
				default:
				}
				return nil
			}
		}
	case dashboardFrameMsg:
		f := dashboardFrame(msg)
		// Connection status is orthogonal to the paused *data* view — a
		// reconnect should clear the disconnect banner immediately, not
		// wait for the user to unpause.
		if f.Hello != nil {
			m.connected = true
		}
		if m.paused {
			m.pausedQueue = append(m.pausedQueue, f)
		} else {
			m.apply(f)
		}
		return m, waitForDashboardFrame(m.frames, m.disconnectCh)
	case dashboardDisconnectMsg:
		m.connected = false
		m.lastErr = msg.err
		return m, waitForDashboardFrame(m.frames, m.disconnectCh)
	case dashboardDoneMsg:
		return m, tea.Quit
	case dashboardSetResultMsg:
		return m.applySetResult(msg), nil
	}
	return m, nil
}

func (m *dashboardModel) apply(f dashboardFrame) {
	switch {
	case f.Hello != nil:
		m.hello = *f.Hello
	case f.History != nil:
		m.loadHistory = m.loadHistory[:0]
		m.iowaitHistory = m.iowaitHistory[:0]
		for _, s := range f.History.Samples {
			m.loadHistory = append(m.loadHistory, s.Load)
			m.iowaitHistory = append(m.iowaitHistory, s.IOWait)
		}
		if n := len(f.History.Samples); n > 0 {
			m.lastSample = f.History.Samples[n-1]
			m.haveSample = true
		}
	case f.Snapshot != nil:
		m.appRows = make(map[string]*dashboardAppRow, len(f.Snapshot.Apps))
		m.appOrder = m.appOrder[:0]
		for _, a := range f.Snapshot.Apps {
			reason := ""
			if a.Reason != nil {
				reason = *a.Reason
			}
			m.appRows[a.Name] = &dashboardAppRow{
				Name:             a.Name,
				State:            a.State,
				Load:             a.Load,
				IOWait:           a.IOWait,
				Mode:             a.Mode,
				JobsSession:      a.JobsSession,
				Reason:           reason,
				MaxLoadCeiling:   a.MaxLoadCeiling,
				MaxIOWaitCeiling: a.MaxIOWaitCeiling,
				MaxConcurrent:    a.MaxConcurrent,
				BatchSize:        a.BatchSize,
				ActiveRuns:       a.ActiveRuns,
				MinInterval:      a.MinInterval,
				SpikeCooldown:    a.SpikeCooldown,
				Priority:         a.Priority,
				Nice:             a.Nice,
				Schedule:         a.Schedule,
			}
			m.appOrder = append(m.appOrder, a.Name)
		}
	case f.Sample != nil:
		m.lastSample = f.Sample.MetricSample
		m.haveSample = true
		m.loadHistory = appendCapped(m.loadHistory, f.Sample.Load, dashHistoryCapacity)
		m.iowaitHistory = appendCapped(m.iowaitHistory, f.Sample.IOWait, dashHistoryCapacity)
	case f.Decision != nil:
		d := f.Decision
		row, ok := m.appRows[d.App]
		if !ok {
			row = &dashboardAppRow{Name: d.App}
			m.appRows[d.App] = row
			m.appOrder = append(m.appOrder, d.App)
		}
		if d.Kind == ipc.KindSchedule {
			// A schedule:run decision says nothing about the app's queue
			// FSM — only its schedule status and the log change.
			row.Schedule = d.Schedule
			m.logEntries = appendCappedLogEntries(m.logEntries, formatScheduleLogEntry(d.DecisionEvent), dashLogCapacity)
			return
		}
		row.State = d.State
		row.Load = d.Load
		row.IOWait = d.IOWait
		row.Reason = ""
		if d.Reason != nil {
			row.Reason = *d.Reason
		}
		if d.Decision == "dispatched" && d.JobSeq != nil {
			row.JobsSession = *d.JobSeq
		}
		row.ActiveRuns = d.ActiveRuns
		m.logEntries = appendCappedLogEntries(m.logEntries, formatLogEntry(d.DecisionEvent, *row, m.hello), dashLogCapacity)
	}
}

// sortedAppRows returns the known apps ordered per the current sortMode
// (TUI.md §9.2), stable so ties keep their prior relative order.
func (m dashboardModel) sortedAppRows() []dashboardAppRow {
	rows := make([]dashboardAppRow, len(m.appOrder))
	for i, name := range m.appOrder {
		rows[i] = *m.appRows[name]
	}
	switch m.sortMode {
	case dashSortState:
		sort.SliceStable(rows, func(i, j int) bool { return dashStateRank(rows[i].State) < dashStateRank(rows[j].State) })
	case dashSortLoad:
		sort.SliceStable(rows, func(i, j int) bool { return rows[i].Load > rows[j].Load })
	case dashSortJobs:
		sort.SliceStable(rows, func(i, j int) bool { return rows[i].JobsSession > rows[j].JobsSession })
	default:
		sort.SliceStable(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	}
	return rows
}

// runningCount is how many known apps are currently in the running state
// (TUI.md §8.4's "running N/M" label).
func (m dashboardModel) runningCount() int {
	n := 0
	for _, name := range m.appOrder {
		if m.appRows[name].State == "running" {
			n++
		}
	}
	return n
}

func (m dashboardModel) View() string {
	if m.width <= 0 || m.height <= 0 {
		return ""
	}
	// TUI.md §3: below the minimum size, show nothing but this message —
	// resumes normal rendering as soon as the terminal grows back, since
	// this is just a View() branch, not any persisted state.
	if m.width < dashMinWidth || m.height < dashMinHeight {
		return renderTooSmallMessage(m.width, m.height)
	}

	rows := m.sortedAppRows()
	selected := clampInt(m.selectedIdx, 0, maxInt(len(rows)-1, 0))
	displayRows, highlightIdx, loadCeiling, iowaitCeiling, loadRightLabel, iowaitRightLabel, logEntries := m.applyFocus(rows, selected)

	graphsRow := m.buildGraphsRow(loadCeiling, iowaitCeiling, loadRightLabel, iowaitRightLabel)
	appsBox, appsBoxHeight := m.buildAppsBox(displayRows, highlightIdx)
	logBox := m.buildLogBox(logEntries, appsBoxHeight)

	var b strings.Builder
	b.WriteString(graphsRow)
	b.WriteString("\n")
	b.WriteString(appsBox)
	b.WriteString("\n")
	b.WriteString(logBox)
	b.WriteString("\n")
	b.WriteString(footerLine(m, rows, selected))

	out := b.String()
	if m.showHelp {
		out = compositeOverlay(out, renderHelpOverlay(), m.width, m.height)
	}
	if m.edit.mode != editClosed {
		out = compositeOverlay(out, renderEditOverlay(m), m.width, m.height)
	}
	return out
}

// applyFocus resolves TUI.md §9.4's focus mode: when active, narrows the
// apps table to just the selected app, retitles the graphs to that app's
// own ceiling, and filters the log to just its entries. All of View()'s
// other panel-builders take their inputs from here rather than checking
// m.focused themselves, so there's exactly one place that decides what
// "focused" changes.
func (m dashboardModel) applyFocus(rows []dashboardAppRow, selected int) (
	displayRows []dashboardAppRow, highlightIdx int,
	loadCeiling, iowaitCeiling float64,
	loadRightLabel, iowaitRightLabel string,
	logEntries []dashboardLogEntry,
) {
	displayRows = rows
	highlightIdx = selected
	loadCeiling = m.hello.MaxSystemLoad
	iowaitCeiling = float64(m.hello.MaxIOWait)
	loadRightLabel, iowaitRightLabel = "5m", "%"
	logEntries = m.logEntries

	if !m.focused || len(rows) == 0 {
		return
	}

	focusedRow := rows[selected]
	displayRows = []dashboardAppRow{focusedRow}
	highlightIdx = 0
	loadCeiling = focusedRow.MaxLoadCeiling
	iowaitCeiling = float64(focusedRow.MaxIOWaitCeiling)
	loadRightLabel = focusedRow.Name
	iowaitRightLabel = focusedRow.Name

	filtered := make([]dashboardLogEntry, 0, len(m.logEntries))
	for _, e := range m.logEntries {
		if e.App == focusedRow.Name {
			filtered = append(filtered, e)
		}
	}
	logEntries = filtered
	return
}

// buildGraphsRow renders the load+iowait graph panels side by side
// (TUI.md §7). loadCeiling/iowaitCeiling/loadRightLabel/iowaitRightLabel
// come from applyFocus: the global ceiling/labels normally, or the
// focused app's own when focus mode is active.
func (m dashboardModel) buildGraphsRow(loadCeiling, iowaitCeiling float64, loadRightLabel, iowaitRightLabel string) string {
	graphWidth := (m.width - 1) / 2

	perCoreText := ""
	if m.haveSample && m.hello.Cores > 0 {
		perCoreText = fmt.Sprintf("%.2f/core", m.lastSample.Load/float64(m.hello.Cores))
	}
	loadValueText, loadCeilingText := "—", "—"
	iowaitValueText, iowaitCeilingText := "—", "—"
	if m.haveSample {
		loadValueText = formatLoadReadout(m.lastSample.Load)
		loadCeilingText = formatLoadReadout(loadCeiling)
		iowaitValueText = formatIOWaitReadout(m.lastSample.IOWait)
		iowaitCeilingText = formatIOWaitReadout(iowaitCeiling)
	}

	loadBox := buildGraphBox("load", m.loadHistory, graphReadout{
		Value: m.lastSample.Load, Ceiling: loadCeiling,
		ValueText: loadValueText, CeilingText: loadCeilingText, ExtraText: perCoreText,
	}, m.haveSample, colLoadBorder, loadRightLabel, graphWidth, graphsBoxHeight)
	iowaitBox := buildGraphBox("iowait", m.iowaitHistory, graphReadout{
		Value: m.lastSample.IOWait, Ceiling: iowaitCeiling,
		ValueText: iowaitValueText, CeilingText: iowaitCeilingText,
	}, m.haveSample, colIOWaitBorder, iowaitRightLabel, m.width-graphWidth-1, graphsBoxHeight)
	return lipgloss.JoinHorizontal(lipgloss.Top, loadBox, " ", iowaitBox)
}

// buildAppsBox renders the apps table panel (TUI.md §8), and also
// returns its total outer height, which buildLogBox needs in order to
// know how much vertical space is left for the log panel.
func (m dashboardModel) buildAppsBox(displayRows []dashboardAppRow, highlightIdx int) (box string, outerHeight int) {
	appsInnerWidth := m.width - 2
	if appsInnerWidth < 1 {
		appsInnerWidth = 1
	}
	now := time.Now()
	appsContent := renderAppsTable(displayRows, appsInnerWidth, m.hello, highlightIdx, now)

	// TUI.md §8.4 wants "running N/M" in the apps panel's top-border right
	// label; §9.2 separately wants the current sort mode shown there too.
	// The spec doesn't reconcile the two sharing one label slot, so both
	// are shown together rather than picking one over the other.
	appsLabel := fmt.Sprintf("sort: %s   running %d/%d", m.sortModeName(), m.runningCount(), m.hello.MaxConcurrentJobs)

	// buildLogBox needs the total outer height up front to size the log
	// panel around it — content lines (from the shared appsTableLineCount,
	// so this can't drift from what renderAppsTable actually emits) + 2
	// borders. No zero-apps special case: appsTableLineCount(nil) is
	// already 1 (the "no apps configured" line), matching exactly what
	// titledPanelWithLabel renders — a previous "+1 for symmetry" here
	// over-reported the height by one line, leaving View() one line short
	// of the terminal height whenever zero apps were configured.
	outerHeight = appsTableLineCount(displayRows) + 2
	box = titledPanelWithLabel("apps", appsInnerWidth, colAppsBorder, appsLabel, appsContent)
	return box, outerHeight
}

// buildLogBox renders the log panel (TUI.md §10), filling all vertical
// space left after the fixed graphs row, the apps box (whose height
// varies with row count), and the 1-row footer.
func (m dashboardModel) buildLogBox(logEntries []dashboardLogEntry, appsBoxHeight int) string {
	usedHeight := graphsBoxHeight + appsBoxHeight + 1 // +1 footer
	logOuterHeight := m.height - usedHeight
	if logOuterHeight < 3 {
		logOuterHeight = 3
	}
	logInnerWidth := m.width - 2
	if logInnerWidth < 1 {
		logInnerWidth = 1
	}
	logInnerHeight := logOuterHeight - 2
	if logInnerHeight < 1 {
		logInnerHeight = 1
	}
	return titledPanel("log", logInnerWidth, colLogBorder, renderLogContent(logEntries, logInnerHeight))
}

// renderTooSmallMessage is TUI.md §3's below-minimum-size screen: a
// single centered line, nothing else — produces exactly height lines so
// bubbletea's line-diffing still has a consistent frame to compare
// against on the next resize.
func renderTooSmallMessage(width, height int) string {
	msg := "terminal too small — need at least 80×24"
	styled := lipgloss.NewStyle().Foreground(colStateThrottled).Render(msg)

	lines := make([]string, height)
	center := centerOffset(height, 1)
	left := centerOffset(width, len([]rune(msg)))
	for i := range lines {
		if i == center {
			lines[i] = strings.Repeat(" ", left) + styled
		} else {
			lines[i] = ""
		}
	}
	return strings.Join(lines, "\n")
}

func footerLine(m dashboardModel, rows []dashboardAppRow, selected int) string {
	// TUI.md §12.4: the disconnect banner is the most urgent state —
	// show it alone rather than alongside the normal keybind hints.
	if !m.connected {
		return lipgloss.NewStyle().Foreground(colStateThrottled).Render(" ⚠ daemon disconnected — reconnecting…")
	}

	if m.focused {
		name := ""
		if len(rows) > 0 {
			name = rows[selected].Name
		}
		text := fmt.Sprintf(" focus: %s   esc back", name)
		if m.paused {
			text += "   ‖ PAUSED"
		}
		return lipgloss.NewStyle().Foreground(colAccentKeys).Render(text)
	}

	accent := lipgloss.NewStyle().Foreground(colAccentKeys)
	label := lipgloss.NewStyle().Foreground(colTextSecond)
	footer := " " + accent.Render("1") + label.Render(" help   ") +
		accent.Render("2") + label.Render(" sort   ") +
		accent.Render("3") + label.Render(" filter   ") +
		accent.Render("e") + label.Render(" edit   ") +
		accent.Render("g") + label.Render(" global   ") +
		accent.Render("space") + label.Render(" pause   ") +
		accent.Render("q") + label.Render(" quit")

	if m.paused {
		return lipgloss.NewStyle().Foreground(colStateCooldown).Render("‖ PAUSED  ") + footer
	}
	return footer
}
