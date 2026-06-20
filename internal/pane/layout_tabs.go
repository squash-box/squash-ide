package pane

import "fmt"

// tabBarRows is the number of rows the Manager reserves above the active pane
// for the tab strip the Tabs strategy renders.
const tabBarRows = 1

// Tabs shows one pane at full size with a tab strip above it listing every pane;
// the others are hidden behind their tabs. It is the layout of last resort in
// the Responsive auto-mode (T-040): when a region is too small to fit even a
// single stacked row at MinHeight, tabbing always succeeds, so a spawn reflows
// instead of being rejected.
//
// Arrange returns one rect per pane, every one sized to the content region
// (region minus the tab-bar row), so switching the active tab needs no resize —
// the child that becomes visible is already laid out at the right size. The
// Manager renders only the active pane's box plus the tab strip (composeTabs);
// the active tab tracks focus, and next/prev-tab is just Manager.FocusNext /
// FocusPrev.
type Tabs struct{}

// axis reports that the Manager composes Tabs as a tab strip + one pane.
func (Tabs) axis() layoutAxis { return axisTabbed }

// Arrange implements Strategy. Every pane gets the same content rect (the region
// less the tab-bar row). It rejects only when the content region cannot fit a
// single pane at MinWidth/MinHeight — the whole point of Tabs is that pane count
// never causes a rejection, so n is not part of the fit check.
func (Tabs) Arrange(region Rect, n int, c Constraints) ([]Rect, error) {
	if n < 1 {
		return nil, fmt.Errorf("pane.Tabs: n must be >= 1, got %d", n)
	}
	if region.W < 1 || region.H < 1 {
		return nil, fmt.Errorf("pane.Tabs: region must be non-empty, got %dx%d", region.W, region.H)
	}
	if c.MinWidth < 1 {
		return nil, fmt.Errorf("pane.Tabs: MinWidth must be >= 1, got %d", c.MinWidth)
	}
	minH := c.MinHeight
	if minH < 1 {
		minH = 1
	}

	contentH := region.H - tabBarRows
	if region.W < c.MinWidth || contentH < minH {
		return nil, fmt.Errorf(
			"%w: region %dx%d too small for a tab (need %dx%d incl tab bar)",
			ErrInsufficientSpace, region.W, region.H, c.MinWidth, minH+tabBarRows,
		)
	}

	rects := make([]Rect, n)
	for i := range rects {
		rects[i] = Rect{X: region.X, Y: region.Y + tabBarRows, W: region.W, H: contentH}
	}
	return rects, nil
}
