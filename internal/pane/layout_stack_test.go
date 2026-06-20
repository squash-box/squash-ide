package pane

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func heightsOf(rects []Rect) []int {
	hs := make([]int, len(rects))
	for i, r := range rects {
		hs[i] = r.H
	}
	return hs
}

// TestStackRows_HappyPath asserts exact X/Y/W/H: full-width rows stacked top to
// bottom, a leading gutter row before each, summing to the region height.
func TestStackRows_HappyPath(t *testing.T) {
	// region H=42, n=2, gutter=1 -> avail = 42-2 = 40 -> 20 each.
	region := Rect{X: 5, Y: 3, W: 80, H: 42}
	rects, err := StackRows{}.Arrange(region, 2, Constraints{MinHeight: 5, Gutter: 1})
	if err != nil {
		t.Fatalf("Arrange: %v", err)
	}
	want := []Rect{
		{X: 5, Y: 4, W: 80, H: 20},  // region.Y + 1 gutter
		{X: 5, Y: 25, W: 80, H: 20}, // 4 + 20 + 1 gutter
	}
	if !reflect.DeepEqual(rects, want) {
		t.Errorf("rects = %v, want %v", rects, want)
	}
	used := 0
	for _, r := range rects {
		used += r.H
	}
	used += 2 // two gutters
	if used != region.H {
		t.Errorf("rows+gutters used %d, region is %d", used, region.H)
	}
}

// TestStackRows_RemainderToTopmost is the row-wise dual of FlexColumns'
// remainder-to-leftmost: leftover rows go to the topmost panes.
func TestStackRows_RemainderToTopmost(t *testing.T) {
	// H=43, n=2, gutter=1 -> avail = 41 -> 20 r1 -> first pane +1.
	rects, err := StackRows{}.Arrange(Rect{W: 80, H: 43}, 2, Constraints{MinHeight: 5, Gutter: 1})
	if err != nil {
		t.Fatalf("Arrange: %v", err)
	}
	if got, want := heightsOf(rects), []int{21, 20}; !reflect.DeepEqual(got, want) {
		t.Errorf("heights = %v, want %v", got, want)
	}
}

// TestStackRows_RejectsWhenTooShort mirrors FlexColumns' rejection on the height
// axis: a region too short for n rows at MinHeight wraps ErrInsufficientSpace.
func TestStackRows_RejectsWhenTooShort(t *testing.T) {
	// avail = 11-2 = 9; 9/2 = 4 < 5 -> reject.
	_, err := StackRows{}.Arrange(Rect{W: 80, H: 11}, 2, Constraints{MinHeight: 5, Gutter: 1})
	if !errors.Is(err, ErrInsufficientSpace) {
		t.Fatalf("error = %v, want wrapped ErrInsufficientSpace", err)
	}
	for _, frag := range []string{"region height=", "panes=", "min="} {
		if !strings.Contains(err.Error(), frag) {
			t.Errorf("error %q missing fragment %q", err.Error(), frag)
		}
	}
}

func TestStackRows_BadInputs(t *testing.T) {
	cases := []struct {
		name   string
		region Rect
		n      int
		c      Constraints
		want   string
	}{
		{"zero-panes", Rect{W: 80, H: 40}, 0, Constraints{MinHeight: 5, Gutter: 1}, "n must be >= 1"},
		{"empty-region", Rect{W: 80, H: 0}, 1, Constraints{MinHeight: 5}, "region must be non-empty"},
		{"zero-min", Rect{W: 80, H: 40}, 1, Constraints{MinHeight: 0, Gutter: 1}, "MinHeight must be >= 1"},
		{"negative-gutter", Rect{W: 80, H: 40}, 1, Constraints{MinHeight: 5, Gutter: -1}, "Gutter must be >= 0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := StackRows{}.Arrange(tc.region, tc.n, tc.c)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want containing %q", err, tc.want)
			}
		})
	}
}

func TestStackRows_Axis(t *testing.T) {
	if (StackRows{}).axis() != axisRows {
		t.Errorf("StackRows axis = %v, want axisRows", (StackRows{}).axis())
	}
}
