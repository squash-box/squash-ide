package ui

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
)

// stripLines returns the visible (SGR-stripped) content of each line of s.
func stripLines(s string) []string {
	lines := strings.Split(s, "\n")
	out := make([]string, len(lines))
	for i, ln := range lines {
		out[i] = ansi.Strip(ln)
	}
	return out
}

// Happy path: the box lands at the requested offset and every bg cell outside it
// is preserved verbatim.
func TestPlaceOverlay_DrawsBoxLeavesBaseUntouched(t *testing.T) {
	bg := strings.Join([]string{
		"..........",
		"..........",
		"..........",
	}, "\n")
	fg := "XX"

	got := stripLines(placeOverlay(2, 1, fg, bg))
	want := []string{
		"..........",
		"..XX......",
		"..........",
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// A multi-line box overwrites a rectangle, leaving the surrounding frame intact.
func TestPlaceOverlay_MultiLineBox(t *testing.T) {
	bg := strings.Join([]string{
		"##########",
		"##########",
		"##########",
		"##########",
	}, "\n")
	fg := strings.Join([]string{"ABC", "DEF"}, "\n")

	got := stripLines(placeOverlay(3, 1, fg, bg))
	want := []string{
		"##########",
		"###ABC####",
		"###DEF####",
		"##########",
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// SGR styling in the base survives outside the box: the visible content is
// composited correctly and the truecolor escape is still present in the output.
func TestPlaceOverlay_PreservesBaseSGR(t *testing.T) {
	red := "\x1b[38;2;255;0;0m"
	bg := red + "RRRRRRRRRR" + sgrReset
	fg := "XX"

	out := placeOverlay(4, 0, fg, bg)

	if got := ansi.Strip(out); got != "RRRRXXRRRR" {
		t.Errorf("stripped overlay = %q, want %q", got, "RRRRXXRRRR")
	}
	if !strings.Contains(out, "38;2;255;0;0") {
		t.Errorf("base truecolor SGR did not survive the overlay: %q", out)
	}
}

// Wide runes (CJK) cut on a cell boundary: a box exactly covering one wide rune
// replaces it and leaves the neighbouring wide rune intact — no half-cell.
func TestPlaceOverlay_WideRunesCellBoundary(t *testing.T) {
	bg := "ab日本cd" // a,b (1 each), 日,本 (2 each), c,d (1 each) = 8 cells
	fg := "[]"     // 2 cells, placed over 日 (cols 2-3)

	out := placeOverlay(2, 0, fg, bg)
	if got := ansi.Strip(out); got != "ab[]本cd" {
		t.Errorf("stripped overlay = %q, want %q", got, "ab[]本cd")
	}
}

// Wide rune straddling the box's right edge: no broken UTF-8 / half-cell — the
// straddled rune is preserved whole rather than split.
func TestPlaceOverlay_WideRuneStraddleNoCorruption(t *testing.T) {
	bg := "ab日本cd"
	fg := "###" // 3 cells at x=2 → right edge falls mid-本

	out := placeOverlay(2, 0, fg, bg)
	if !utf8.ValidString(out) {
		t.Fatalf("overlay produced invalid UTF-8: %q", out)
	}
	stripped := ansi.Strip(out)
	if !strings.Contains(stripped, "###") {
		t.Errorf("box not drawn: %q", stripped)
	}
	// The fully-outside runes survive; the straddled rune is not split into a
	// replacement char.
	for _, want := range []string{"ab", "本", "cd"} {
		if !strings.Contains(stripped, want) {
			t.Errorf("expected %q preserved in %q", want, stripped)
		}
	}
}

// A short base line is padded out so the box still lands at the requested column.
func TestPlaceOverlay_PadsShortBaseLine(t *testing.T) {
	bg := "abc" // only 3 cells wide
	fg := "XX"

	got := stripLines(placeOverlay(5, 0, fg, bg))
	if got[0] != "abc  XX" {
		t.Errorf("padded overlay = %q, want %q", got[0], "abc  XX")
	}
}

// A box taller than the base extends the base with blank lines rather than
// panicking, and a negative offset clamps to 0.
func TestPlaceOverlay_ExtendsAndClamps(t *testing.T) {
	bg := "row0"
	fg := strings.Join([]string{"AA", "BB"}, "\n")

	out := placeOverlay(-3, -1, fg, bg) // negatives clamp to (0,0)
	got := stripLines(out)
	// Row 0: box cols 0-1 become "AA", cols 2.. keep "w0". Row 1: base is
	// extended with a blank line that the box's second row "BB" lands on.
	want := []string{"AAw0", "BB"}
	if len(got) != len(want) {
		t.Fatalf("got %d lines %v, want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// centerOverlay positions the box in the middle of the canvas.
func TestCenterOverlay_Centers(t *testing.T) {
	// 9x5 canvas of dots, a 3x1 box → centered at x=(9-3)/2=3, y=(5-1)/2=2.
	rows := make([]string, 5)
	for i := range rows {
		rows[i] = strings.Repeat(".", 9)
	}
	bg := strings.Join(rows, "\n")
	fg := "BOX"

	got := stripLines(centerOverlay(fg, bg, 9, 5))
	if got[2] != "...BOX..." {
		t.Errorf("centered line = %q, want %q", got[2], "...BOX...")
	}
	for _, i := range []int{0, 1, 3, 4} {
		if got[i] != strings.Repeat(".", 9) {
			t.Errorf("line %d should be untouched, got %q", i, got[i])
		}
	}
}

// An empty foreground returns the background unchanged.
func TestPlaceOverlay_EmptyFG(t *testing.T) {
	bg := "unchanged\nlines"
	if out := placeOverlay(2, 0, "", bg); out != bg {
		t.Errorf("empty fg should return bg unchanged, got %q", out)
	}
}
