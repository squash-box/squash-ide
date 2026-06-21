package pane

import (
	"strings"
	"testing"
)

// TestManager_MainStack_RenderAndHitTest drives the hybrid end-to-end through
// the Manager: 3 panes under the Responsive auto-mode in a region wide enough
// for two 100-col columns but not three, and tall enough to stack the 2 side
// panes. The layout must resolve to axisMainStack, render a non-empty 2-column
// view, and hit-test each of the three rects to the right task id.
func TestManager_MainStack_RenderAndHitTest(t *testing.T) {
	fake := newFakePTY(t)
	m := NewManager(WithStarter(fake), WithStrategy(Responsive{}),
		WithConstraints(Constraints{MinWidth: 20, MinHeight: 5, Gutter: 1}))
	region := Rect{X: 60, Y: 0, W: 240, H: 50} // fits 2 cols@100, not 3; tall
	m.Resize(region)
	for _, id := range []string{"T-MAIN", "T-2", "T-3"} {
		if _, err := m.Spawn(SpawnSpec{Command: fakeCmd("claude"), TaskID: id}); err != nil {
			t.Fatalf("spawn %s: %v", id, err)
		}
	}

	rects, axis := hitRects(t, m)
	if axis != axisMainStack {
		t.Fatalf("axis = %d, want axisMainStack", axis)
	}
	if len(rects) != 3 {
		t.Fatalf("got %d rects, want 3", len(rects))
	}

	// Main pane is full height on the left; the two side panes sit in a column
	// to its right at distinct Y bands.
	main, s1, s2 := rects[0], rects[1], rects[2]
	if main.H != region.H {
		t.Errorf("main.H = %d, want full height %d", main.H, region.H)
	}
	if !(s1.X > main.X+main.W && s2.X == s1.X) {
		t.Errorf("side panes not in a column right of main: main=%+v s1=%+v s2=%+v", main, s1, s2)
	}

	// Hit-test: a point inside each rect resolves to that pane's task.
	for _, tc := range []struct {
		want string
		r    Rect
	}{
		{"T-MAIN", main},
		{"T-2", s1},
		{"T-3", s2},
	} {
		if id, ok := m.TaskAtPoint(tc.r.X+1, tc.r.Y+1); !ok || id != tc.want {
			t.Errorf("inside %s rect = (%q, %v), want (%s, true)", tc.want, id, ok, tc.want)
		}
	}

	// Render composes without panicking and produces a non-empty 2-column view.
	out := m.Render(region)
	if strings.TrimSpace(out) == "" {
		t.Error("main+stack Render produced empty output")
	}
}
