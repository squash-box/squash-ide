package pane

// Responsive is the auto-mode that picks a concrete sub-strategy from the
// available region and pane count, degrading columns → stack → tabs as space
// runs out. It is the headline behavioural change over tmux: where tmux.Tile
// (and the FlexColumns port) *reject* a spawn that won't fit as equal columns,
// Responsive *reflows* — it stacks when the region is too narrow for columns and
// tabs when it is too short to stack, so a spawn under engine=native never hard-
// rejects. The hard reject remains available only when the user pins
// layout: columns.
//
// The choice is a pure function of (region, n, constraints): columns if they fit
// at MinWidth, else rows if they fit at MinHeight, else tabs (which never fails
// on count). Because it is pure and deterministic, the Manager can resolve the
// concrete sub-strategy once and use it for both pane sizing and rendering with
// no risk of the two disagreeing.
type Responsive struct{}

// Arrange implements Strategy by delegating to the sub-strategy chosen for this
// region/n/constraints.
func (r Responsive) Arrange(region Rect, n int, c Constraints) ([]Rect, error) {
	s, _ := r.resolve(region, n, c)
	return s.Arrange(region, n, c)
}

// resolve picks the concrete sub-strategy (and the axis the Manager composes it
// along) for the given region. columns → stack → tabs, taking the first that
// fits; tabs is the always-succeeds fallback that delivers the reflow-never-
// reject guarantee.
func (r Responsive) resolve(region Rect, n int, c Constraints) (Strategy, layoutAxis) {
	if n <= 0 {
		return FlexColumns{}, axisColumns
	}
	if _, err := (FlexColumns{}).Arrange(region, n, c); err == nil {
		return FlexColumns{}, axisColumns
	}
	if _, err := (StackRows{}).Arrange(region, n, c); err == nil {
		return StackRows{}, axisRows
	}
	return Tabs{}, axisTabbed
}
