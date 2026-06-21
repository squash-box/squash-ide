package pane

import (
	"errors"
	"testing"
)

// msc are the constraints the MainStack geometry tests reason about: MinWidth
// 20, MinHeight 5, Gutter 1 (the manager default).
var msc = Constraints{MinWidth: 20, MinHeight: 5, Gutter: 1}

// TestMainStack_Arrange_Geometry: 3 panes in a 240x50 region. Two equal columns
// (availW = 240-2 = 238 → side 119, main 119), the main full height on the left
// and the 2 side panes stacking the full height of the right column.
func TestMainStack_Arrange_Geometry(t *testing.T) {
	region := Rect{X: 10, Y: 0, W: 240, H: 50}
	rects, err := (MainStack{}).Arrange(region, 3, msc)
	if err != nil {
		t.Fatalf("Arrange error: %v", err)
	}
	if len(rects) != 3 {
		t.Fatalf("got %d rects, want 3", len(rects))
	}

	// Main column: leading gutter, full height, left half.
	main := rects[0]
	if main.X != region.X+1 || main.Y != region.Y || main.H != region.H {
		t.Errorf("main rect = %+v, want X=%d Y=%d H=%d", main, region.X+1, region.Y, region.H)
	}
	if main.W != 119 {
		t.Errorf("main.W = %d, want 119", main.W)
	}

	// Side panes: a single column to the right of main (X = X+g+mainW+g), each
	// the side width, stacked to fill the region height with a leading gutter row.
	sideX := region.X + 1 + main.W + 1
	for i, r := range rects[1:] {
		if r.X != sideX {
			t.Errorf("side[%d].X = %d, want %d", i, r.X, sideX)
		}
		if r.W != 119 {
			t.Errorf("side[%d].W = %d, want 119", i, r.W)
		}
	}
	// Vertical packing: leading gutter before each side pane, heights fill H.
	if got := rects[1].Y; got != region.Y+1 {
		t.Errorf("side[0].Y = %d, want %d", got, region.Y+1)
	}
	if got := rects[2].Y; got != rects[1].Y+rects[1].H+1 {
		t.Errorf("side[1].Y = %d, want %d (after side[0]+gutter)", got, rects[1].Y+rects[1].H+1)
	}
	// availH = 50 - 2*1 = 48, split 24/24 (no remainder).
	if rects[1].H != 24 || rects[2].H != 24 {
		t.Errorf("side heights = %d,%d, want 24,24", rects[1].H, rects[2].H)
	}
}

// TestMainStack_Arrange_OddRemainders: the width remainder goes to the main
// column and the height remainder to the topmost side pane, matching
// FlexColumns/StackRows.
func TestMainStack_Arrange_OddRemainders(t *testing.T) {
	// availW = 205-2 = 203 → side 101, main 102 (remainder to main).
	// availH = 51-2 = 49 → per 24, remainder 1 to the top side pane → 25,24.
	rects, err := (MainStack{}).Arrange(Rect{W: 205, H: 51}, 3, msc)
	if err != nil {
		t.Fatalf("Arrange error: %v", err)
	}
	if rects[0].W != 102 {
		t.Errorf("main.W = %d, want 102 (width remainder to main)", rects[0].W)
	}
	if rects[1].W != 101 {
		t.Errorf("side.W = %d, want 101", rects[1].W)
	}
	if rects[1].H != 25 || rects[2].H != 24 {
		t.Errorf("side heights = %d,%d, want 25,24 (height remainder to top)", rects[1].H, rects[2].H)
	}
}

// TestMainStack_Arrange_Rejects: too narrow for two columns at MinWidth, and too
// short to stack the n-1 side panes at MinHeight, both wrap ErrInsufficientSpace.
func TestMainStack_Arrange_Rejects(t *testing.T) {
	// 2*MinWidth + 2*gutter = 42; W=41 is one column short.
	if _, err := (MainStack{}).Arrange(Rect{W: 41, H: 50}, 3, msc); !errors.Is(err, ErrInsufficientSpace) {
		t.Errorf("too-narrow error = %v, want ErrInsufficientSpace", err)
	}
	// Side column stacks 2 panes: needs 2*5 + 2*gutter = 12; H=11 is one short.
	if _, err := (MainStack{}).Arrange(Rect{W: 240, H: 11}, 3, msc); !errors.Is(err, ErrInsufficientSpace) {
		t.Errorf("too-short error = %v, want ErrInsufficientSpace", err)
	}
}

// TestMainStack_Arrange_NeedsTwoPanes: n<2 is a programming error, not a space
// rejection (one pane has no "stack" to fill the side column).
func TestMainStack_Arrange_NeedsTwoPanes(t *testing.T) {
	_, err := (MainStack{}).Arrange(Rect{W: 240, H: 50}, 1, msc)
	if err == nil || errors.Is(err, ErrInsufficientSpace) {
		t.Errorf("n=1 error = %v, want a non-space error", err)
	}
}
