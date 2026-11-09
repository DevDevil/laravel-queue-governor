package cli

import (
	"strings"
	"testing"
)

func TestCenterLinesVertically_PlacesLinesInTheMiddle(t *testing.T) {
	out := centerLinesVertically([]string{"x", "y"}, 6, 3)
	if len(out) != 6 {
		t.Fatalf("len(out) = %d, want 6", len(out))
	}
	nonBlank := []int{}
	for i, l := range out {
		if strings.TrimSpace(l) != "" {
			nonBlank = append(nonBlank, i)
		}
	}
	if len(nonBlank) != 2 || nonBlank[0] != 2 || nonBlank[1] != 3 {
		t.Errorf("non-blank rows = %v, want [2 3] (centered in a 6-row block)", nonBlank)
	}
}

func TestCenterLinesVertically_MoreLinesThanHeightStillReturnsHeightRows(t *testing.T) {
	out := centerLinesVertically([]string{"a", "b", "c"}, 2, 3)
	if len(out) != 2 {
		t.Errorf("len(out) = %d, want 2 (clamped to height even though more lines were given)", len(out))
	}
}

func TestBuildGraphBox_TopLineWidthMatchesOuterWidth(t *testing.T) {
	history := []float64{1, 2, 3, 4, 5}
	for _, w := range []int{20, 40, 80} {
		out := buildGraphBox("load", history, graphReadout{Value: 3.0, Ceiling: 6.0, ValueText: "3.00", CeilingText: "6.00", ExtraText: "0.50/core"}, true, colLoadBorder, "5m", w, 9)
		lines := strings.Split(out, "\n")
		want := len([]rune(stripANSI(lines[1])))
		for i, line := range lines {
			if got := len([]rune(stripANSI(line))); got != want {
				t.Errorf("width=%d: line[%d] visible width = %d, want %d", w, i, got, want)
			}
		}
	}
}

// TestBuildGraphBox_DenseHistoryDoesNotOverflowOuterHeight guards against
// a real bug found while building this panel: braille content is dozens
// of individually ANSI-styled characters per line, and lipgloss's
// Width()+Padding(0,1) reserves 2 columns for that padding *inside* the
// given width — content sized to the full width (not width-2) silently
// overflowed by exactly 2 columns, which lipgloss's wrap-on-overflow
// logic turned into many extra lines for this dense a line (rather than
// clipping 2 trailing columns, which is what happens for lower-density
// content like the apps table). A varied, non-flat history maximizes the
// number of distinct threshold/depth colour codes on one line, so this
// reproduces the failure mode a flat or empty history wouldn't.
func TestBuildGraphBox_DenseHistoryDoesNotOverflowOuterHeight(t *testing.T) {
	values := make([]float64, 300)
	for i := range values {
		values[i] = float64(i%10) + 0.5
	}
	for _, w := range []int{40, 58, 60, 80, 130} {
		out := buildGraphBox("load", values, graphReadout{Value: 5.5, Ceiling: 10, ValueText: "5.50", CeilingText: "10.00", ExtraText: "1.38/core"}, true, colLoadBorder, "5m", w, 9)
		if got := len(strings.Split(out, "\n")); got != 9 {
			t.Errorf("outerWidth=%d: buildGraphBox produced %d lines, want exactly 9 (outerHeight)", w, got)
		}
	}
}

func TestBuildGraphBox_ContainsTitleLabelAndReadout(t *testing.T) {
	out := stripANSI(buildGraphBox("load", nil, graphReadout{Value: 3.0, Ceiling: 6.0, ValueText: "3.00", CeilingText: "6.00", ExtraText: "0.50/core"}, true, colLoadBorder, "5m", 60, 9))
	if !containsAll(out, "load", "5m", "3.00", "6.00", "0.50/core") {
		t.Errorf("buildGraphBox output missing expected content: %q", out)
	}
}

func TestBuildGraphBox_NoDataShowsMutedPlaceholderNotZero(t *testing.T) {
	out := stripANSI(buildGraphBox("load", nil, graphReadout{ValueText: "—", CeilingText: "—"}, false, colLoadBorder, "5m", 60, 9))
	if !strings.Contains(out, "—") {
		t.Errorf("expected the em-dash placeholder before any data arrives, got %q", out)
	}
	if strings.Contains(out, "0.00") {
		t.Errorf("must not show a fabricated 0.00 reading before real data arrives: %q", out)
	}
}

func TestDashRampColor_LevelSelectsDashPalette(t *testing.T) {
	if got, want := dashRampColor(0, 0), lerpColor(dashGreenBright, dashGreenDim, 0); got != want {
		t.Errorf("level 0 = %v, want green bright endpoint %v", got, want)
	}
	if got, want := dashRampColor(1, 0), lerpColor(dashAmberBright, dashAmberDim, 0); got != want {
		t.Errorf("level 1 = %v, want amber bright endpoint %v", got, want)
	}
	if got, want := dashRampColor(2, 0), lerpColor(dashRedBright, dashRedDim, 0); got != want {
		t.Errorf("level 2 = %v, want red bright endpoint %v", got, want)
	}
}

func TestFormatLoadReadout_TwoDecimals(t *testing.T) {
	if got, want := formatLoadReadout(3.8), "3.80"; got != want {
		t.Errorf("formatLoadReadout(3.8) = %q, want %q", got, want)
	}
}

func TestFormatIOWaitReadout_IntegerPercent(t *testing.T) {
	if got, want := formatIOWaitReadout(12.4), "12%"; got != want {
		t.Errorf("formatIOWaitReadout(12.4) = %q, want %q", got, want)
	}
}
