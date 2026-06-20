package pane

import (
	"errors"
	"testing"
)

// TestTabs_ArrangeFullContentRect: every pane gets the same content rect (the
// region less the tab-bar row), so switching tabs needs no resize.
func TestTabs_ArrangeFullContentRect(t *testing.T) {
	region := Rect{X: 10, Y: 2, W: 100, H: 40}
	rects, err := Tabs{}.Arrange(region, 3, Constraints{MinWidth: 20, MinHeight: 5})
	if err != nil {
		t.Fatalf("Arrange: %v", err)
	}
	if len(rects) != 3 {
		t.Fatalf("got %d rects, want 3", len(rects))
	}
	wantH := region.H - tabBarRows
	for i, r := range rects {
		if r.W != region.W || r.H != wantH {
			t.Errorf("rect[%d] = %dx%d, want %dx%d (region less tab bar)", i, r.W, r.H, region.W, wantH)
		}
		if r.Y != region.Y+tabBarRows {
			t.Errorf("rect[%d].Y = %d, want %d (below the tab bar)", i, r.Y, region.Y+tabBarRows)
		}
	}
}

// TestTabs_SinglePane: one pane is still valid — a single (inert) tab, full
// content.
func TestTabs_SinglePane(t *testing.T) {
	rects, err := Tabs{}.Arrange(Rect{W: 50, H: 20}, 1, Constraints{MinWidth: 20, MinHeight: 5})
	if err != nil {
		t.Fatalf("Arrange: %v", err)
	}
	if len(rects) != 1 || rects[0].H != 20-tabBarRows {
		t.Errorf("rects = %v, want one full-content rect", rects)
	}
}

// TestTabs_RejectsWhenTooSmall: tabs is count-insensitive but still rejects a
// region too small for even one pane at MinWidth/MinHeight.
func TestTabs_RejectsWhenTooSmall(t *testing.T) {
	t.Run("too-narrow", func(t *testing.T) {
		_, err := Tabs{}.Arrange(Rect{W: 10, H: 40}, 3, Constraints{MinWidth: 20, MinHeight: 5})
		if !errors.Is(err, ErrInsufficientSpace) {
			t.Errorf("error = %v, want ErrInsufficientSpace", err)
		}
	})
	t.Run("too-short", func(t *testing.T) {
		// contentH = 5-1 = 4 < MinHeight 5 -> reject.
		_, err := Tabs{}.Arrange(Rect{W: 80, H: 5}, 3, Constraints{MinWidth: 20, MinHeight: 5})
		if !errors.Is(err, ErrInsufficientSpace) {
			t.Errorf("error = %v, want ErrInsufficientSpace", err)
		}
	})
}

func TestTabs_Axis(t *testing.T) {
	if (Tabs{}).axis() != axisTabbed {
		t.Errorf("Tabs axis = %v, want axisTabbed", (Tabs{}).axis())
	}
}

// TestTabsActiveIndex: the active tab tracks focus; unknown focus falls to 0.
func TestTabsActiveIndex(t *testing.T) {
	fake := newFakePTY(t)
	m := NewManager(WithStarter(fake), WithStrategy(Tabs{}))
	m.Resize(Rect{W: 120, H: 40})
	a, _ := m.Spawn(SpawnSpec{Command: fakeCmd("claude"), TaskID: "T-1"})
	b, _ := m.Spawn(SpawnSpec{Command: fakeCmd("claude"), TaskID: "T-2"})

	panes := m.Panes()
	if got := tabsActiveIndex(panes, ""); got != 0 {
		t.Errorf("no focus -> active %d, want 0", got)
	}
	if got := tabsActiveIndex(panes, b.ID()); got != 1 {
		t.Errorf("focus b -> active %d, want 1", got)
	}
	// next-tab is FocusNext: with focus on a, advancing reaches b.
	_ = m.Focus(a.ID())
	m.FocusNext()
	if got := tabsActiveIndex(m.Panes(), m.Focused().ID()); got != 1 {
		t.Errorf("after FocusNext active = %d, want 1", got)
	}
	// Render must not panic and should mention both task ids in the strip.
	out := m.Render(Rect{W: 120, H: 40})
	if out == "" {
		t.Error("tabbed render returned empty")
	}
}
