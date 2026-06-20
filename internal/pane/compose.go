package pane

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// layoutAxis tells the Manager how to stitch rendered pane boxes together and
// how to size panes. It is the rendering counterpart to the geometry a Strategy
// produces: FlexColumns lays out columns (axisColumns), StackRows rows
// (axisRows), and Tabs a strip + one pane (axisTabbed). Responsive resolves to
// one of the three per region.
type layoutAxis int

const (
	axisColumns layoutAxis = iota // boxes side by side, full height (FlexColumns)
	axisRows                      // boxes stacked, full width (StackRows)
	axisTabbed                    // one pane + a tab strip (Tabs)
)

// fixedAxis is the optional capability a Strategy implements when it composes
// other than as columns. FlexColumns (the T-037 default) does not implement it,
// so it — and any future strategy that forgets to — composes as columns,
// preserving the original behaviour. Responsive is handled separately because
// its axis depends on the region, not a constant.
type fixedAxis interface {
	axis() layoutAxis
}

// Collapsed-strip dimensions (bounding box, incl. border). A collapsed pane is
// rendered as a thin strip — vertical under columns, horizontal under rows — so
// its siblings reflow into the space it gives up.
const (
	collapsedStripW = 6 // columns axis: thin vertical strip width
	collapsedStripH = 3 // rows axis: short horizontal strip height
)

// resolveStrategy returns the concrete strategy and the axis the Manager should
// compose it along. Responsive resolves dynamically from (region, n, c); a
// strategy implementing fixedAxis reports its constant axis; everything else
// (FlexColumns) composes as columns. Adding a new strategy touches only this
// resolver and never the Manager's spawn/close/render lifecycle — the Open/
// Closed payoff of the T-037 seam.
func resolveStrategy(s Strategy, region Rect, n int, c Constraints) (Strategy, layoutAxis) {
	if r, ok := s.(Responsive); ok {
		return r.resolve(region, n, c)
	}
	if fa, ok := s.(fixedAxis); ok {
		return s, fa.axis()
	}
	return s, axisColumns
}

// computeRects produces one bounding-box rect per pane (in order) plus the axis
// to compose them along. Collapsed panes are carved out as fixed strips and the
// expanded panes are arranged in the region that remains, so toggling a collapse
// reflows the siblings. Tabbed layouts ignore collapse (only one pane is shown).
// An error (wrapping ErrInsufficientSpace) means the expanded panes don't fit;
// the caller swallows it and retains the prior geometry.
func computeRects(s Strategy, region Rect, panes []*Pane, collapsed map[string]bool, c Constraints) ([]Rect, layoutAxis, error) {
	n := len(panes)
	if n == 0 {
		return nil, axisColumns, nil
	}
	concrete, axis := resolveStrategy(s, region, n, c)

	if axis == axisTabbed {
		rects, err := concrete.Arrange(region, n, c)
		return rects, axis, err
	}

	nCollapsed := countCollapsed(panes, collapsed)
	if nCollapsed == 0 {
		rects, err := concrete.Arrange(region, n, c)
		return rects, axis, err
	}

	rects := make([]Rect, n)
	nExpanded := n - nCollapsed
	if nExpanded == 0 {
		// Every pane is collapsed: lay them all out as strips, no Arrange.
		for i := range panes {
			rects[i] = stripRect(region, axis)
		}
		return rects, axis, nil
	}

	exp, err := concrete.Arrange(reduceRegion(region, nCollapsed, axis, c), nExpanded, c)
	if err != nil {
		return nil, axis, err
	}
	ei := 0
	for i, p := range panes {
		if collapsed[p.id] {
			rects[i] = stripRect(region, axis)
		} else {
			rects[i] = exp[ei]
			ei++
		}
	}
	return rects, axis, nil
}

// reduceRegion shrinks region to make room for nCollapsed strips along axis, so
// the expanded panes get the space the collapsed ones gave up. Each collapsed
// pane costs one strip plus its gutter, keeping the total gutter count at n.
func reduceRegion(region Rect, nCollapsed int, axis layoutAxis, c Constraints) Rect {
	r := region
	switch axis {
	case axisRows:
		r.H = region.H - nCollapsed*(collapsedStripH+c.Gutter)
		if r.H < 1 {
			r.H = 1
		}
	default: // axisColumns
		r.W = region.W - nCollapsed*(collapsedStripW+c.Gutter)
		if r.W < 1 {
			r.W = 1
		}
	}
	return r
}

// stripRect is the bounding box of a single collapsed strip on the given axis.
func stripRect(region Rect, axis layoutAxis) Rect {
	switch axis {
	case axisRows:
		return Rect{X: region.X, Y: region.Y, W: region.W, H: collapsedStripH}
	default: // axisColumns
		return Rect{X: region.X, Y: region.Y, W: collapsedStripW, H: region.H}
	}
}

func countCollapsed(panes []*Pane, collapsed map[string]bool) int {
	n := 0
	for _, p := range panes {
		if collapsed[p.id] {
			n++
		}
	}
	return n
}

// composeLinear stitches the pane boxes for a columns or rows layout: a leading
// gutter (blank columns for columns, blank rows for rows) before each box, then
// a horizontal or vertical join. Collapsed panes render as strips. It reproduces
// the original FlexColumns composition exactly when nothing is collapsed.
func composeLinear(axis layoutAxis, panes []*Pane, rects []Rect, collapsed map[string]bool, focusID string, gutter int, blink bool) string {
	parts := make([]string, 0, len(panes)*(gutter+1))
	gap := strings.Repeat(" ", gutter)
	for i, p := range panes {
		if i >= len(rects) {
			break
		}
		var box string
		if collapsed[p.id] {
			box = p.RenderStrip(rects[i].W, rects[i].H, axis, p.id == focusID, blink)
		} else {
			box = p.Render(rects[i].W, rects[i].H, p.id == focusID, blink)
		}
		if axis == axisRows {
			for g := 0; g < gutter; g++ {
				parts = append(parts, "") // a leading blank row before each pane
			}
			parts = append(parts, box)
		} else {
			if gutter > 0 {
				parts = append(parts, gap) // a leading separator column before each pane
			}
			parts = append(parts, box)
		}
	}
	if axis == axisRows {
		return lipgloss.JoinVertical(lipgloss.Left, parts...)
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, parts...)
}

// composeTabs renders a tabbed layout: a tab strip listing every pane above the
// active pane's box. The active tab is the focused pane (or the first pane when
// focus is elsewhere); next/prev-tab is Manager.FocusNext / FocusPrev.
func composeTabs(region Rect, panes []*Pane, rects []Rect, focusID string, blink bool) string {
	if len(panes) == 0 {
		return ""
	}
	active := tabsActiveIndex(panes, focusID)
	strip := renderTabStrip(panes, active, region.W, blink)

	w, h := region.W, region.H-tabBarRows
	if active < len(rects) {
		w, h = rects[active].W, rects[active].H
	}
	if w < 1 {
		w = 1
	}
	if h < 1 {
		h = 1
	}
	box := panes[active].Render(w, h, true, blink)
	return lipgloss.JoinVertical(lipgloss.Left, strip, box)
}

// tabsActiveIndex returns the index of the focused pane, or 0 when focus is not
// on any pane (so a tabbed region always shows something).
func tabsActiveIndex(panes []*Pane, focusID string) int {
	for i, p := range panes {
		if p.id == focusID {
			return i
		}
	}
	return 0
}

// renderTabStrip draws the one-row tab bar: a chip per pane (task id + state
// glyph), the active one highlighted and an input_required pane pulsing with the
// blink phase. The strip is clamped to width so it never overruns the region.
func renderTabStrip(panes []*Pane, active, width int, blink bool) string {
	chips := make([]string, 0, len(panes))
	for i, p := range panes {
		label := p.TaskID()
		if label == "" {
			label = p.ID()
		}
		state := p.State()
		text := " " + stateGlyph(state) + " " + label + " "
		style := tabInactiveStyle
		switch {
		case state == StateInputRequired && blink:
			style = tabAlertStyle
		case i == active:
			style = tabActiveStyle
		}
		chips = append(chips, style.Render(text))
	}
	strip := lipgloss.JoinHorizontal(lipgloss.Top, chips...)
	return lipgloss.NewStyle().Width(width).MaxWidth(width).Render(strip)
}

// --- layout name registry ---------------------------------------------------
//
// These string values mirror the layout names config validates (config.Layout*)
// and are the single source of truth mapping a name to a Strategy. Keeping the
// mapping here (next to the strategies) means a new layout is registered in one
// place; config only needs to know the set of valid names.

const (
	LayoutColumns    = "columns"
	LayoutStack      = "stack"
	LayoutTabs       = "tabs"
	LayoutResponsive = "responsive"
)

// layoutCycle is the order the cycle-layout keybinding walks.
var layoutCycle = []string{LayoutColumns, LayoutStack, LayoutTabs, LayoutResponsive}

// StrategyForName maps a config layout name to its Strategy. An unknown name
// falls back to Responsive (the safe reflow-never-reject default); config
// validates the name at load so callers normally pass a known value.
func StrategyForName(name string) Strategy {
	switch name {
	case LayoutColumns:
		return FlexColumns{}
	case LayoutStack:
		return StackRows{}
	case LayoutTabs:
		return Tabs{}
	default:
		return Responsive{}
	}
}

// NextLayoutName returns the next layout name in the cycle
// columns → stack → tabs → responsive → columns. An unknown current name starts
// the cycle at the beginning.
func NextLayoutName(current string) string {
	for i, name := range layoutCycle {
		if name == current {
			return layoutCycle[(i+1)%len(layoutCycle)]
		}
	}
	return layoutCycle[0]
}
