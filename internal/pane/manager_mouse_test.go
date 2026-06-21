package pane

import "testing"

// hitRects recomputes the geometry TaskAtPoint hit-tests against, so the test
// asserts against the same rects the production code derives rather than a
// hand-copied table that could drift from the layout math.
func hitRects(t *testing.T, m *Manager) ([]Rect, layoutAxis) {
	t.Helper()
	rects, axis, err := computeRects(m.strategy, m.region, m.panes, m.collapsed, m.constraints)
	if err != nil {
		t.Fatalf("computeRects: %v", err)
	}
	return rects, axis
}

func TestManager_TaskAtPoint_Columns(t *testing.T) {
	fake := newFakePTY(t)
	m := NewManager(WithStarter(fake), WithStrategy(FlexColumns{}),
		WithConstraints(Constraints{MinWidth: 20, MinHeight: 5, Gutter: 1}))
	// Region offset in X mirrors rightRegion (X = tuiWidth + gutter): the hit-test
	// must use absolute screen cells, so a non-zero X proves no offset translation.
	region := Rect{X: 60, Y: 0, W: 120, H: 40}
	m.Resize(region)
	if _, err := m.Spawn(SpawnSpec{Command: fakeCmd("claude"), TaskID: "T-A"}); err != nil {
		t.Fatalf("spawn A: %v", err)
	}
	if _, err := m.Spawn(SpawnSpec{Command: fakeCmd("claude"), TaskID: "T-B"}); err != nil {
		t.Fatalf("spawn B: %v", err)
	}

	rects, axis := hitRects(t, m)
	if axis != axisColumns {
		t.Fatalf("axis = %d, want axisColumns", axis)
	}
	r0, r1 := rects[0], rects[1]

	// A point inside each pane's rect resolves to that pane's task id.
	if id, ok := m.TaskAtPoint(r0.X+1, r0.Y+1); !ok || id != "T-A" {
		t.Errorf("inside pane 0 = (%q, %v), want (T-A, true)", id, ok)
	}
	if id, ok := m.TaskAtPoint(r1.X+1, r1.Y+1); !ok || id != "T-B" {
		t.Errorf("inside pane 1 = (%q, %v), want (T-B, true)", id, ok)
	}

	// Far X edge of pane 0 (x == r0.X+r0.W) is the gutter between the columns —
	// half-open bounds put it outside pane 0 and before pane 1.
	if id, ok := m.TaskAtPoint(r0.X+r0.W, r0.Y); ok {
		t.Errorf("gutter between columns = (%q, %v), want ( , false)", id, ok)
	}
	// Leading gutter before the first pane (region.X) is outside every pane.
	if id, ok := m.TaskAtPoint(region.X, 0); ok {
		t.Errorf("leading gutter = (%q, %v), want ( , false)", id, ok)
	}
	// Far Y edge (y == r0.Y+r0.H) is below the full-height column.
	if id, ok := m.TaskAtPoint(r0.X+1, r0.Y+r0.H); ok {
		t.Errorf("far Y edge = (%q, %v), want ( , false)", id, ok)
	}
}

func TestManager_TaskAtPoint_Rows(t *testing.T) {
	fake := newFakePTY(t)
	m := NewManager(WithStarter(fake), WithStrategy(StackRows{}),
		WithConstraints(Constraints{MinWidth: 20, MinHeight: 5, Gutter: 1}))
	region := Rect{X: 60, Y: 0, W: 80, H: 40}
	m.Resize(region)
	if _, err := m.Spawn(SpawnSpec{Command: fakeCmd("claude"), TaskID: "T-TOP"}); err != nil {
		t.Fatalf("spawn top: %v", err)
	}
	if _, err := m.Spawn(SpawnSpec{Command: fakeCmd("claude"), TaskID: "T-BOT"}); err != nil {
		t.Fatalf("spawn bottom: %v", err)
	}

	rects, axis := hitRects(t, m)
	if axis != axisRows {
		t.Fatalf("axis = %d, want axisRows", axis)
	}
	top, bot := rects[0], rects[1]

	// A point in the lower row resolves to the second pane (Y-offset hit-test).
	if id, ok := m.TaskAtPoint(bot.X+1, bot.Y+1); !ok || id != "T-BOT" {
		t.Errorf("inside lower row = (%q, %v), want (T-BOT, true)", id, ok)
	}
	if id, ok := m.TaskAtPoint(top.X+1, top.Y+1); !ok || id != "T-TOP" {
		t.Errorf("inside upper row = (%q, %v), want (T-TOP, true)", id, ok)
	}
}

func TestManager_TaskAtPoint_Tabbed(t *testing.T) {
	fake := newFakePTY(t)
	m := NewManager(WithStarter(fake), WithStrategy(Tabs{}),
		WithConstraints(Constraints{MinWidth: 20, MinHeight: 5, Gutter: 1}))
	region := Rect{X: 60, Y: 0, W: 80, H: 40}
	m.Resize(region)
	a, _ := m.Spawn(SpawnSpec{Command: fakeCmd("claude"), TaskID: "T-A"})
	if _, err := m.Spawn(SpawnSpec{Command: fakeCmd("claude"), TaskID: "T-B"}); err != nil {
		t.Fatalf("spawn B: %v", err)
	}
	_ = a

	rects, axis := hitRects(t, m)
	if axis != axisTabbed {
		t.Fatalf("axis = %d, want axisTabbed", axis)
	}
	content := rects[0] // all rects are the identical content area under Tabs

	// With nothing focused the active tab is pane 0 — a content-area click returns
	// it (not necessarily a positional scan's index 0, but here they coincide).
	if id, ok := m.TaskAtPoint(content.X+1, content.Y+1); !ok || id != "T-A" {
		t.Errorf("content click (no focus) = (%q, %v), want (T-A, true)", id, ok)
	}

	// Focus pane B: a content-area click now returns the *focused* tab, proving the
	// hit-test resolves to the visible pane rather than always index 0.
	if err := m.FocusByTask("T-B"); err != nil {
		t.Fatalf("FocusByTask: %v", err)
	}
	if id, ok := m.TaskAtPoint(content.X+1, content.Y+1); !ok || id != "T-B" {
		t.Errorf("content click (B focused) = (%q, %v), want (T-B, true)", id, ok)
	}

	// A click in the tab strip (the row above the content, y == region.Y) is not a
	// pane click — tab-strip click-to-select is out of scope (T-051).
	if id, ok := m.TaskAtPoint(content.X+1, region.Y); ok {
		t.Errorf("tab-strip click = (%q, %v), want ( , false)", id, ok)
	}
}

func TestManager_TaskAtPoint_ZeroPanes(t *testing.T) {
	fake := newFakePTY(t)
	m := NewManager(WithStarter(fake))
	m.Resize(wideRegion)
	if id, ok := m.TaskAtPoint(10, 10); ok {
		t.Errorf("TaskAtPoint with zero panes = (%q, %v), want ( , false)", id, ok)
	}
}

func TestManager_TaskAtPoint_EmptyRegion(t *testing.T) {
	fake := newFakePTY(t)
	m := NewManager(WithStarter(fake))
	// Spawn before any Resize: region is the zero value.
	if _, err := m.Spawn(SpawnSpec{Command: fakeCmd("claude"), TaskID: "T-A"}); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if id, ok := m.TaskAtPoint(0, 0); ok {
		t.Errorf("TaskAtPoint with an un-sized region = (%q, %v), want ( , false)", id, ok)
	}
}

func TestManager_TaskAtPoint_LayoutRejectDegrades(t *testing.T) {
	fake := newFakePTY(t)
	// layout: columns with MinWidth 80 — hard-rejects rather than reflowing.
	m := NewManager(WithStarter(fake), WithStrategy(FlexColumns{}),
		WithConstraints(Constraints{MinWidth: 80, MinHeight: 5, Gutter: 1}))
	m.Resize(wideRegion) // room for two 80-col panes
	if _, err := m.Spawn(SpawnSpec{Command: fakeCmd("claude"), TaskID: "T-A"}); err != nil {
		t.Fatalf("spawn A: %v", err)
	}
	if _, err := m.Spawn(SpawnSpec{Command: fakeCmd("claude"), TaskID: "T-B"}); err != nil {
		t.Fatalf("spawn B: %v", err)
	}
	// Shrink so two columns at MinWidth 80 no longer fit. Resize swallows the
	// reject and keeps the old geometry, but records the new (too-small) region —
	// so TaskAtPoint recomputes against it, hits the same reject, and must degrade
	// to a miss without panicking.
	m.Resize(Rect{X: 0, Y: 0, W: 90, H: 24})
	if id, ok := m.TaskAtPoint(10, 10); ok {
		t.Errorf("TaskAtPoint under a rejected layout = (%q, %v), want ( , false)", id, ok)
	}
}
