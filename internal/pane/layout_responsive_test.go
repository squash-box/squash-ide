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

// TestResponsive_ColumnsBoundary: columns need W >= n*MinWidth + n*Gutter. For
// n=3, MinWidth 20, Gutter 1 that is 63. 63 → columns, 62 → stack (region tall).
func TestResponsive_ColumnsBoundary(t *testing.T) {
	tall := 40
	if s, _ := (Responsive{}).resolve(Rect{W: 63, H: tall}, 3, rc); strategyName(s) != "columns" {
		t.Errorf("W=63 picked %s, want columns (exact fit)", strategyName(s))
	}
	if s, _ := (Responsive{}).resolve(Rect{W: 62, H: tall}, 3, rc); strategyName(s) != "stack" {
		t.Errorf("W=62 picked %s, want stack (one col short)", strategyName(s))
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
