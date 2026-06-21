package pane

import (
	"testing"
)

// spawnN spawns n panes with task ids T-1..T-n into a manager sized to region.
func spawnN(t *testing.T, m *Manager, n int) []*Pane {
	t.Helper()
	for i := 1; i <= n; i++ {
		if _, err := m.Spawn(SpawnSpec{Command: fakeCmd("claude"), TaskID: taskID(i)}); err != nil {
			t.Fatalf("Spawn %d: %v", i, err)
		}
	}
	return m.Panes()
}

func taskID(i int) string { return "T-" + string(rune('0'+i)) }

// TestComputeRects_CollapseReflows: collapsing the middle pane shrinks it to a
// strip and the siblings reflow to share the freed width — total still fills the
// region.
func TestComputeRects_CollapseReflows(t *testing.T) {
	fake := newFakePTY(t)
	m := NewManager(WithStarter(fake))
	region := Rect{W: 400, H: 40}
	m.Resize(region)
	panes := spawnN(t, m, 3)

	// No collapse: equal-ish columns.
	base, axis, err := computeRects(FlexColumns{}, region, panes, map[string]bool{}, m.constraints)
	if err != nil || axis != axisColumns {
		t.Fatalf("base computeRects err=%v axis=%v", err, axis)
	}

	collapsed := map[string]bool{panes[1].id: true}
	rects, _, err := computeRects(FlexColumns{}, region, panes, collapsed, m.constraints)
	if err != nil {
		t.Fatalf("collapsed computeRects: %v", err)
	}
	if rects[1].W != collapsedStripW {
		t.Errorf("collapsed pane width = %d, want strip %d", rects[1].W, collapsedStripW)
	}
	// Siblings reflow: each is wider than its even-split baseline.
	if rects[0].W <= base[0].W {
		t.Errorf("sibling didn't reflow wider: %d (base %d)", rects[0].W, base[0].W)
	}
	// Panes + strips + gutters fill the region exactly.
	used := rects[0].W + rects[1].W + rects[2].W + 3 // 3 gutters
	if used != region.W {
		t.Errorf("widths+gutters used %d, region is %d", used, region.W)
	}
}

// TestComputeRects_AllCollapsed: every pane is a strip; no panic, region shows
// strips only.
func TestComputeRects_AllCollapsed(t *testing.T) {
	fake := newFakePTY(t)
	m := NewManager(WithStarter(fake))
	region := Rect{W: 400, H: 40}
	m.Resize(region)
	panes := spawnN(t, m, 3)

	collapsed := map[string]bool{panes[0].id: true, panes[1].id: true, panes[2].id: true}
	rects, _, err := computeRects(FlexColumns{}, region, panes, collapsed, m.constraints)
	if err != nil {
		t.Fatalf("all-collapsed computeRects: %v", err)
	}
	for i, r := range rects {
		if r.W != collapsedStripW || r.H != region.H {
			t.Errorf("strip[%d] = %dx%d, want %dx%d", i, r.W, r.H, collapsedStripW, region.H)
		}
	}
}

// TestToggleCollapse_RoundTrips: ToggleCollapse marks then clears the collapse
// state and never panics on render.
func TestToggleCollapse_RoundTrips(t *testing.T) {
	fake := newFakePTY(t)
	m := NewManager(WithStarter(fake))
	m.Resize(Rect{W: 400, H: 40})
	panes := spawnN(t, m, 2)
	id := panes[0].id

	m.ToggleCollapse(id)
	if !m.collapsed[id] {
		t.Fatal("ToggleCollapse should have collapsed the pane")
	}
	if out := m.Render(Rect{W: 400, H: 40}); out == "" {
		t.Error("render with a collapsed pane returned empty")
	}
	m.ToggleCollapse(id)
	if m.collapsed[id] {
		t.Error("ToggleCollapse should have expanded the pane back")
	}
	// Unknown id is a no-op.
	m.ToggleCollapse("pane-nope")
}

// TestToggleCollapseFocused_FallsBackToFirst: with no focus it collapses the
// first pane.
func TestToggleCollapseFocused_FallsBackToFirst(t *testing.T) {
	fake := newFakePTY(t)
	m := NewManager(WithStarter(fake))
	m.Resize(Rect{W: 400, H: 40})
	panes := spawnN(t, m, 2)

	m.ToggleCollapseFocused() // no focus -> first pane
	if !m.collapsed[panes[0].id] {
		t.Error("ToggleCollapseFocused with no focus should collapse the first pane")
	}
}

// TestFocusNextPrev_Wraps cycles focus through the panes in order, wrapping.
func TestFocusNextPrev_Wraps(t *testing.T) {
	fake := newFakePTY(t)
	m := NewManager(WithStarter(fake))
	m.Resize(Rect{W: 400, H: 40})
	panes := spawnN(t, m, 3)

	m.FocusNext() // none -> first
	if m.Focused().ID() != panes[0].ID() {
		t.Fatalf("FocusNext from none = %s, want first", m.Focused().ID())
	}
	m.FocusNext()
	m.FocusNext()
	if m.Focused().ID() != panes[2].ID() {
		t.Fatalf("after two more FocusNext = %s, want third", m.Focused().ID())
	}
	m.FocusNext() // wraps to first
	if m.Focused().ID() != panes[0].ID() {
		t.Fatalf("FocusNext wrap = %s, want first", m.Focused().ID())
	}
	m.FocusPrev() // wraps back to last
	if m.Focused().ID() != panes[2].ID() {
		t.Fatalf("FocusPrev wrap = %s, want third", m.Focused().ID())
	}
}

// TestSetStateByTask_BadgeOnly: SetStateByTask paints the input_required badge
// but NEVER moves focus (T-048 badge-only contract). Focus-follows-input is
// owned entirely by the UI; the manager has no view of user intent (modal open /
// ctrl+w dismissal), so it must not seize focus on a state change. The
// notify-click path (Focus/FocusByTask) is the only thing that moves focus.
func TestSetStateByTask_BadgeOnly(t *testing.T) {
	fake := newFakePTY(t)
	m := NewManager(WithStarter(fake))
	m.Resize(Rect{W: 400, H: 40})
	panes := spawnN(t, m, 2)

	// Precondition: spawning does not auto-focus a pane.
	if f := m.Focused(); f != nil {
		t.Fatalf("precondition: focus = %s, want none after spawn", f.ID())
	}

	m.SetStateByTask("T-2", StateInputRequired)

	if f := m.Focused(); f != nil {
		t.Errorf("badge-only: focus = %s, want none (manager must not steal focus)", f.ID())
	}
	if panes[1].State() != StateInputRequired {
		t.Errorf("badge not updated: state = %s, want input_required", panes[1].State())
	}
}

// TestSetStateByTask_TwoSimultaneous_BadgeOnly: two panes paused
// near-simultaneously both get their badge and neither is focused — the manager
// renders, the UI decides surfacing (T-048).
func TestSetStateByTask_TwoSimultaneous_BadgeOnly(t *testing.T) {
	fake := newFakePTY(t)
	m := NewManager(WithStarter(fake))
	m.Resize(Rect{W: 400, H: 40})
	panes := spawnN(t, m, 2)

	m.SetStateByTask("T-1", StateInputRequired)
	m.SetStateByTask("T-2", StateInputRequired)

	if f := m.Focused(); f != nil {
		t.Errorf("badge-only: focus = %s, want none (no steal for either pane)", f.ID())
	}
	if panes[0].State() != StateInputRequired || panes[1].State() != StateInputRequired {
		t.Errorf("both badges should be input_required: T-1=%s T-2=%s", panes[0].State(), panes[1].State())
	}
}

// TestTick_SafeAfterClose: a badge-blink tick after the last pane closed is a
// harmless no-op (no use-after-close panic), and toggles the blink phase.
func TestTick_SafeAfterClose(t *testing.T) {
	fake := newFakePTY(t)
	m := NewManager(WithStarter(fake))
	m.Resize(Rect{W: 400, H: 40})
	panes := spawnN(t, m, 1)
	if err := m.Close(panes[0].id); err != nil {
		t.Fatalf("Close: %v", err)
	}

	m.Tick() // must not panic
	if !m.blinkOn {
		t.Error("Tick should toggle blink on")
	}
	m.Tick()
	if m.blinkOn {
		t.Error("second Tick should toggle blink off")
	}
	// A late status write to a closed task is a no-op.
	m.SetStateByTask("T-1", StateInputRequired)
	if out := m.Render(Rect{W: 400, H: 40}); out != "" {
		t.Errorf("render with no panes = %q, want empty", out)
	}
}

// TestSetStrategy_Switches re-tiles under the new strategy without disturbing the
// pane set.
func TestSetStrategy_Switches(t *testing.T) {
	fake := newFakePTY(t)
	m := NewManager(WithStarter(fake))
	m.Resize(Rect{W: 400, H: 40})
	spawnN(t, m, 2)

	m.SetStrategy(StackRows{})
	if _, ok := m.strategy.(StackRows); !ok {
		t.Errorf("strategy = %T, want StackRows", m.strategy)
	}
	if len(m.Panes()) != 2 {
		t.Errorf("pane count changed after SetStrategy: %d, want 2", len(m.Panes()))
	}
	m.SetStrategy(nil) // ignored
	if _, ok := m.strategy.(StackRows); !ok {
		t.Error("nil SetStrategy should be ignored")
	}
}
