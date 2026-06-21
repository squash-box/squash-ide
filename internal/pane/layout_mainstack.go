package pane

import "fmt"

// MainStack lays panes out as one full-height "main" column on the left and the
// remaining n-1 panes stacked as rows in a right-hand column of equal width. It
// is the Responsive auto-mode's intermediate step between full columns and a
// pure vertical stack: when the region can fit two columns at the preferred
// width but not n of them, it keeps one pane prominent and full height while the
// rest share the second column, instead of collapsing everything to full-width
// rows. Modelled on tmux's main-vertical layout.
//
// The width split mirrors FlexColumns (equal columns, remainder to the left) and
// the right column's row split mirrors StackRows (equal rows, remainder to the
// top), so the geometry and gutter accounting match the strategies it composes
// from. It is selected only by Responsive; a pinned layout never resolves to it.
type MainStack struct{}

// axis reports that the Manager composes MainStack as a main box beside a
// stacked column.
func (MainStack) axis() layoutAxis { return axisMainStack }

// Arrange implements Strategy. It requires n >= 2 (one main pane plus at least
// one stacked pane) and rejects (wrapping ErrInsufficientSpace) when the region
// is too narrow for two columns at MinWidth or too short to stack the n-1 side
// panes at MinHeight — the same reject-rather-than-squeeze contract as
// FlexColumns and StackRows.
func (MainStack) Arrange(region Rect, n int, c Constraints) ([]Rect, error) {
	if n < 2 {
		return nil, fmt.Errorf("pane.MainStack: n must be >= 2, got %d", n)
	}
	if region.W < 1 || region.H < 1 {
		return nil, fmt.Errorf("pane.MainStack: region must be non-empty, got %dx%d", region.W, region.H)
	}
	if c.MinWidth < 1 {
		return nil, fmt.Errorf("pane.MainStack: MinWidth must be >= 1, got %d", c.MinWidth)
	}
	if c.MinHeight < 1 {
		return nil, fmt.Errorf("pane.MainStack: MinHeight must be >= 1, got %d", c.MinHeight)
	}
	gutter := c.Gutter
	if gutter < 0 {
		return nil, fmt.Errorf("pane.MainStack: Gutter must be >= 0, got %d", gutter)
	}

	// Two columns, each with a leading gutter — the dual of FlexColumns' n=2
	// case: a leading separator before the main column and before the side.
	availW := region.W - 2*gutter
	if availW < 2*c.MinWidth {
		return nil, fmt.Errorf(
			"%w: region width=%d gutter=%d cols=2 min=%d (would give %d cols/pane)",
			ErrInsufficientSpace, region.W, gutter, c.MinWidth, perPaneSafe(availW, 2),
		)
	}
	sideW := availW / 2
	mainW := availW - sideW // remainder to the main (left) column

	// The side column stacks k = n-1 panes, one leading gutter row before each —
	// the StackRows split over the full region height.
	k := n - 1
	availH := region.H - k*gutter
	if availH < k*c.MinHeight {
		return nil, fmt.Errorf(
			"%w: region height=%d gutter=%d panes=%d min=%d (would give %d rows/pane)",
			ErrInsufficientSpace, region.H, gutter, k, c.MinHeight, perPaneSafe(availH, k),
		)
	}
	per := availH / k
	heights := make([]int, k)
	for i := range heights {
		heights[i] = per
	}
	// Leftover rows go one-per-pane to the topmost side panes, mirroring
	// StackRows' remainder-to-top loop.
	remainder := availH - per*k
	for i := 0; i < remainder; i++ {
		heights[i]++
	}

	// rects[0] is the main column; rects[1..] follow the panes order so the
	// Manager's positional hit-test and composition stay index-aligned.
	rects := make([]Rect, n)
	rects[0] = Rect{X: region.X + gutter, Y: region.Y, W: mainW, H: region.H}
	sideX := region.X + gutter + mainW + gutter
	y := region.Y
	for i, h := range heights {
		y += gutter // leading separator row before this side pane
		rects[i+1] = Rect{X: sideX, Y: y, W: sideW, H: h}
		y += h
	}
	return rects, nil
}
