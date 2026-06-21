package ui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// sgrReset is the ANSI "reset all attributes" sequence. It is inserted at every
// seam where one styled segment abuts another so a colour/attribute opened in
// the background line can never bleed into the overlaid box (or vice-versa).
const sgrReset = "\x1b[0m"

// placeOverlay composites the foreground string fg on top of the background
// string bg, with fg's top-left corner at cell (x, y). It is the true-overlay
// primitive the native engine needs for the /log-task popover: unlike
// lipgloss.Place — which renders onto a fresh canvas and therefore *hides*
// whatever was underneath — placeOverlay overwrites only the cells fg covers and
// preserves every bg cell outside the box, so the spawned panes remain visible
// around the popover.
//
// It is ANSI-SGR and rune-width aware (via charmbracelet/x/ansi): SGR styling in
// bg outside the box survives, and wide runes (CJK / emoji) straddling a box edge
// are cut on a cell boundary rather than split mid-cell. A negative x/y is
// clamped to 0; a box that extends past the bottom of bg extends bg with blank
// lines rather than panicking.
func placeOverlay(x, y int, fg, bg string) string {
	if fg == "" {
		return bg
	}
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}

	fgLines := strings.Split(fg, "\n")
	bgLines := strings.Split(bg, "\n")

	// Extend bg with blank lines if the box reaches past its last line so a
	// popover near the bottom edge still draws fully.
	for len(bgLines) < y+len(fgLines) {
		bgLines = append(bgLines, "")
	}

	for i, fgLine := range fgLines {
		row := y + i
		bgLines[row] = overlayLine(x, fgLine, bgLines[row])
	}

	return strings.Join(bgLines, "\n")
}

// overlayLine overwrites a single background line with fgLine starting at column
// x, preserving the bg cells to the left of x and to the right of the box. The
// three segments are separated by SGR resets so neither side's styling leaks
// across the box.
func overlayLine(x int, fgLine, bgLine string) string {
	fgWidth := ansi.StringWidth(fgLine)

	// Left segment: bg columns [0, x). ansi.Cut keeps the SGR context; if the bg
	// line is shorter than x we pad with (unstyled) spaces out to x so the box
	// lands at the right column.
	left := ansi.Cut(bgLine, 0, x)
	if pad := x - ansi.StringWidth(left); pad > 0 {
		left += sgrReset + strings.Repeat(" ", pad)
	}

	// Right segment: bg columns [x+fgWidth, end). ansi.Cut replays the bg SGR
	// state up to the cut, so the tail keeps its original colours.
	var right string
	if bgWidth := ansi.StringWidth(bgLine); bgWidth > x+fgWidth {
		right = sgrReset + ansi.Cut(bgLine, x+fgWidth, bgWidth)
	}

	return left + sgrReset + fgLine + sgrReset + right
}

// centerOverlay composites over on top of base, centered within a w×h canvas. It
// computes the top-left corner from over's rendered dimensions and delegates to
// placeOverlay. Offsets are clamped to non-negative so an over larger than the
// canvas still draws from the top-left rather than off-screen.
func centerOverlay(over, base string, w, h int) string {
	ow, oh := overlayDims(over)
	x := (w - ow) / 2
	if x < 0 {
		x = 0
	}
	y := (h - oh) / 2
	if y < 0 {
		y = 0
	}
	return placeOverlay(x, y, over, base)
}

// overlayDims returns the cell width (widest line) and height (line count) of a
// rendered string.
func overlayDims(s string) (w, h int) {
	lines := strings.Split(s, "\n")
	for _, ln := range lines {
		if lw := ansi.StringWidth(ln); lw > w {
			w = lw
		}
	}
	return w, len(lines)
}
