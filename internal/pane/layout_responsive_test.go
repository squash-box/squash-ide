package pane

import (
	"errors"
	"fmt"
	"testing"
)

// rc are the constraints the Responsive boundary tests reason about: MinWidth
// 20, MinHeight 5, Gutter 1.
var rc = Constraints{MinWidth: 20, MinHeight: 5, Gutter: 1}

func strategyName(s Strategy) string {
	switch s.(type) {
	case FlexColumns:
		return "columns"
	case StackRows:
		return "stack"
	case Tabs:
		return "tabs"
	default:
		return fmt.Sprintf("%T", s)
	}
}

// TestResponsive_PicksByRegion: wide → columns, narrow-but-tall → stack,
// narrow-and-short → tabs.
func TestResponsive_PicksByRegion(t *testing.T) {
	cases := []struct {
		name   string
		region Rect
		n      int
		want   string
	}{
		{"wide-columns", Rect{W: 400, H: 40}, 3, "columns"},
		{"narrow-tall-stack", Rect{W: 50, H: 40}, 3, "stack"},
		{"narrow-short-tabs", Rect{W: 50, H: 10}, 3, "tabs"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := (Responsive{}).resolve(tc.region, tc.n, rc)
			if got := strategyName(s); got != tc.want {
				t.Errorf("resolve(%v, %d) = %s, want %s", tc.region, tc.n, got, tc.want)
			}
		})
	}
}

// TestResponsive_ColumnsBoundary: the auto-mode keeps columns only while each
// pane clears responsiveColumnMinWidth (100), not the hard MinWidth (20). For
// n=3, Gutter 1 that is 3*100 + 3*1 = 303. 303 → columns, 302 → stack (tall).
func TestResponsive_ColumnsBoundary(t *testing.T) {
	tall := 40
	if s, _ := (Responsive{}).resolve(Rect{W: 303, H: tall}, 3, rc); strategyName(s) != "columns" {
		t.Errorf("W=303 picked %s, want columns (exact fit at 100/pane)", strategyName(s))
	}
	if s, _ := (Responsive{}).resolve(Rect{W: 302, H: tall}, 3, rc); strategyName(s) != "stack" {
		t.Errorf("W=302 picked %s, want stack (one col short of 100/pane)", strategyName(s))
	}
}

// TestResponsive_ColumnsBoundary_TwoPanes: the headline case — two panes go
// side-by-side only at 2*100 + 2*1 = 202 (avail = W - n*gutter must clear
// n*100), and stack below it even though they would comfortably fit columns at
// the old MinWidth(20) threshold.
func TestResponsive_ColumnsBoundary_TwoPanes(t *testing.T) {
	tall := 40
	if s, _ := (Responsive{}).resolve(Rect{W: 202, H: tall}, 2, rc); strategyName(s) != "columns" {
		t.Errorf("W=202 picked %s, want columns (exact fit at 100/pane)", strategyName(s))
	}
	if s, _ := (Responsive{}).resolve(Rect{W: 201, H: tall}, 2, rc); strategyName(s) != "stack" {
		t.Errorf("W=201 picked %s, want stack (one col short of 100/pane)", strategyName(s))
	}
	// A width that fits 2 columns at MinWidth(20) but not at 100/pane now stacks.
	if s, _ := (Responsive{}).resolve(Rect{W: 80, H: tall}, 2, rc); strategyName(s) != "stack" {
		t.Errorf("W=80 picked %s, want stack (fits old min, not 100/pane)", strategyName(s))
	}
}

// TestResponsive_SinglePaneKeepsColumns: the per-pane preference is skipped for
// n==1 — a lone pane fills the region, so a narrow region must not reflow it to
// the rows axis. (FlexColumns and StackRows render identically for one pane, but
// the axis the Manager composes along should stay columns.)
func TestResponsive_SinglePaneKeepsColumns(t *testing.T) {
	// 50 cols is far below responsiveColumnMinWidth but well above MinWidth(20).
	if s, _ := (Responsive{}).resolve(Rect{W: 50, H: 40}, 1, rc); strategyName(s) != "columns" {
		t.Errorf("single pane at W=50 picked %s, want columns", strategyName(s))
	}
}

// TestResponsive_StackBoundary: at a width too narrow for columns, stack needs
// H >= n*MinHeight + n*Gutter. For n=3 that is 18. 18 → stack, 17 → tabs.
func TestResponsive_StackBoundary(t *testing.T) {
	narrow := 50
	if s, _ := (Responsive{}).resolve(Rect{W: narrow, H: 18}, 3, rc); strategyName(s) != "stack" {
		t.Errorf("H=18 picked %s, want stack (exact fit)", strategyName(s))
	}
	if s, _ := (Responsive{}).resolve(Rect{W: narrow, H: 17}, 3, rc); strategyName(s) != "tabs" {
		t.Errorf("H=17 picked %s, want tabs (one row short)", strategyName(s))
	}
}

// TestResponsive_ReflowNeverRejects: the headline guarantee — spawning more
// panes than fit as columns reflows to stack/tabs instead of erroring, where
// FlexColumns alone would reject.
func TestResponsive_ReflowNeverRejects(t *testing.T) {
	region := Rect{W: 50, H: 40} // too narrow for 3 columns at MinWidth 20
	if _, err := (FlexColumns{}).Arrange(region, 3, rc); !errors.Is(err, ErrInsufficientSpace) {
		t.Fatalf("precondition: FlexColumns should reject, got %v", err)
	}
	if _, err := (Responsive{}).Arrange(region, 3, rc); err != nil {
		t.Errorf("Responsive should reflow (stack), got error %v", err)
	}
	// Even when too short to stack, it tabs rather than rejecting.
	if _, err := (Responsive{}).Arrange(Rect{W: 50, H: 10}, 3, rc); err != nil {
		t.Errorf("Responsive should reflow (tabs), got error %v", err)
	}
}

// TestResponsive_ErrorsOnlyWhenEvenTabsCantFit: when the region is too small for
// even a single tab, Responsive surfaces the wrapped rejection.
func TestResponsive_ErrorsOnlyWhenEvenTabsCantFit(t *testing.T) {
	_, err := (Responsive{}).Arrange(Rect{W: 10, H: 10}, 3, rc) // W < MinWidth
	if !errors.Is(err, ErrInsufficientSpace) {
		t.Errorf("error = %v, want ErrInsufficientSpace (even tabs cannot fit)", err)
	}
}

func TestLayoutNameRegistry(t *testing.T) {
	cases := []struct {
		name string
		want string
	}{
		{LayoutColumns, "columns"},
		{LayoutStack, "stack"},
		{LayoutTabs, "tabs"},
		{LayoutResponsive, "responsive"},
	}
	for _, tc := range cases {
		if got := strategyNameOrResponsive(StrategyForName(tc.name)); got != tc.want {
			t.Errorf("StrategyForName(%q) = %s, want %s", tc.name, got, tc.want)
		}
	}
	// Unknown falls back to responsive (config validates before this is reached).
	if got := strategyNameOrResponsive(StrategyForName("nope")); got != "responsive" {
		t.Errorf("StrategyForName(unknown) = %s, want responsive", got)
	}
}

func strategyNameOrResponsive(s Strategy) string {
	if _, ok := s.(Responsive); ok {
		return "responsive"
	}
	return strategyName(s)
}

// TestNextLayoutName wraps columns→stack→tabs→responsive→columns.
func TestNextLayoutName(t *testing.T) {
	want := map[string]string{
		LayoutColumns:    LayoutStack,
		LayoutStack:      LayoutTabs,
		LayoutTabs:       LayoutResponsive,
		LayoutResponsive: LayoutColumns,
		"garbage":        LayoutColumns, // unknown starts the cycle
	}
	for cur, exp := range want {
		if got := NextLayoutName(cur); got != exp {
			t.Errorf("NextLayoutName(%q) = %q, want %q", cur, got, exp)
		}
	}
}
