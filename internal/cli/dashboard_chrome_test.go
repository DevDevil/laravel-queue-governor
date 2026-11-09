package cli

import (
	"strings"
	"testing"
)

func TestTitledPanelWithLabel_TopLineContainsTitleAndLabel(t *testing.T) {
	out := titledPanelWithLabel("apps", 60, colAppsBorder, "running 2/2", "content")
	top := strings.Split(out, "\n")[0]
	plain := stripANSI(top)
	if !strings.Contains(plain, "apps") || !strings.Contains(plain, "running 2/2") {
		t.Errorf("top border = %q, want it to contain both the title and the label", plain)
	}
}

func TestTitledPanelWithLabel_WidthMatchesBodyAcrossSizes(t *testing.T) {
	for _, w := range []int{20, 60, 100} {
		out := titledPanelWithLabel("apps", w, colAppsBorder, "running 2/2", "hello\nworld")
		lines := strings.Split(out, "\n")
		want := len([]rune(stripANSI(lines[1])))
		for i, line := range lines {
			if got := len([]rune(stripANSI(line))); got != want {
				t.Errorf("width=%d: line[%d] visible width = %d, want %d", w, i, got, want)
			}
		}
	}
}

func TestTitledPanelWithLabel_ExtremeLabelNeverBreaksBoxAlignment(t *testing.T) {
	// A label far too long for a narrow panel must not corrupt the box,
	// no matter how it degrades (truncate or drop) — same width-matching
	// guarantee as above.
	out := titledPanelWithLabel("apps", 10, colAppsBorder, "a very very long label that cannot possibly fit", "x")
	lines := strings.Split(out, "\n")
	want := len([]rune(stripANSI(lines[1])))
	for i, line := range lines {
		if got := len([]rune(stripANSI(line))); got != want {
			t.Errorf("line[%d] visible width = %d, want %d", i, got, want)
		}
	}
}

func TestTitledPanelWithLabel_TruncatesLabelWithEllipsisRatherThanDropping(t *testing.T) {
	// TUI.md §14: truncate with "…", never let content just vanish. A
	// label that's moderately too long (not "far too long") should still
	// show a truncated fragment of it, not disappear entirely.
	out := titledPanelWithLabel("apps", 20, colAppsBorder, "running 12345/67890", "content")
	top := stripANSI(strings.Split(out, "\n")[0])
	if !strings.Contains(top, "…") {
		t.Errorf("top border = %q, want a truncated (…) label fragment, not a dropped one", top)
	}
	if strings.Contains(top, "running 12345/67890") {
		t.Errorf("top border = %q, want the full label truncated, not shown in full (should not fit)", top)
	}
}
