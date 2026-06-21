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
// The choice is a pure function of (region, n, constraints): columns if each
// pane clears responsiveColumnMinWidth, else rows if they fit at MinHeight, else
// tabs (which never fails on count). Because it is pure and deterministic, the
// Manager can resolve the concrete sub-strategy once and use it for both pane
// sizing and rendering with no risk of the two disagreeing.
type Responsive struct{}

// responsiveColumnMinWidth is the per-pane width the auto-mode demands before it
// keeps two or more panes side-by-side. It is deliberately wider than the hard
// MinWidth floor: equal columns only earn their keep when each pane stays
// comfortably readable, so below this Responsive reflows to stacked rows (where
// every pane gets the full region width) rather than cram narrow columns.
//
// Only the auto-mode consults it, and only for n>=2 — a lone pane fills the
// region whichever axis is chosen. A pinned `layout: columns` still uses the
// raw MinWidth and hard-rejects, so this never blocks an explicit columns spawn.
const responsiveColumnMinWidth = 100

// Arrange implements Strategy by delegating to the sub-strategy chosen for this
// region/n/constraints. Called only when Responsive is used directly (no
// collapse context), so it resolves with nCollapsed 0.
func (r Responsive) Arrange(region Rect, n int, c Constraints) ([]Rect, error) {
	s, _ := r.resolve(region, n, c, 0)
	return s.Arrange(region, n, c)
}

// resolve picks the concrete sub-strategy (and the axis the Manager composes it
// along) for the given region: columns → main+stack → stack → tabs, taking the
// first that fits; tabs is the always-succeeds fallback that delivers the
// reflow-never-reject guarantee. nCollapsed gates the main+stack hybrid, whose
// 2-D geometry the collapse-strip reflow doesn't model.
func (r Responsive) resolve(region Rect, n int, c Constraints, nCollapsed int) (Strategy, layoutAxis) {
	if n <= 0 {
		return FlexColumns{}, axisColumns
	}
	// Probe columns against a wider per-pane minimum than the hard floor, so the
	// auto-mode prefers stacking once columns would get cramped. n==1 keeps the
	// raw constraint — a single pane fills the region either way, and bumping it
	// would needlessly reflow a lone pane to rows. The actual Arrange below uses
	// the real constraints; since responsiveColumnMinWidth >= MinWidth, a region
	// that clears the probe also clears the real sizing pass.
	colC := c
	if n >= 2 && colC.MinWidth < responsiveColumnMinWidth {
		colC.MinWidth = responsiveColumnMinWidth
	}
	if _, err := (FlexColumns{}).Arrange(region, n, colC); err == nil {
		return FlexColumns{}, axisColumns
	}
	// Before collapsing to a pure vertical stack, try the main+stack hybrid: one
	// full-height pane beside a column of the rest. Needs 3+ panes (n==2
	// degenerates to two columns, already handled above) and the same wider
	// per-pane width bar as columns. Skipped while any pane is collapsed — the
	// 2-D layout has no strip-reflow path (v1).
	if n >= 3 && nCollapsed == 0 {
		if _, err := (MainStack{}).Arrange(region, n, colC); err == nil {
			return MainStack{}, axisMainStack
		}
	}
	if _, err := (StackRows{}).Arrange(region, n, c); err == nil {
		return StackRows{}, axisRows
	}
	return Tabs{}, axisTabbed
}
