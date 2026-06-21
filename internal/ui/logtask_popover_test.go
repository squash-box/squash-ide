package ui

import (
	"errors"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/squashbox/squash-ide/internal/config"
	"github.com/squashbox/squash-ide/internal/pane"
)

// withClaudeOnPath forces the /log-task pre-flight to succeed for the duration of
// a test, so the popover/exec paths run without `claude` actually installed.
func withClaudeOnPath(t *testing.T) {
	t.Helper()
	orig := lookPath
	lookPath = func(string) (string, error) { return "/usr/bin/claude", nil }
	t.Cleanup(func() { lookPath = orig })
}

// submitValidForm opens a valid new-task form and presses Enter to submit it,
// returning the resulting model and command.
func submitValidForm(t *testing.T, m Model) (Model, tea.Cmd) {
	t.Helper()
	f := newNewTaskForm()
	f.name = "My new task"
	f.repo = "squash-ide"
	m.creatingTask = &f
	out, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	return out.(Model), cmd
}

// tmuxModel builds a tmux-engine model with a stub manager attached (production
// tmux mode has no manager) so a test can prove the submit fork never touches it.
func tmuxModel(t *testing.T, mgr paneManager) Model {
	t.Helper()
	cfg := config.Defaults() // engine defaults to tmux
	cfg.Vault = "/fake/vault"
	m := New(cfg)
	m.manager = mgr
	m.allTasks = testTasks()
	m.width = 200
	m.height = 50
	m.buildItems()
	m.applyFilter()
	m.clampCursor()
	return m
}

// Core (T-050): native submit opens the popover via SpawnModal and sets the
// modal-state marker — it does not hand off to tea.ExecProcess.
func TestNativeSubmit_OpensPopover(t *testing.T) {
	withClaudeOnPath(t)
	mgr := newStubManager()
	m := nativeModel(t, mgr)

	m, cmd := submitValidForm(t, m)

	if len(mgr.modalSpawns) != 1 {
		t.Fatalf("expected exactly one SpawnModal call, got %d", len(mgr.modalSpawns))
	}
	spec := mgr.modalSpawns[0]
	if spec.TaskID != "log-task" {
		t.Errorf("modal TaskID = %q, want %q", spec.TaskID, "log-task")
	}
	if spec.Command == nil || len(spec.Command.Args) == 0 || spec.Command.Args[0] != "claude" {
		t.Errorf("modal command = %v, want a claude invocation", spec.Command)
	}
	if !m.logTaskPopover {
		t.Error("logTaskPopover should be set after a native submit")
	}
	if !m.dispatching {
		t.Error("dispatching should be set while the popover runs")
	}
	if m.creatingTask != nil {
		t.Error("the form should be cleared on submit")
	}
	if cmd == nil {
		t.Error("native submit should return a command (waitForModalExit + repaint)")
	}
}

// The popover box passed to SpawnModal is centered and within the terminal.
func TestNativeSubmit_PopoverBoxCentered(t *testing.T) {
	withClaudeOnPath(t)
	mgr := newStubManager()
	m := nativeModel(t, mgr) // width 200, height 50

	m, _ = submitValidForm(t, m)

	box := mgr.modalBoxes[0]
	if box.W <= 0 || box.H <= 0 {
		t.Fatalf("popover box has non-positive size: %+v", box)
	}
	if box.X+box.W > m.width || box.Y+box.H > m.height {
		t.Errorf("popover box %+v exceeds terminal %dx%d", box, m.width, m.height)
	}
	// Cap honoured (width 200 → 90% = 180, capped to maxPopoverWidth).
	if box.W != maxPopoverWidth {
		t.Errorf("box width = %d, want capped %d", box.W, maxPopoverWidth)
	}
}

// Regression guard: tmux submit keeps the tea.ExecProcess path and never spawns
// a modal.
func TestTmuxSubmit_NoPopover(t *testing.T) {
	withClaudeOnPath(t)
	mgr := newStubManager()
	m := tmuxModel(t, mgr)

	m, cmd := submitValidForm(t, m)

	if len(mgr.modalSpawns) != 0 {
		t.Errorf("tmux submit must not call SpawnModal, got %d calls", len(mgr.modalSpawns))
	}
	if m.logTaskPopover {
		t.Error("tmux submit must not set logTaskPopover")
	}
	if cmd == nil {
		t.Error("tmux submit should still return the runLogTask command")
	}
}

// logTaskDoneMsg under native closes the modal, clears the marker, and reloads.
func TestLogTaskDone_NativeClosesAndReloads(t *testing.T) {
	mgr := newStubManager()
	m := nativeModel(t, mgr)
	m.logTaskPopover = true
	m.dispatching = true
	mgr.hasModal = true

	out, cmd := m.Update(logTaskDoneMsg{})
	m = out.(Model)

	if mgr.closeModalCount != 1 {
		t.Errorf("CloseModal calls = %d, want 1", mgr.closeModalCount)
	}
	if m.logTaskPopover {
		t.Error("logTaskPopover should be cleared on done")
	}
	if m.dispatching {
		t.Error("dispatching should be cleared on done")
	}
	if cmd == nil {
		t.Error("done should trigger a vault reload command")
	}
}

// While the popover is open, an ordinary key is encoded and forwarded to the
// modal child — it must not move the list cursor.
func TestPopoverKey_ForwardsToModal(t *testing.T) {
	mgr := newStubManager()
	m := nativeModel(t, mgr)
	m.logTaskPopover = true
	startCursor := m.cursor

	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	m = out.(Model)

	if len(mgr.writtenToModal) != 1 || string(mgr.writtenToModal[0]) != "a" {
		t.Errorf("expected forwarded 'a', got %q", mgr.writtenToModal)
	}
	if m.cursor != startCursor {
		t.Error("key leaked into the list while the popover was open")
	}
}

// ctrl+c while the popover is open is forwarded to the child (interrupt), not
// treated as a squash-ide quit.
func TestPopoverKey_CtrlCForwardedNotQuit(t *testing.T) {
	mgr := newStubManager()
	m := nativeModel(t, mgr)
	m.logTaskPopover = true

	out, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m = out.(Model)

	if cmd != nil {
		t.Error("ctrl+c in the popover must not return a quit command")
	}
	if len(mgr.writtenToModal) != 1 || len(mgr.writtenToModal[0]) != 1 || mgr.writtenToModal[0][0] != 3 {
		t.Errorf("ctrl+c should forward byte 0x03 to the child, got %q", mgr.writtenToModal)
	}
	if !m.logTaskPopover {
		t.Error("ctrl+c should not close the popover")
	}
}

// ctrl+\ force-closes a wedged popover and returns to the list.
func TestPopoverKey_CtrlBackslashForceCloses(t *testing.T) {
	mgr := newStubManager()
	m := nativeModel(t, mgr)
	m.logTaskPopover = true
	m.dispatching = true
	mgr.hasModal = true

	out, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlBackslash})
	m = out.(Model)

	if mgr.closeModalCount != 1 {
		t.Errorf("ctrl+\\ should CloseModal once, got %d", mgr.closeModalCount)
	}
	if m.logTaskPopover {
		t.Error("ctrl+\\ should clear logTaskPopover")
	}
	if m.dispatching {
		t.Error("ctrl+\\ should clear dispatching")
	}
	if cmd == nil {
		t.Error("ctrl+\\ should reload the vault")
	}
	// A key after force-close routes to the list (cursor moves), not a dead pane.
	mgr.writtenToModal = nil
	out, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	m = out.(Model)
	if len(mgr.writtenToModal) != 0 {
		t.Error("keys after force-close should not forward to the modal")
	}
}

// Exception: a LookPath failure under native surfaces logTaskErrMsg and opens no
// popover.
func TestNativeSubmit_LookPathFails(t *testing.T) {
	orig := lookPath
	lookPath = func(string) (string, error) { return "", errors.New("not found") }
	t.Cleanup(func() { lookPath = orig })

	mgr := newStubManager()
	m := nativeModel(t, mgr)

	m, cmd := submitValidForm(t, m)

	if len(mgr.modalSpawns) != 0 {
		t.Error("no modal should be spawned when claude is missing")
	}
	if m.logTaskPopover {
		t.Error("logTaskPopover must stay false on a LookPath failure")
	}
	if cmd == nil {
		t.Fatal("expected a command emitting logTaskErrMsg")
	}
	if _, ok := cmd().(logTaskErrMsg); !ok {
		t.Errorf("expected logTaskErrMsg, got %T", cmd())
	}
}

// Exception: a SpawnModal failure is wrapped into logTaskErrMsg; no popover, no
// dispatching.
func TestNativeSubmit_SpawnModalError(t *testing.T) {
	withClaudeOnPath(t)
	mgr := newStubManager()
	mgr.modalSpawnErr = errors.New("layout rejected")
	m := nativeModel(t, mgr)

	m, cmd := submitValidForm(t, m)

	if m.logTaskPopover {
		t.Error("logTaskPopover must stay false when SpawnModal fails")
	}
	if m.dispatching {
		t.Error("dispatching must stay false when SpawnModal fails")
	}
	if cmd == nil {
		t.Fatal("expected a command emitting logTaskErrMsg")
	}
	msg, ok := cmd().(logTaskErrMsg)
	if !ok {
		t.Fatalf("expected logTaskErrMsg, got %T", cmd())
	}
	// Feeding it back clears state cleanly.
	out, _ := m.Update(msg)
	m = out.(Model)
	if m.logTaskPopover || m.dispatching {
		t.Error("logTaskErrMsg handler should leave a clean state")
	}
}

// The popover counts as a modal state so focus-follows-input can't yank focus
// out from under it.
func TestPopover_InModalState(t *testing.T) {
	mgr := newStubManager()
	m := nativeModel(t, mgr)
	if m.inModalState() {
		t.Fatal("precondition: not modal before popover opens")
	}
	m.logTaskPopover = true
	if !m.inModalState() {
		t.Error("logTaskPopover should report inModalState() == true")
	}

	// Focus-follows-input is suppressed while the popover is open; the badge
	// still updates from the status pipeline.
	m = tickStatusInput(t, m, "T-003", pane.StateInputRequired)
	if m.paneFocused {
		t.Error("focus-follows-input must not steal focus while the popover is open")
	}
	if mgr.focusedTask != "" {
		t.Errorf("no FocusByTask expected while the popover is open, got %q", mgr.focusedTask)
	}
	if mgr.states["T-003"] != pane.StateInputRequired {
		t.Errorf("badge state = %q, want input_required (badge must still update)", mgr.states["T-003"])
	}
}

// nativeView must not panic when a modal is flagged open but the pane is nil
// (defensive guard) and must not draw the overlay then.
func TestNativeView_ModalNilPaneNoPanic(t *testing.T) {
	mgr := newStubManager()
	mgr.renderOut = "PANE-REGION-SENTINEL"
	mgr.hasModal = true // HasModal() true, but ModalPane() returns nil
	m := nativeModel(t, mgr)

	view := m.View() // must not panic
	if view == "" {
		t.Error("view should still render with a nil modal pane")
	}
}
