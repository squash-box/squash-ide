package pane

import (
	"strings"
	"testing"
	"time"
)

// modalBox is a popover geometry comfortably above the interior floors.
var modalBox = Rect{X: 10, Y: 5, W: 60, H: 24}

func TestManager_SpawnModal(t *testing.T) {
	fake := newFakePTY(t)
	m := NewManager(WithStarter(fake))
	m.Resize(wideRegion)

	p, err := m.SpawnModal(SpawnSpec{
		Command: fakeCmd("claude", "/log-task", "New task"),
		TaskID:  "log-task",
		Title:   "New task",
	}, modalBox)
	if err != nil {
		t.Fatalf("SpawnModal: %v", err)
	}
	if p == nil {
		t.Fatal("SpawnModal returned nil pane")
	}
	if !m.HasModal() {
		t.Error("HasModal() = false after SpawnModal")
	}
	if mp := m.ModalPane(); mp != p {
		t.Errorf("ModalPane() = %v, want the spawned modal", mp)
	}
	// The modal is NOT a tiled pane.
	if got := m.Panes(); len(got) != 0 {
		t.Errorf("Panes() = %v, want empty (modal must not be tiled)", got)
	}
	if got := m.Render(wideRegion); got != "" {
		t.Errorf("Render() = %q, want empty (modal is not rendered by the layout)", got)
	}
	if fake.startCount() != 1 {
		t.Errorf("Start called %d times, want 1", fake.startCount())
	}
}

func TestManager_SpawnModalRejectsSecond(t *testing.T) {
	fake := newFakePTY(t)
	m := NewManager(WithStarter(fake))

	first, err := m.SpawnModal(SpawnSpec{Command: fakeCmd("claude")}, modalBox)
	if err != nil {
		t.Fatalf("first SpawnModal: %v", err)
	}
	if _, err := m.SpawnModal(SpawnSpec{Command: fakeCmd("claude")}, modalBox); err == nil {
		t.Error("second SpawnModal should error while a modal is open")
	}
	if m.ModalPane() != first {
		t.Error("the existing modal must be left intact after a rejected second spawn")
	}
	if fake.startCount() != 1 {
		t.Errorf("Start called %d times, want 1 (no PTY for the rejected spawn)", fake.startCount())
	}
}

func TestManager_WriteToModal(t *testing.T) {
	fake := newFakePTY(t)
	m := NewManager(WithStarter(fake))

	// No-op before a modal exists.
	if n, err := m.WriteToModal([]byte("x")); n != 0 || err != nil {
		t.Errorf("WriteToModal with no modal = (%d, %v), want (0, nil)", n, err)
	}

	if _, err := m.SpawnModal(SpawnSpec{Command: fakeCmd("claude")}, modalBox); err != nil {
		t.Fatalf("SpawnModal: %v", err)
	}
	if _, err := m.WriteToModal([]byte("hello")); err != nil {
		t.Fatalf("WriteToModal: %v", err)
	}

	// The bytes reach the child's PTY — readable on the test end of the pair.
	testEnd := fake.records()[0].testEnd
	_ = testEnd.SetReadDeadline(time.Now().Add(time.Second))
	buf := make([]byte, 16)
	n, err := testEnd.Read(buf)
	if err != nil {
		t.Fatalf("reading forwarded bytes: %v", err)
	}
	if got := string(buf[:n]); got != "hello" {
		t.Errorf("modal child received %q, want %q", got, "hello")
	}
}

func TestManager_ModalDoneClosesOnChildExit(t *testing.T) {
	fake := newFakePTY(t)
	m := NewManager(WithStarter(fake))

	if _, err := m.SpawnModal(SpawnSpec{Command: fakeCmd("claude")}, modalBox); err != nil {
		t.Fatalf("SpawnModal: %v", err)
	}
	done := m.ModalDone()
	if done == nil {
		t.Fatal("ModalDone() = nil while a modal is open")
	}

	// Simulate the child exiting: killing the proc closes the socketpair peer,
	// EOFing the pane's read so readLoop closes done.
	_ = fake.records()[0].proc.Kill()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("ModalDone did not close after the child exited")
	}
}

func TestManager_ModalDoneNilWhenNoModal(t *testing.T) {
	m := NewManager(WithStarter(newFakePTY(t)))
	if ch := m.ModalDone(); ch != nil {
		t.Errorf("ModalDone() with no modal = %v, want nil", ch)
	}
}

func TestManager_CloseModalIdempotent(t *testing.T) {
	fake := newFakePTY(t)
	m := NewManager(WithStarter(fake))

	// No-op when nothing is open.
	if err := m.CloseModal(); err != nil {
		t.Errorf("CloseModal with no modal = %v, want nil", err)
	}

	if _, err := m.SpawnModal(SpawnSpec{Command: fakeCmd("claude")}, modalBox); err != nil {
		t.Fatalf("SpawnModal: %v", err)
	}
	if err := m.CloseModal(); err != nil {
		t.Fatalf("CloseModal: %v", err)
	}
	if m.HasModal() {
		t.Error("HasModal() = true after CloseModal")
	}
	if !fake.records()[0].proc.wasKilled() {
		t.Error("CloseModal did not kill the modal child")
	}
	// Second close is a clean no-op (double-close after auto-exit + force-close).
	if err := m.CloseModal(); err != nil {
		t.Errorf("second CloseModal = %v, want nil", err)
	}
}

func TestManager_SpawnModalRequiresCommand(t *testing.T) {
	m := NewManager(WithStarter(newFakePTY(t)))
	if _, err := m.SpawnModal(SpawnSpec{}, modalBox); err == nil {
		t.Error("SpawnModal with a nil Command should error")
	}
}

// Spawning a modal does not disturb the tiled panes, and they keep rendering.
func TestManager_ModalLeavesTiledPanesAlone(t *testing.T) {
	fake := newFakePTY(t)
	m := NewManager(WithStarter(fake))
	m.Resize(wideRegion)

	tiled, err := m.Spawn(SpawnSpec{Command: fakeCmd("claude"), TaskID: "T-1", Title: "tiled"})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if _, err := m.SpawnModal(SpawnSpec{Command: fakeCmd("claude"), TaskID: "log-task"}, modalBox); err != nil {
		t.Fatalf("SpawnModal: %v", err)
	}

	if got := m.Panes(); len(got) != 1 || got[0] != tiled {
		t.Errorf("Panes() = %v, want just the tiled pane", got)
	}
	if !strings.Contains(m.Render(wideRegion), "T-1") {
		t.Error("tiled pane should still render behind the modal")
	}
}
