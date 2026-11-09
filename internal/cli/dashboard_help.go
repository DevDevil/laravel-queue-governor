package cli

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// helpOverlayWidth is fixed (not terminal-width-relative) — the content
// is short, fixed keybind/legend text, comfortably under any reasonable
// terminal width, so there's no need to reflow it.
const helpOverlayWidth = 58

// renderHelpOverlay is TUI.md §9.5's keybind + colour legend overlay:
// grouped into sections (navigate / live tune / other / legend) with a
// one-line description of what `lqg watch` is, so it doubles as an
// onboarding screen for someone who's never used it before, not just a
// keybind cheat-sheet.
func renderHelpOverlay() string {
	section := lipgloss.NewStyle().Foreground(colAccentKeys).Bold(true)
	muted := lipgloss.NewStyle().Foreground(colTextMuted)
	key := lipgloss.NewStyle().Foreground(colAccentKeys)
	desc := lipgloss.NewStyle().Foreground(colTextSecond)

	kb := func(keys, text string) string {
		return key.Render(padCell(keys, 13, false)) + desc.Render(text)
	}

	lines := []string{
		muted.Render("live view of the lqg queue governor daemon"),
		"",
		section.Render("navigate"),
		kb("↑/↓, k/j", "select row"),
		kb("2, s", "cycle sort (name → state → load → jobs)"),
		kb("3, f", "focus selected app, dim/hide the rest"),
		kb("space, p", "pause / resume live updates"),
		kb("r", "reconnect now"),
		"",
		section.Render("live tune — session-only, resets on restart/SIGHUP"),
		kb("e", "edit selected app's performance knobs"),
		kb("g", "edit the daemon's global performance knobs"),
		"",
		section.Render("other"),
		kb("1, ?", "toggle this help"),
		kb("esc", "close help/edit, or exit focus"),
		kb("q, ctrl+c", "quit"),
		"",
		section.Render("state"),
		"● running    ◐ cooldown",
		"○ throttled  ○ idle",
		"",
		section.Render("load / iowait colour"),
		"green <70% of ceiling   amber 70–100%   red ≥100%",
	}
	return titledPanel("help", helpOverlayWidth, colAccentKeys, strings.Join(lines, "\n"))
}

// compositeOverlay centers overlay on top of background (TUI.md §9.5:
// "repaint the frame with the overlay composited on top", not a real
// terminal layer) by replacing the rows and columns the overlay occupies
// — background content in that region is fully overwritten, not dimmed
// through, which is a deliberate simplification: dimming would mean
// reprocessing every ANSI-coloured background cell it overlaps, for a
// cosmetic effect the acceptance criteria don't require.
func compositeOverlay(background, overlay string, termWidth, termHeight int) string {
	bgLines := strings.Split(background, "\n")
	ovLines := strings.Split(overlay, "\n")

	ovWidth := 0
	for _, l := range ovLines {
		if w := lipgloss.Width(l); w > ovWidth {
			ovWidth = w
		}
	}

	top := centerOffset(termHeight, len(ovLines))
	left := centerOffset(termWidth, ovWidth)

	for i, l := range ovLines {
		row := top + i
		if row < 0 || row >= len(bgLines) {
			continue
		}
		visible := lipgloss.Width(l)
		trailing := termWidth - left - visible
		if trailing < 0 {
			trailing = 0
		}
		bgLines[row] = strings.Repeat(" ", left) + l + strings.Repeat(" ", trailing)
	}
	return strings.Join(bgLines, "\n")
}
