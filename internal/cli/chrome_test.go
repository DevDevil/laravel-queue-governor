package cli

import (
	"strings"
	"testing"
)

func TestTitledPanel_TopLineWidthMatchesBody(t *testing.T) {
	for _, w := range []int{10, 46, 60} {
		out := titledPanel("load", w, colTextMuted, "hello\nworld")
		lines := strings.Split(out, "\n")
		if len(lines) != 4 {
			t.Fatalf("width=%d: got %d lines, want 4 (top, 2 content, bottom)", w, len(lines))
		}
		want := len([]rune(stripANSI(lines[1])))
		for i, line := range lines {
			if got := len([]rune(stripANSI(line))); got != want {
				t.Errorf("width=%d: line[%d] visible width = %d, want %d (must match every other line)", w, i, got, want)
			}
		}
	}
}

func TestTitledPanel_TitleAppearsInTopBorder(t *testing.T) {
	out := titledPanel("saas_seed_spa", 60, colTextMuted, "content")
	top := strings.Split(out, "\n")[0]
	if !strings.Contains(stripANSI(top), "saas_seed_spa") {
		t.Errorf("top border = %q, want it to contain the title", stripANSI(top))
	}
	body := strings.Join(strings.Split(out, "\n")[1:], "\n")
	if strings.Contains(stripANSI(body), "saas_seed_spa") {
		t.Errorf("title leaked into the body content: %q", stripANSI(body))
	}
}

func TestTitledPanel_VeryLongTitleDoesNotBreakAlignment(t *testing.T) {
	out := titledPanel("a-title-way-longer-than-the-panel-itself", 10, colTextMuted, "x")
	lines := strings.Split(out, "\n")
	want := len([]rune(stripANSI(lines[1])))
	for i, line := range lines {
		if got := len([]rune(stripANSI(line))); got != want {
			t.Errorf("line[%d] visible width = %d, want %d", i, got, want)
		}
	}
}

// TestTitledPanel_LongTitleTruncatesWithEllipsis is TUI.md §14: truncate
// with "…", never let content just disappear. titledPanel used to
// hard-cut the title with no ellipsis at all — inconsistent with
// padCell/titledPanelWithLabel, which both did add one.
func TestTitledPanel_LongTitleTruncatesWithEllipsis(t *testing.T) {
	out := titledPanel("a-title-way-longer-than-the-panel-itself", 10, colTextMuted, "x")
	top := stripANSI(strings.Split(out, "\n")[0])
	if !strings.Contains(top, "…") {
		t.Errorf("top border = %q, want a truncated (…) title fragment", top)
	}
}
