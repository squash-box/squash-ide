package pane

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/squashbox/squash-ide/internal/tmux"
)

// widthsOf extracts just the W of each rect, for comparison against tmux.Tile.
func widthsOf(rects []Rect) []int {
	ws := make([]int, len(rects))
	for i, r := range rects {
		ws[i] = r.W
	}
	return ws
}

// TestFlexColumns_ParityWithTile is the layout-parity acceptance check: for the
// same inputs, FlexColumns over a region of width (totalCols-tuiWidth) with one
// gutter per pane produces exactly the per-pane widths tmux.Tile returns. The
// cases mirror internal/tmux/layout_test.go's happy-path table.
func TestFlexColumns_ParityWithTile(t *testing.T) {
	cases := []struct {
		name                     string
		totalCols, tuiW, n, minW int
	}{
		{"ultrawide-3panes-clean", 360, 60, 3, 80},
		{"single-pane-fills-rest", 200, 60, 1, 80},
		{"two-panes-even", 262, 60, 2, 80},
		{"two-panes-remainder-leftmost", 263, 60, 2, 80},
		{"four-panes-remainder-distributed", 425, 60, 4, 80},
		{"exact-fit-at-minimum", 60 + 4 + 4*80, 60, 4, 80},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			want, err := tmux.Tile(tc.totalCols, tc.tuiW, tc.n, tc.minW)
			if err != nil {
				t.Fatalf("tmux.Tile: unexpected error: %v", err)
			}
			region := Rect{X: 0, Y: 0, W: tc.totalCols - tc.tuiW, H: 24}
			rects, err := FlexColumns{}.Arrange(region, tc.n, Constraints{MinWidth: tc.minW, Gutter: 1})
			if err != nil {
				t.Fatalf("FlexColumns.Arrange: unexpected error: %v", err)
			}
			if got := widthsOf(rects); !reflect.DeepEqual(got, want) {
				t.Errorf("FlexColumns widths = %v, want (tmux.Tile) %v", got, want)
			}
		})
	}
}

// TestFlexColumns_HappyPath asserts exact X/Y/W/H placement, including the
// leading gutter before each pane and full-height columns.
func TestFlexColumns_HappyPath(t *testing.T) {
	// region W=202, n=2, gutter=1 -> avail = 202-2 = 200 -> 100 each.
	region := Rect{X: 5, Y: 3, W: 202, H: 40}
	rects, err := FlexColumns{}.Arrange(region, 2, Constraints{MinWidth: 80, Gutter: 1})
	if err != nil {
		t.Fatalf("Arrange: %v", err)
	}
	want := []Rect{
		{X: 6, Y: 3, W: 100, H: 40},   // region.X + 1 gutter
		{X: 107, Y: 3, W: 100, H: 40}, // 6 + 100 + 1 gutter
	}
	if !reflect.DeepEqual(rects, want) {
		t.Errorf("rects = %v, want %v", rects, want)
	}
	// Panes + gutters fill the region width exactly.
	used := 0
	for _, r := range rects {
		used += r.W
	}
	used += 2 // two gutters
	if used != region.W {
		t.Errorf("panes+gutters used %d cols, region is %d", used, region.W)
	}
}

// TestFlexColumns_RemainderToLeftmost ports tmux.Tile's remainder semantics:
// when the available width does not divide evenly, the leftmost panes get the
// extra columns.
func TestFlexColumns_RemainderToLeftmost(t *testing.T) {
	// W=363, n=4, gutter=1 -> avail = 363-4 = 359 -> 89 r3 -> first three +1.
	rects, err := FlexColumns{}.Arrange(Rect{W: 363, H: 24}, 4, Constraints{MinWidth: 10, Gutter: 1})
	if err != nil {
		t.Fatalf("Arrange: %v", err)
	}
	want := []int{90, 90, 90, 89}
	if got := widthsOf(rects); !reflect.DeepEqual(got, want) {
		t.Errorf("widths = %v, want %v", got, want)
	}
}

// TestFlexColumns_SinglePaneAndMinRegion covers n=1 and a minimally viable
// region.
func TestFlexColumns_SinglePaneAndMinRegion(t *testing.T) {
	// n=1, gutter=0, region exactly MinWidth.
	rects, err := FlexColumns{}.Arrange(Rect{W: 10, H: 1}, 1, Constraints{MinWidth: 10, Gutter: 0})
	if err != nil {
		t.Fatalf("Arrange: %v", err)
	}
	if len(rects) != 1 || rects[0].W != 10 || rects[0].H != 1 {
		t.Errorf("rects = %v, want one 10x1 column", rects)
	}
}

// TestFlexColumns_RejectsWhenTooNarrow mirrors tmux.Tile's rejection: a region
// too narrow for n panes at MinWidth returns a wrapped ErrInsufficientSpace
// carrying the numbers.
func TestFlexColumns_RejectsWhenTooNarrow(t *testing.T) {
	// avail = 138-2 = 136; 136/2 = 68 < 80 -> reject.
	_, err := FlexColumns{}.Arrange(Rect{W: 138, H: 24}, 2, Constraints{MinWidth: 80, Gutter: 1})
	if err == nil {
		t.Fatal("expected rejection, got nil")
	}
	if !errors.Is(err, ErrInsufficientSpace) {
		t.Errorf("error %v should wrap ErrInsufficientSpace", err)
	}
	msg := err.Error()
	for _, frag := range []string{"region width=", "panes=", "min="} {
		if !strings.Contains(msg, frag) {
			t.Errorf("error %q missing fragment %q", msg, frag)
		}
	}
}

func TestFlexColumns_BadInputs(t *testing.T) {
	cases := []struct {
		name   string
		region Rect
		n      int
		c      Constraints
		want   string
	}{
		{"zero-panes", Rect{W: 100, H: 10}, 0, Constraints{MinWidth: 10, Gutter: 1}, "n must be >= 1"},
		{"negative-panes", Rect{W: 100, H: 10}, -1, Constraints{MinWidth: 10}, "n must be >= 1"},
		{"empty-region", Rect{W: 0, H: 10}, 1, Constraints{MinWidth: 10}, "region must be non-empty"},
		{"zero-min", Rect{W: 100, H: 10}, 1, Constraints{MinWidth: 0, Gutter: 1}, "MinWidth must be >= 1"},
		{"negative-gutter", Rect{W: 100, H: 10}, 1, Constraints{MinWidth: 10, Gutter: -1}, "Gutter must be >= 0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := FlexColumns{}.Arrange(tc.region, tc.n, tc.c)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q missing %q", err.Error(), tc.want)
			}
		})
	}
}
