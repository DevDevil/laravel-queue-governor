package cli

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// truncateWithEllipsis returns s unchanged if it already fits within max
// runes, or cut short with a trailing "…" if not (TUI.md §14: truncate,
// never let content just disappear or wrap) — the one place this package
// decides how truncation looks, shared by padCell's cell truncation and
// titledPanel's/titledPanelWithLabel's title and label truncation.
func truncateWithEllipsis(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	if max <= 0 {
		return ""
	}
	if max == 1 {
		return string(r[:1])
	}
	return string(r[:max-1]) + "…"
}

// centerOffset returns the position to start placing something of size
// content centered within container, clamped to 0 when content doesn't
// fit — the one clamped "(container-content)/2" computation shared by
// every place in this package that centers a block: renderTooSmallMessage
// and compositeOverlay center on both axes; centerLinesVertically
// (dashboard_graphs.go) only needs the vertical one.
func centerOffset(container, content int) int {
	d := (container - content) / 2
	if d < 0 {
		return 0
	}
	return d
}

// titledPanel renders a lipgloss RoundedBorder panel with the title
// embedded directly in the top border (╭─ title ─...─╮) rather than as a
// separate content line (TUI.md §6). lipgloss has no built-in titled-
// border support, so the top edge is drawn by hand and the body rendered
// with BorderTop(false); verified empirically that Style.Width(width)
// already includes padding, with the border adding exactly 2 more
// columns (1 each side) on top of that, so the hand-drawn top line's
// rune count must equal width+2 to line up with the body.
func titledPanel(title string, width int, borderColor lipgloss.Color, content string) string {
	border := lipgloss.RoundedBorder()
	total := width + 2

	// A title longer than the panel would otherwise grow the top line
	// past the body's fixed width, breaking alignment (caught by
	// TestTitledPanel_VeryLongTitleDoesNotBreakAlignment) — truncate to
	// whatever fits: corners(2) + lead dash(1) + 2 spaces + >=1 dash.
	maxTitleLen := total - 6
	if maxTitleLen < 0 {
		maxTitleLen = 0
	}
	title = truncateWithEllipsis(title, maxTitleLen)
	titleRunes := []rune(title)

	dashes := total - 5 - len(titleRunes) // 5 = corners(2) + lead dash(1) + 2 spaces
	if dashes < 1 {
		dashes = 1
	}
	top := border.TopLeft + border.Top + " " + title + " " + strings.Repeat(border.Top, dashes) + border.TopRight

	body := lipgloss.NewStyle().
		Border(border).
		BorderTop(false).
		BorderForeground(borderColor).
		Padding(0, 1).
		Width(width).
		Render(content)

	return lipgloss.NewStyle().Foreground(borderColor).Render(top) + "\n" + body
}

// titledPanelWithLabel is titledPanel plus a right-aligned secondary
// label in the top border (TUI.md §6), e.g.
// "╭─ apps ─────────────── running 2/2 ─╮" — a separate function rather
// than adding a parameter to titledPanel, since most callers don't need
// the label and titledPanel's simpler signature is easier to reason
// about on its own.
func titledPanelWithLabel(title string, width int, borderColor lipgloss.Color, rightLabel string, content string) string {
	border := lipgloss.RoundedBorder()
	total := width + 2

	maxTitleLen := total - 6
	if maxTitleLen < 0 {
		maxTitleLen = 0
	}
	title = truncateWithEllipsis(title, maxTitleLen)
	titleRunes := []rune(title)

	// Budget left for "dashes + labelPart" after the title and the fixed
	// corners/dash/spaces (6, titledPanel's own convention) — at least 1
	// dash must remain even with no label at all.
	labelBudget := total - len(titleRunes) - 6 - 1
	labelPart := ""
	if rightLabel != "" && labelBudget > 0 {
		// " " + label + " " = 2 + len(label) — only drop the label
		// entirely if there's no room even for one truncated character
		// of it (TUI.md §14: truncate, don't just disappear content).
		maxLabelLen := labelBudget - 2
		if maxLabelLen > 0 {
			labelPart = " " + truncateWithEllipsis(rightLabel, maxLabelLen) + " "
		}
	}

	dashes := total - len(titleRunes) - len([]rune(labelPart)) - 6
	if dashes < 1 {
		dashes = 1
	}

	top := border.TopLeft + border.Top + " " + title + " " +
		strings.Repeat(border.Top, dashes) + labelPart + border.Top + border.TopRight

	body := lipgloss.NewStyle().
		Border(border).
		BorderTop(false).
		BorderForeground(borderColor).
		Padding(0, 1).
		Width(width).
		Render(content)

	return lipgloss.NewStyle().Foreground(borderColor).Render(top) + "\n" + body
}
