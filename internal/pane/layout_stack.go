package pane

import "fmt"

// StackRows lays panes out as rows stacked top to bottom, each spanning the full
// region width, distributing any remainder one row at a time to the topmost
// panes. It is the row-wise dual of FlexColumns: where FlexColumns splits width
// into equal columns and rejects below MinWidth, StackRows splits height into
// equal rows and rejects below MinHeight. One gutter row per pane (a leading
// blank row before each) mirrors FlexColumns' one-gutter-column-per-pane model,
// so the Manager composes the two the same way (a leading separator before each
// box) — just along the other axis.
//
// It is the strategy the Responsive auto-mode (T-040) falls back to when a
// narrow terminal can no longer fit equal columns at MinWidth but is still tall
// enough to stack, so a spawn reflows instead of being rejected.
type StackRows struct{}

// axis reports that the Manager composes StackRows boxes vertically.
func (StackRows) axis() layoutAxis { return axisRows }

// Arrange implements Strategy. It mirrors FlexColumns.Arrange with the width and
// height roles swapped: the split is over region.H at MinHeight, and every pane
// spans the full region width.
func (StackRows) Arrange(region Rect, n int, c Constraints) ([]Rect, error) {
	if n < 1 {
		return nil, fmt.Errorf("pane.StackRows: n must be >= 1, got %d", n)
	}
	if region.W < 1 || region.H < 1 {
		return nil, fmt.Errorf("pane.StackRows: region must be non-empty, got %dx%d", region.W, region.H)
	}
	minH := c.MinHeight
	if minH < 1 {
		return nil, fmt.Errorf("pane.StackRows: MinHeight must be >= 1, got %d", minH)
	}
	gutter := c.Gutter
	if gutter < 0 {
		return nil, fmt.Errorf("pane.StackRows: Gutter must be >= 0, got %d", gutter)
	}

	// One gutter per pane (a leading separator row before each), the dual of
	// FlexColumns' n-gutters model.
	avail := region.H - n*gutter
	if avail < n*minH {
		return nil, fmt.Errorf(
			"%w: region height=%d gutter=%d panes=%d min=%d (would give %d rows/pane)",
			ErrInsufficientSpace, region.H, gutter, n, minH, perPaneSafe(avail, n),
		)
	}

	per := avail / n
	heights := make([]int, n)
	for i := range heights {
		heights[i] = per
	}
	// Leftover rows go one-per-pane to the topmost panes — the dual of
	// FlexColumns' remainder-to-leftmost loop.
	remainder := avail - per*n
	for i := 0; i < remainder; i++ {
		heights[i]++
	}

	rects := make([]Rect, n)
	y := region.Y
	for i, h := range heights {
		y += gutter // leading separator before this pane
		rects[i] = Rect{X: region.X, Y: y, W: region.W, H: h}
		y += h
	}
	return rects, nil
}
