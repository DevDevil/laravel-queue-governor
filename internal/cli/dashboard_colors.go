package cli

import "github.com/charmbracelet/lipgloss"

// TUI.md §5.2 — the dashboard's own literal palette, defined once here.
var (
	colLoadBorder   = lipgloss.Color("#3A6D2C")
	colIOWaitBorder = lipgloss.Color("#7A5410")
	colAppsBorder   = lipgloss.Color("#2F5A86")
	colLogBorder    = lipgloss.Color("#333331")
	colAccentKeys   = lipgloss.Color("#5DCAA5")
	colTextPrimary  = lipgloss.Color("#E2E2DA")
	colTextSecond   = lipgloss.Color("#9A9A92")
	colTextMuted    = lipgloss.Color("#6A6A62")

	colStateRunning   = lipgloss.Color("#97C459")
	colStateCooldown  = lipgloss.Color("#EF9F27")
	colStateThrottled = lipgloss.Color("#E24B4A")
	colStateIdle      = lipgloss.Color("#7A7A72")

	// colRowHighlight is the apps table's selected-row background
	// (TUI.md §9.1: "Highlight = reverse or bright row") — not a spec
	// hex, TUI.md doesn't give one; a subtle dark neutral chosen so cell
	// foreground colours stay legible on top of it.
	colRowHighlight = lipgloss.Color("#33333F")
)

// Bright/dim rgb pairs for the graph fill's depth ramp, reusing braille.go's
// generic rgb/lerpColor helpers (those are palette-agnostic; dashRampColor
// below is what supplies this dashboard's own hues to them). Green/amber
// bright+dim both come straight from TUI.md §5.2's "load line/area" and
// "iowait line/area"
// hexes — §5.3 applies the same green/amber/red threshold rule to the
// graph line regardless of which metric it is (a value's own current-vs-
// ceiling ratio decides the colour, not which panel it's in), so these
// three pairs are shared by both the load and iowait graphs. §5.2 gives
// no dim variant for red (state throttled has no "area fill" counterpart
// anywhere in the table) — dashRedDim is this rebuild's own interpolated
// darkening of colStateThrottled, at roughly the same bright:dim ratio
// the other two pairs use.
var (
	dashGreenBright = rgb{0x97, 0xC4, 0x59}
	dashGreenDim    = rgb{0x63, 0x99, 0x22}
	dashAmberBright = rgb{0xEF, 0x9F, 0x27}
	dashAmberDim    = rgb{0xBA, 0x75, 0x17}
	dashRedBright   = rgb{0xE2, 0x4B, 0x4A}
	dashRedDim      = rgb{0x4F, 0x1A, 0x1A}
)

// dashRampColor is brailleGraph's colorFor for this dashboard: the same
// bright-at-top/dim-at-baseline depth ramp brailleGraph always applies,
// using this file's TUI.md-specific hues.
func dashRampColor(level int, depth float64) lipgloss.Color {
	switch level {
	case 0:
		return lerpColor(dashGreenBright, dashGreenDim, depth)
	case 1:
		return lerpColor(dashAmberBright, dashAmberDim, depth)
	default:
		return lerpColor(dashRedBright, dashRedDim, depth)
	}
}

// dashThresholdColor is TUI.md §5.3's traffic-light rule, reused for any
// value measured against a ceiling: green under 70%, amber 70-100%, red
// at/over. ceiling<=0 (not known yet — e.g. before hello arrives, or an
// app with no iowait ceiling configured) renders muted rather than
// asserting a colour with no basis for it.
func dashThresholdColor(value, ceiling float64) lipgloss.Color {
	if ceiling <= 0 {
		return colTextMuted
	}
	t := value / ceiling
	switch {
	case t >= 1.0:
		return colStateThrottled
	case t >= 0.70:
		return colStateCooldown
	default:
		return colStateRunning
	}
}

// dashStateColor + dashStateGlyph are TUI.md §8.2's state dots. Only
// idle/running/cooldown/throttled exist in appstate.Machine — the
// spec's parenthetical "dispatching" transient has no backing state, so
// there's no case for it.
func dashStateColor(state string) lipgloss.Color {
	switch state {
	case "running":
		return colStateRunning
	case "cooldown":
		return colStateCooldown
	case "throttled":
		return colStateThrottled
	default:
		return colStateIdle
	}
}

func dashStateGlyph(state string) string {
	switch state {
	case "running":
		return "●"
	case "cooldown":
		return "◐"
	default:
		return "○" // throttled, idle
	}
}
