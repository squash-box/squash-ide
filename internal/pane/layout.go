package pane

import (
	"errors"
	"fmt"
)

// Rect is an axis-aligned region of the terminal grid, in cells. X/Y are the
// top-left origin and W/H the size, all measured in terminal columns/rows. It
// is the full bounding box a pane occupies, border included.
type Rect struct {
	X, Y, W, H int
}

// Constraints bound how a Strategy may arrange panes within a region.
//
//   - MinWidth is the floor on a single pane's bounding-box width. A layout
//     that would put any pane below it is rejected rather than silently
//     squeezed — the same contract as tmux.Tile, whose caller refuses the
//     spawn.
//   - MinHeight is the floor on a single pane's bounding-box height. It is the
//     row-wise dual of MinWidth: StackRows (T-040) rejects a stack that would
//     put any pane below it, the same way FlexColumns rejects on MinWidth.
//     FlexColumns ignores it (columns always span the full region height).
//   - Gutter is the number of blank columns reserved per pane for separation
//     between adjacent panes (and a leading column before the first). One
//     gutter column per pane mirrors tmux's one-border-per-pane model, which is
//     what makes FlexColumns reproduce tmux.Tile's column math exactly.
//     StackRows reuses it as a row gutter (one leading blank row per pane).
type Constraints struct {
	MinWidth  int
	MinHeight int
	Gutter    int
}

// ErrInsufficientSpace is returned (wrapped) by a Strategy when the region is
// too narrow to fit the requested panes at the configured MinWidth. Callers
// match it with errors.Is to distinguish a layout rejection (refuse the spawn)
// from a programming error (bad n / bad region).
var ErrInsufficientSpace = errors.New("not enough space for another pane")

// Strategy computes per-pane geometry from an available region. It is a pure
// function behind an interface so future layouts (stacking, tabs, collapse —
// T-040) drop in without touching the Manager (Open/Closed). This mirrors the
// pure-Tile-function precedent in internal/tmux/layout.go.
type Strategy interface {
	// Arrange splits region into n pane rects under the given constraints, or
	// returns an error (wrapping ErrInsufficientSpace) if they cannot fit.
	Arrange(region Rect, n int, c Constraints) ([]Rect, error)
}

// FlexColumns lays panes out as equal-width columns filling the region left to
// right, distributing any remainder one column at a time to the leftmost panes.
// It is a 2-D port of internal/tmux/layout.go's Tile: every pane spans the full
// region height, and the column-width math (equal split, remainder-to-leftmost,
// reject-below-min) is identical. Given a region of width totalCols-tuiWidth and
// Gutter 1, FlexColumns produces exactly the widths Tile returns for the same
// (totalCols, tuiWidth, n, minWidth) — the parity the layout tests assert.
type FlexColumns struct{}

// Arrange implements Strategy.
func (FlexColumns) Arrange(region Rect, n int, c Constraints) ([]Rect, error) {
	if n < 1 {
		return nil, fmt.Errorf("pane.FlexColumns: n must be >= 1, got %d", n)
	}
	if region.W < 1 || region.H < 1 {
		return nil, fmt.Errorf("pane.FlexColumns: region must be non-empty, got %dx%d", region.W, region.H)
	}
	if c.MinWidth < 1 {
		return nil, fmt.Errorf("pane.FlexColumns: MinWidth must be >= 1, got %d", c.MinWidth)
	}
	gutter := c.Gutter
	if gutter < 0 {
		return nil, fmt.Errorf("pane.FlexColumns: Gutter must be >= 0, got %d", gutter)
	}

	// One gutter per pane (a leading separator column before each), matching
	// tmux's n-borders-for-n-panes model so the split below mirrors Tile.
	avail := region.W - n*gutter
	if avail < n*c.MinWidth {
		// Surface the numbers so the caller can explain the rejection, the way
		// tmux.Tile's message does.
		return nil, fmt.Errorf(
			"%w: region width=%d gutter=%d panes=%d min=%d (would give %d cols/pane)",
			ErrInsufficientSpace, region.W, gutter, n, c.MinWidth, perPaneSafe(avail, n),
		)
	}

	per := avail / n
	widths := make([]int, n)
	for i := range widths {
		widths[i] = per
	}
	// Leftover columns go one-per-pane to the leftmost panes, keeping widths
	// within 1 column of each other — identical to Tile's remainder loop.
	remainder := avail - per*n
	for i := 0; i < remainder; i++ {
		widths[i]++
	}

	rects := make([]Rect, n)
	x := region.X
	for i, w := range widths {
		x += gutter // leading separator before this pane
		rects[i] = Rect{X: x, Y: region.Y, W: w, H: region.H}
		x += w
	}
	return rects, nil
}

// perPaneSafe guards the rejection message against divide-by-zero and reports 0
// rather than a confusing negative when avail is itself non-positive — the same
// helper shape as tmux.perPaneSafe.
func perPaneSafe(avail, n int) int {
	if n <= 0 || avail <= 0 {
		return 0
	}
	return avail / n
}
