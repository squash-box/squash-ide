package pane

import (
	"errors"
	"testing"
)

// The task-keyed helpers (CloseByTask / FocusByTask / SetStateByTask / CanSpawn)
// are the seam T-039 uses to route the spawn/teardown/notify-focus side effects
// through the manager keyed on task id rather than internal pane id.

func TestManager_CloseByTask(t *testing.T) {
	fake := newFakePTY(t)
	m := NewManager(WithStarter(fake))
	m.Resize(wideRegion)

	p, err := m.Spawn(SpawnSpec{Command: fakeCmd("claude"), TaskID: "T-039"})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if err := m.CloseByTask("T-039"); err != nil {
		t.Fatalf("CloseByTask: %v", err)
	}
	if got := m.Panes(); len(got) != 0 {
		t.Fatalf("Panes() = %d after CloseByTask, want 0", len(got))
	}
	if !p.proc.(*fakeProc).wasKilled() {
		t.Error("CloseByTask should have killed the child process")
	}
}

func TestManager_CloseByTaskUnknownIsErrUnknownPane(t *testing.T) {
	fake := newFakePTY(t)
	m := NewManager(WithStarter(fake))
	m.Resize(wideRegion)
	if _, err := m.Spawn(SpawnSpec{Command: fakeCmd("claude"), TaskID: "T-1"}); err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	err := m.CloseByTask("T-404")
	if !errors.Is(err, ErrUnknownPane) {
		t.Errorf("CloseByTask(unknown) = %v, want ErrUnknownPane", err)
	}
	// The existing pane must be untouched.
	if got := m.Panes(); len(got) != 1 {
		t.Errorf("Panes() = %d after closing an unknown task, want 1", len(got))
	}
}

func TestManager_FocusByTask(t *testing.T) {
	fake := newFakePTY(t)
	m := NewManager(WithStarter(fake))
	m.Resize(wideRegion)

	first, _ := m.Spawn(SpawnSpec{Command: fakeCmd("claude"), TaskID: "T-1"})
	second, _ := m.Spawn(SpawnSpec{Command: fakeCmd("claude"), TaskID: "T-2"})
	_ = first

	if err := m.FocusByTask("T-2"); err != nil {
		t.Fatalf("FocusByTask: %v", err)
	}
	if f := m.Focused(); f == nil || f.ID() != second.ID() {
		t.Errorf("Focused() = %v, want the T-2 pane", f)
	}

	if err := m.FocusByTask("T-404"); !errors.Is(err, ErrUnknownPane) {
		t.Errorf("FocusByTask(unknown) = %v, want ErrUnknownPane", err)
	}
	// Focus must be unchanged by the failed lookup.
	if f := m.Focused(); f == nil || f.ID() != second.ID() {
		t.Errorf("Focused() changed after a failed FocusByTask, = %v", f)
	}
}

func TestManager_SetStateByTask(t *testing.T) {
	fake := newFakePTY(t)
	m := NewManager(WithStarter(fake))
	m.Resize(wideRegion)

	p, _ := m.Spawn(SpawnSpec{Command: fakeCmd("claude"), TaskID: "T-1"})
	if p.State() != StateWorking {
		t.Fatalf("new pane state = %q, want %q", p.State(), StateWorking)
	}

	m.SetStateByTask("T-1", StateInputRequired)
	if p.State() != StateInputRequired {
		t.Errorf("after SetStateByTask state = %q, want %q", p.State(), StateInputRequired)
	}

	// Unknown task id is a silent no-op (no panic, existing pane unchanged).
	m.SetStateByTask("T-404", StateIdle)
	if p.State() != StateInputRequired {
		t.Errorf("unknown SetStateByTask changed an unrelated pane to %q", p.State())
	}
}

func TestManager_CanSpawn(t *testing.T) {
	fake := newFakePTY(t)
	m := NewManager(WithStarter(fake))

	// Before any Resize the region is zero — admit (Spawn tiles on first Resize).
	if !m.CanSpawn() {
		t.Error("CanSpawn should admit before the first Resize (zero region)")
	}

	// A region wide enough for exactly one pane at MinWidth 20 + Gutter 1 (per
	// pane) admits the first spawn but rejects a second.
	m.Resize(Rect{X: 0, Y: 0, W: 30, H: 20})
	if !m.CanSpawn() {
		t.Error("CanSpawn should admit the first pane in a 30-col region")
	}
	if _, err := m.Spawn(SpawnSpec{Command: fakeCmd("claude"), TaskID: "T-1"}); err != nil {
		t.Fatalf("first Spawn: %v", err)
	}
	if m.CanSpawn() {
		t.Error("CanSpawn should reject a second pane that would fall below MinWidth")
	}

	// Widen the region and the second pane fits again.
	m.Resize(wideRegion)
	if !m.CanSpawn() {
		t.Error("CanSpawn should admit a second pane once the region is wide")
	}
}

func TestManager_DoneByTask(t *testing.T) {
	fake := newFakePTY(t)
	m := NewManager(WithStarter(fake))
	m.Resize(wideRegion)

	p, err := m.Spawn(SpawnSpec{Command: fakeCmd("claude"), TaskID: "log-task-1"})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	done := m.DoneByTask("log-task-1")
	if done == nil {
		t.Fatal("DoneByTask should return the live pane's exit channel")
	}
	select {
	case <-done:
		t.Fatal("Done channel closed before the child exited")
	default:
	}

	// Killing the child (Close) EOFs the master and closes Done — the auto-close
	// watcher's wake-up edge.
	if err := m.Close(p.ID()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case <-done:
	default:
		t.Error("Done channel should be closed after the child exits")
	}

	// Unknown id returns a nil channel (waitForLogTaskExit treats it as a no-op).
	if ch := m.DoneByTask("nope"); ch != nil {
		t.Error("DoneByTask(unknown) should return a nil channel")
	}
}

func TestManager_HasPaneForTask(t *testing.T) {
	fake := newFakePTY(t)
	m := NewManager(WithStarter(fake))
	m.Resize(wideRegion)

	if m.HasPaneForTask("T-1") {
		t.Error("HasPaneForTask should be false before spawning")
	}
	if _, err := m.Spawn(SpawnSpec{Command: fakeCmd("claude"), TaskID: "T-1"}); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if !m.HasPaneForTask("T-1") {
		t.Error("HasPaneForTask should be true after spawning")
	}
	if m.HasPaneForTask("") {
		t.Error("HasPaneForTask must never match an empty task id")
	}
}
