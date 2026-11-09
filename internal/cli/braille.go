package cli

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Unicode braille cells pack a 2 (dot columns) x 4 (dot rows) grid into
// one terminal character — 4x the vertical and 2x the horizontal
// resolution of a plain block character. Bit layout is the Unicode
// standard: brailleBit[subCol][subRow].
var brailleBit = [2][4]int{
	{0x01, 0x02, 0x04, 0x40}, // left column, top to bottom
	{0x08, 0x10, 0x20, 0x80}, // right column, top to bottom
}

const (
	brailleSubCols = 2
	brailleSubRows = 4
)

// rgb is a truecolor endpoint. lipgloss/termenv detect COLORTERM and
// downsample a hex lipgloss.Color to the terminal's actual palette
// (verified: with COLORTERM unset it renders as a 256-color SGR, with
// COLORTERM=truecolor as a real 24-bit SGR) — so defining hues as
// continuous RGB and interpolating gives smooth truecolor gradients for
// free, with automatic graceful degradation on lesser terminals, rather
// than a fixed set of discrete 256-indexed colour steps.
type rgb struct{ r, g, b uint8 }

func lerpU8(a, b uint8, t float64) uint8 {
	return uint8(float64(a) + t*(float64(b)-float64(a)))
}

func lerpColor(from, to rgb, t float64) lipgloss.Color {
	if t < 0 {
		t = 0
	}
	if t > 1 {
		t = 1
	}
	return lipgloss.Color(fmt.Sprintf("#%02x%02x%02x", lerpU8(from.r, to.r, t), lerpU8(from.g, to.g, t), lerpU8(from.b, to.b, t)))
}

// thresholdLevel classifies t = value/ceiling (TUI.md §5.3): green
// under ~70% of ceiling, amber approaching it, red at or over.
func thresholdLevel(t float64) int {
	switch {
	case t >= 1.0:
		return 2
	case t >= 0.70:
		return 1
	default:
		return 0
	}
}

// brailleGraph renders values (oldest first) as a filled-area braille
// graph, cols x rows terminal characters, one line per row. Right-
// aligned: the most recent value sits at the rightmost column, and if
// there are fewer values than columns, the graph is blank on the left
// rather than stretched.
//
// If ceiling > 0, fill height and colour level are absolute: t =
// value/ceiling, clamped to the graph's full height at t=1 (green under
// 70% of ceiling, amber approaching, red at/over — TUI.md §5.3).
// If ceiling <= 0 (none configured), falls back to each column's
// position relative to the whole slice's own min/max — there's no
// absolute scale to use in that case. Either way, colour
// brightness ramps from bright at the top of a column's fill (nearest
// its value) to dim at the baseline.
//
// colorFor maps a (threshold level, depth) pair to a colour — injected
// rather than hardcoded, so this rendering core stays reusable if a
// future palette or caller needs different hues than dashRampColor's
// (dashboard_colors.go).
func brailleGraph(values []float64, cols, rows int, ceiling float64, colorFor func(level int, depth float64) lipgloss.Color) []string {
	subCols := cols * brailleSubCols
	subRows := rows * brailleSubRows

	fillSubRows := make([]int, subCols)
	level := make([]int, subCols)

	if len(values) > 0 {
		start := subCols - len(values)

		if ceiling > 0 {
			for i, v := range values {
				col := start + i
				if col < 0 {
					continue
				}
				t := v / ceiling
				fillSubRows[col] = clampInt(int(t*float64(subRows)), 0, subRows)
				level[col] = thresholdLevel(t)
			}
		} else {
			lo, hi := values[0], values[0]
			for _, v := range values {
				if v < lo {
					lo = v
				}
				if v > hi {
					hi = v
				}
			}
			span := hi - lo

			for i, v := range values {
				col := start + i
				if col < 0 {
					continue // more history than fits; caller caps to subCols anyway
				}
				t := 0.5
				if span > 0 {
					t = (v - lo) / span
				}
				fillSubRows[col] = int(t * float64(subRows))
				// Deliberately a different, self-relative tier scale from
				// thresholdLevel's ceiling-based 70%/100% rule above: with
				// no ceiling, t is already normalized to this series' own
				// min-max span (0-1), so plain tertiles are the natural
				// three-way split rather than reusing thresholdLevel's
				// ceiling-relative cutoffs, which wouldn't mean anything
				// here.
				switch {
				case t >= 0.66:
					level[col] = 2
				case t >= 0.33:
					level[col] = 1
				default:
					level[col] = 0
				}
			}
		}
	}

	lines := make([]string, rows)
	for r := 0; r < rows; r++ {
		var b strings.Builder
		for c := 0; c < cols; c++ {
			col0, col1 := c*brailleSubCols, c*brailleSubCols+1
			bits := 0
			for sc, col := range [brailleSubCols]int{col0, col1} {
				h := fillSubRows[col]
				for sr := 0; sr < brailleSubRows; sr++ {
					subRow := r*brailleSubRows + sr
					rowFromBottom := subRows - 1 - subRow
					if rowFromBottom < h {
						bits |= brailleBit[sc][sr]
					}
				}
			}

			if bits == 0 {
				b.WriteByte(' ')
				continue
			}

			ch := rune(0x2800 + bits)
			h := maxInt(fillSubRows[col0], fillSubRows[col1])
			lvl := maxInt(level[col0], level[col1])
			depth := cellDepth(r, h, subRows)
			b.WriteString(lipgloss.NewStyle().Foreground(colorFor(lvl, depth)).Render(string(ch)))
		}
		lines[r] = b.String()
	}
	return lines
}

// cellDepth is how far terminal row r is below the top of a column's
// fill (height h sub-rows, in a graph subRows tall), as a fraction: 0 at
// the top of the fill (brightest), 1 at the baseline (dimmest).
func cellDepth(r, h, subRows int) float64 {
	if h <= 0 {
		return 1
	}
	topSubRow := subRows - h
	topRow := topSubRow / brailleSubRows
	totalRows := (h + brailleSubRows - 1) / brailleSubRows // ceil(h / brailleSubRows): terminal rows spanned by the fill
	if totalRows <= 1 {
		return 0
	}
	return float64(r-topRow) / float64(totalRows-1)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
