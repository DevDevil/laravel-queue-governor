package cli

import (
	"strings"
	"testing"
)

func TestBrailleGraph_EmptyValuesRendersBlank(t *testing.T) {
	lines := brailleGraph(nil, 4, 2, 0, dashRampColor)
	if len(lines) != 2 {
		t.Fatalf("len(lines) = %d, want 2", len(lines))
	}
	for _, line := range lines {
		if visible := stripANSI(line); strings.TrimSpace(visible) != "" {
			t.Errorf("line = %q, want all spaces (no data)", visible)
		}
	}
}

func TestBrailleGraph_LineAndColumnCounts(t *testing.T) {
	values := make([]float64, 20)
	for i := range values {
		values[i] = float64(i)
	}

	cols, rows := 6, 3
	lines := brailleGraph(values, cols, rows, 0, dashRampColor)
	if len(lines) != rows {
		t.Fatalf("len(lines) = %d, want %d rows", len(lines), rows)
	}
	for i, line := range lines {
		visible := []rune(stripANSI(line))
		if len(visible) != cols {
			t.Errorf("line[%d] has %d visible chars, want %d columns", i, len(visible), cols)
		}
	}
}

// TestBrailleGraph_ConstantValueFillsToMidHeight verifies the
// self-relative fallback's "no data range" case (ceiling<=0, span==0): a
// flat input should render as a fill at the midpoint of the graph's
// height, not empty or full — a value with no variation is neither
// "high" nor "low" relative to itself.
func TestBrailleGraph_ConstantValueFillsToMidHeight(t *testing.T) {
	values := make([]float64, 8)
	for i := range values {
		values[i] = 5.0
	}

	rows := 4
	lines := brailleGraph(values, 4, rows, 0, dashRampColor)

	// Bottom half of the graph should have dots (filled to ~mid-height);
	// the very top row should be blank, since a flat value shouldn't
	// reach the top.
	bottomRow := stripANSI(lines[rows-1])
	if strings.TrimSpace(bottomRow) == "" {
		t.Error("bottom row should have dots for a constant (mid-height) fill")
	}
	topRow := stripANSI(lines[0])
	if strings.TrimSpace(topRow) != "" {
		t.Errorf("top row should be blank for a constant value at mid-height, got %q", topRow)
	}
}

func TestBrailleGraph_HighValueReachesTopRow(t *testing.T) {
	// A clear ramp from low to high: the last (highest, most recent)
	// value should reach near the top of the graph on the right edge.
	values := []float64{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	rows := 4
	lines := brailleGraph(values, len(values), rows, 0, dashRampColor)

	topRow := stripANSI(lines[0])
	visible := []rune(topRow)
	last := visible[len(visible)-1]
	if last == ' ' {
		t.Errorf("top row's last column should have dots for the highest (most recent) value, got blank: %q", topRow)
	}
}

func TestBrailleGraph_RightAlignsWhenFewerValuesThanColumns(t *testing.T) {
	values := []float64{5, 5} // 2 values, plenty of columns
	cols, rows := 10, 2
	lines := brailleGraph(values, cols, rows, 0, dashRampColor)

	// The leftmost columns (no data there) must be blank.
	bottomRow := []rune(stripANSI(lines[rows-1]))
	if bottomRow[0] != ' ' {
		t.Errorf("leftmost column should be blank when there are far fewer values than columns, got %q", string(bottomRow))
	}
}

func TestBrailleGraph_CeilingScalesAbsolutely(t *testing.T) {
	// With a ceiling, fill height is value/ceiling — NOT relative to the
	// slice's own min/max. A value at exactly half the ceiling should
	// fill roughly half the graph, even though every value here is
	// identical (which would be mid-height under the self-relative
	// fallback too, so this alone doesn't distinguish the two — the real
	// point is the *level*, checked separately below).
	rows := 4
	values := []float64{3, 3, 3, 3} // half of a ceiling of 6
	lines := brailleGraph(values, 4, rows, 6, dashRampColor)

	topRow := stripANSI(lines[0])
	if strings.TrimSpace(topRow) != "" {
		t.Errorf("a value at 50%% of ceiling should not reach the top row, got %q", topRow)
	}
	bottomRow := stripANSI(lines[rows-1])
	if strings.TrimSpace(bottomRow) == "" {
		t.Error("a value at 50%% of ceiling should still fill the bottom row")
	}
}

func TestBrailleGraph_CeilingClampsAtFullHeightWhenOverCeiling(t *testing.T) {
	rows := 4
	values := []float64{100} // wildly over a ceiling of 6 — must clamp, not overflow/panic
	lines := brailleGraph(values, 1, rows, 6, dashRampColor)

	topRow := stripANSI(lines[0])
	if strings.TrimSpace(topRow) == "" {
		t.Error("a value far over ceiling should fill all the way to the top row (clamped), not be blank")
	}
}

func TestThresholdLevel_Breakpoints(t *testing.T) {
	cases := []struct {
		t    float64
		want int
	}{
		{0, 0},
		{0.5, 0},
		{0.69, 0},
		{0.70, 1},
		{0.85, 1},
		{0.99, 1},
		{1.0, 2},
		{1.5, 2},
	}
	for _, c := range cases {
		if got := thresholdLevel(c.t); got != c.want {
			t.Errorf("thresholdLevel(%v) = %d, want %d", c.t, got, c.want)
		}
	}
}

func TestCellDepth_TopOfFillIsZero(t *testing.T) {
	// subRows=16 (4 terminal rows), fill height h=16 (fills the whole
	// graph) -> row 0 is the top of the fill -> depth 0.
	if got := cellDepth(0, 16, 16); got != 0 {
		t.Errorf("cellDepth(0, 16, 16) = %v, want 0", got)
	}
}

func TestCellDepth_NoFillIsMax(t *testing.T) {
	if got := cellDepth(0, 0, 16); got != 1 {
		t.Errorf("cellDepth(0, 0, 16) = %v, want 1 (no fill -> dimmest)", got)
	}
}

func TestCellDepth_BaselineRowIsMax(t *testing.T) {
	// A fill that only reaches partway up: the bottom row (baseline)
	// should be depth 1 (dimmest), regardless of how tall the fill is.
	got := cellDepth(3, 8, 16) // 4 rows total (rows 0..3), fill height 8 (half)
	if got != 1 {
		t.Errorf("cellDepth(bottom row, partial fill) = %v, want 1", got)
	}
}
