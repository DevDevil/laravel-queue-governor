package cli

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// dashHistoryCapacity matches monitor.DefaultMetricHistorySize (300
// samples at the sampler's ~1s cadence = TUI.md §7.2's 5-minute window).
// A local constant rather than importing internal/monitor just for one
// number — internal/cli has no other reason to depend on that package.
const dashHistoryCapacity = 300

// dashGraphMinCols keeps the readout from crowding the graph to nothing
// on a narrow terminal.
const dashGraphMinCols = 10

// graphReadoutNumerator/Denominator: the readout column gets
// ~graphReadoutNumerator/graphReadoutDenominator (30%) of the graph
// panel's content width (TUI.md §7.1's "~30%"); kept as an integer ratio
// rather than a float constant so the division stays exact integer math
// (a float equivalent risks an off-by-one from binary floating-point
// rounding on some widths).
const (
	graphReadoutNumerator   = 3
	graphReadoutDenominator = 10
)

// appendCapped appends v to buf, keeping at most cap most-recent values —
// the ring buffer backing the load/iowait graphs' history.
func appendCapped(buf []float64, v float64, cap int) []float64 {
	buf = append(buf, v)
	if len(buf) > cap {
		buf = buf[len(buf)-cap:]
	}
	return buf
}

// graphReadout groups one metric's current value/ceiling and their
// pre-formatted display text into a single unit — buildGraphBox took
// these as five separate params (three adjacent strings, one adjacent
// float pair) positionally, which invited a silent transposition at the
// call site (e.g. value/ceiling swapped) that would only ever surface as
// a rendering glitch, not a compile error.
type graphReadout struct {
	Value       float64
	Ceiling     float64
	ValueText   string
	CeilingText string
	ExtraText   string
}

// buildGraphBox renders one load/iowait panel (TUI.md §7): a braille
// history graph over the left ~70% of the inner width, a right-aligned
// value/ceiling(/per-core) readout over the remaining ~30% (see
// graphReadoutNumerator/Denominator), vertically centered in the panel.
// haveData gates the readout: before the first sample arrives there's
// nothing real to show, and "0.00" would look like an actual reading
// rather than "no data yet".
func buildGraphBox(title string, history []float64, readout graphReadout, haveData bool, borderColor lipgloss.Color, rightLabel string, outerWidth, outerHeight int) string {
	innerWidth := outerWidth - 2
	if innerWidth < 1 {
		innerWidth = 1
	}
	innerHeight := outerHeight - 2
	if innerHeight < 1 {
		innerHeight = 1
	}

	// titledPanelWithLabel's body applies Padding(0,1) *inside* its own
	// Width(innerWidth) box, so only innerWidth-2 columns are actually
	// available for content before that box's own left/right padding
	// eats into it — content sized to the full innerWidth overflows by
	// exactly 2 columns. For most content that overflow silently clips
	// two trailing blank padding columns (harmless), but this panel's
	// content is a braille graph — dozens of individually ANSI-styled
	// characters per line — and lipgloss's wrap-on-overflow logic
	// mishandles that dense a line, multiplying it into many extra
	// lines instead of clipping two trailing columns. Sizing content to
	// contentWidth (not innerWidth) avoids the overflow outright rather
	// than relying on it happening to clip harmlessly.
	contentWidth := innerWidth - 2
	if contentWidth < 1 {
		contentWidth = 1
	}

	readoutLines := []string{readout.ValueText, "/ " + readout.CeilingText}
	if readout.ExtraText != "" {
		readoutLines = append(readoutLines, readout.ExtraText)
	}
	readoutNatural := 0
	for _, l := range readoutLines {
		if n := len([]rune(l)); n > readoutNatural {
			readoutNatural = n
		}
	}

	readoutWidth := contentWidth * graphReadoutNumerator / graphReadoutDenominator
	if readoutWidth < readoutNatural {
		readoutWidth = readoutNatural
	}
	graphCols := contentWidth - readoutWidth - 1 // -1: single gap column
	if graphCols < dashGraphMinCols {
		graphCols = dashGraphMinCols
		readoutWidth = contentWidth - graphCols - 1
		if readoutWidth < 0 {
			readoutWidth = 0
		}
	}

	valueColor := colTextMuted
	if haveData {
		valueColor = dashThresholdColor(readout.Value, readout.Ceiling)
	}
	styledReadout := make([]string, len(readoutLines))
	for i, l := range readoutLines {
		style := lipgloss.NewStyle().Foreground(colTextMuted)
		if i == 0 {
			style = lipgloss.NewStyle().Bold(true).Foreground(valueColor)
		}
		styledReadout[i] = style.Render(padCell(l, readoutWidth, true))
	}
	readoutRows := centerLinesVertically(styledReadout, innerHeight, readoutWidth)

	graphCeiling := readout.Ceiling
	if !haveData {
		graphCeiling = 0 // no ceiling basis yet; brailleGraph falls back to self-relative (still just blank for an empty slice)
	}
	graphLines := brailleGraph(history, graphCols, innerHeight, graphCeiling, dashRampColor)

	var content strings.Builder
	for i := 0; i < innerHeight; i++ {
		if i > 0 {
			content.WriteString("\n")
		}
		content.WriteString(graphLines[i])
		content.WriteString(" ")
		content.WriteString(readoutRows[i])
	}

	return titledPanelWithLabel(title, innerWidth, borderColor, rightLabel, content.String())
}

// centerLinesVertically places lines in the middle of a height-tall,
// width-wide blank block (blank rows above/below, split as evenly as
// height-len(lines) allows) — the graph panel's readout is a short block
// inside a taller panel, not top- or bottom-anchored like the apps/log
// panels.
func centerLinesVertically(lines []string, height, width int) []string {
	blank := strings.Repeat(" ", width)
	out := make([]string, height)
	for i := range out {
		out[i] = blank
	}
	top := centerOffset(height, len(lines))
	for i, l := range lines {
		if top+i >= height {
			break
		}
		out[top+i] = l
	}
	return out
}

func formatLoadReadout(value float64) string {
	return fmt.Sprintf("%.2f", value)
}

func formatIOWaitReadout(value float64) string {
	return fmt.Sprintf("%.0f%%", value)
}
