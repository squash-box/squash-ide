package pane

import (
	"errors"
	"reflect"
	"testing"
)

// wideRegion is comfortably large enough to admit several panes at the default
// MinWidth, for tests that need the layout to succeed.
var wideRegion = Rect{X: 0, Y: 0, W: 400, H: 40}

func TestManager_Spawn(t *testing.T) {
	fake := newFakePTY(t)
	m := NewManager(WithStarter(fake))

	p, err := m.Spawn(SpawnSpec{
		Command: fakeCmd("claude", "/implement", "T-037"),
		TaskID:  "T-037",
		Title:   "internal/pane",
		Project: "squash-ide",
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if p == nil {
		t.Fatal("Spawn returned nil pane")
	}
	if got := m.Panes(); len(got) != 1 || got[0] != p {
		t.Fatalf("Panes() = %v, want the one spawned pane", got)
	}
	if fake.startCount() != 1 {
		t.Errorf("Start called %d times, want 1", fake.startCount())
	}
	if got, want := fake.records()[0].cmd.Args, []string{"claude", "/implement", "T-037"}; !reflect.DeepEqual(got, want) {
		t.Errorf("started command args = %v, want %v", got, want)
	}
	// Focus stays off the new pane (the [[T-031]] rule).
	if f := m.Focused(); f != nil {
		t.Errorf("Focused() = %v after spawn, want nil (focus must not jump to new pane)", f.ID())
	}
	if p.State() != StateWorking {
		t.Errorf("new pane state = %q, want %q", p.State(), StateWorking)
	}
}

func TestManager_SpawnKeepsExistingFocus(t *testing.T) {
	fake := newFakePTY(t)
	m := NewManager(WithStarter(fake))

	first, _ := m.Spawn(SpawnSpec{Command: fakeCmd("claude")})
	if err := m.Focus(first.ID()); err != nil {
		t.Fatalf("Focus: %v", err)
	}
	if _, err := m.Spawn(SpawnSpec{Command: fakeCmd("claude")}); err != nil {
		t.Fatalf("second Spawn: %v", err)
	}
	if f := m.Focused(); f == nil || f.ID() != first.ID() {
		t.Errorf("focus moved to the new pane; want it pinned on %s", first.ID())
	}
}

func TestManager_FocusUnknownIsNoOp(t *testing.T) {
	fake := newFakePTY(t)
	m := NewManager(WithStarter(fake))
	first, _ := m.Spawn(SpawnSpec{Command: fakeCmd("claude")})
	if err := m.Focus(first.ID()); err != nil {
		t.Fatalf("Focus: %v", err)
	}

	err := m.Focus("pane-does-not-exist")
	if !errors.Is(err, ErrUnknownPane) {
		t.Errorf("Focus(unknown) error = %v, want ErrUnknownPane", err)
	}
	if f := m.Focused(); f == nil || f.ID() != first.ID() {
		t.Errorf("focus changed on unknown id; want it left on %s", first.ID())
	}
}

func TestManager_LayoutRejectKillsHalfCreatedPane(t *testing.T) {
	fake := newFakePTY(t)
	// MinWidth 80, region only wide enough for one pane.
	m := NewManager(WithStarter(fake), WithConstraints(Constraints{MinWidth: 80, Gutter: 1}))
	m.Resize(Rect{W: 90, H: 24}) // avail 89 -> one 89-col pane fits, a second won't

	if _, err := m.Spawn(SpawnSpec{Command: fakeCmd("claude")}); err != nil {
		t.Fatalf("first Spawn should fit: %v", err)
	}

	_, err := m.Spawn(SpawnSpec{Command: fakeCmd("claude")})
	if !errors.Is(err, ErrInsufficientSpace) {
		t.Fatalf("second Spawn error = %v, want wrapped ErrInsufficientSpace", err)
	}
	// No half-created pane left behind...
	if got := m.Panes(); len(got) != 1 {
		t.Errorf("Panes() = %d after reject, want 1 (no orphan)", len(got))
	}
	// ...and the just-created child was killed (kill-on-reject).
	recs := fake.records()
	if len(recs) != 2 {
		t.Fatalf("Start called %d times, want 2 (one fits, one rejected)", len(recs))
	}
	if !recs[1].proc.wasKilled() {
		t.Error("rejected pane's process was not killed")
	}
}

func TestManager_SpawnPTYStartFailure(t *testing.T) {
	fake := newFakePTY(t)
	m := NewManager(WithStarter(fake))
	fake.failNextStart(errors.New("exec: \"claude\": executable file not found in $PATH"))

	p, err := m.Spawn(SpawnSpec{Command: fakeCmd("claude")})
	if err == nil {
		t.Fatal("Spawn should fail when the PTY start fails")
	}
	if p != nil {
		t.Error("Spawn returned a pane despite start failure")
	}
	if got := m.Panes(); len(got) != 0 {
		t.Errorf("Panes() = %d after start failure, want 0", len(got))
	}
}

func TestManager_CloseRemovesAndRefocuses(t *testing.T) {
	fake := newFakePTY(t)
	m := NewManager(WithStarter(fake))
	a, _ := m.Spawn(SpawnSpec{Command: fakeCmd("claude")})
	b, _ := m.Spawn(SpawnSpec{Command: fakeCmd("claude")})
	if err := m.Focus(b.ID()); err != nil {
		t.Fatalf("Focus: %v", err)
	}

	if err := m.Close(b.ID()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := m.Panes(); len(got) != 1 || got[0] != a {
		t.Errorf("after closing b, Panes() = %v, want [a]", got)
	}
	// Focus was on the closed pane; it should shift to a remaining pane.
	if f := m.Focused(); f == nil || f.ID() != a.ID() {
		t.Errorf("focus after closing focused pane = %v, want %s", f, a.ID())
	}
	if !fake.records()[1].proc.wasKilled() {
		t.Error("closed pane's process was not killed")
	}
}

func TestManager_CloseUnknown(t *testing.T) {
	m := NewManager(WithStarter(newFakePTY(t)))
	if err := m.Close("nope"); !errors.Is(err, ErrUnknownPane) {
		t.Errorf("Close(unknown) = %v, want ErrUnknownPane", err)
	}
}
