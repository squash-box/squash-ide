package ui

import (
	"fmt"
	"os/exec"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/squashbox/squash-ide/internal/dispatch"
	"github.com/squashbox/squash-ide/internal/pane"
	"github.com/squashbox/squash-ide/internal/status"
	"github.com/squashbox/squash-ide/internal/task"
)

// Enter-to-spawn (native): the dispatch-done handler builds the SpawnSpec from
// the prepared command and calls manager.Spawn, and list focus is retained
// (the new pane must not steal it — [[T-031]]).
func TestNativeDispatchDone_SpawnsIntoManager(t *testing.T) {
	mgr := newStubManager()
	m := nativeModel(t, mgr)
	if m.paneFocused {
		t.Fatal("precondition: list should own focus")
	}

	msg := dispatchDoneMsg{
		taskID: "T-003",
		branch: "feat/T-003-active-task",
		task:   task.Task{ID: "T-003", Title: "Active task", Status: "backlog", Project: "proj"},
		native: &dispatch.NativeSpawn{
			Command: exec.Command("true"),
			TaskID:  "T-003",
			Title:   "Active task",
			Project: "proj",
		},
	}
	out, cmd := m.Update(msg)
	um := out.(Model)

	if len(mgr.spawned) != 1 {
		t.Fatalf("expected exactly one Spawn, got %d", len(mgr.spawned))
	}
	if got := mgr.spawned[0]; got.TaskID != "T-003" || got.Title != "Active task" || got.Project != "proj" {
		t.Errorf("Spawn spec = %+v, want T-003/Active task/proj", got)
	}
	if um.paneFocused {
		t.Error("spawn must not move focus to the new pane (list focus retained)")
	}
	if um.statusIsErr {
		t.Error("successful spawn should not flag an error")
	}
	if cmd == nil {
		t.Error("expected a reload command after spawn")
	}
}

// A Spawn failure after the vault was mutated rolls back: error surfaced, and a
// (rollback+reload) command is returned rather than leaving an orphan.
func TestNativeDispatchDone_SpawnFailureRollsBack(t *testing.T) {
	mgr := newStubManager()
	mgr.spawnErr = fmt.Errorf("pty: out of ptys")
	m := nativeModel(t, mgr)

	msg := dispatchDoneMsg{
		taskID: "T-003",
		task:   task.Task{ID: "T-003", Title: "Active task", Status: "backlog", Project: "proj"},
		native: &dispatch.NativeSpawn{Command: exec.Command("true"), TaskID: "T-003"},
	}
	out, cmd := m.Update(msg)
	um := out.(Model)

	if !um.statusIsErr {
		t.Error("spawn failure should surface as an error in the status line")
	}
	if cmd == nil {
		t.Error("spawn failure should return a rollback command")
	}
}

// Capacity pre-flight (native): when the region can't fit another pane, the
// confirm is rejected before any dispatch — no vault mutation, no Spawn.
func TestNativeConfirm_CapacityReject(t *testing.T) {
	mgr := newStubManager()
	mgr.canSpawn = false
	m := nativeModel(t, mgr)
	tk := task.Task{ID: "T-001", Title: "First task", Status: "backlog", Project: "proj"}
	m.confirming = &tk

	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	um := out.(Model)

	if um.dispatching {
		t.Error("capacity reject must not start a dispatch")
	}
	if !um.statusIsErr {
		t.Error("capacity reject should surface a status-line error")
	}
	if um.confirming != nil {
		t.Error("confirm dialog should be dismissed after a reject")
	}
	if len(mgr.spawned) != 0 {
		t.Error("no pane should be spawned on capacity reject")
	}
}

// Capacity allows: confirm proceeds to dispatch.
func TestNativeConfirm_CapacityAllowsDispatch(t *testing.T) {
	mgr := newStubManager() // canSpawn defaults true
	m := nativeModel(t, mgr)
	tk := task.Task{ID: "T-001", Title: "First task", Status: "backlog", Project: "proj"}
	m.confirming = &tk

	out, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	um := out.(Model)

	if !um.dispatching {
		t.Error("a fitting spawn should start dispatching")
	}
	if cmd == nil {
		t.Error("expected a dispatch command")
	}
}

// Complete (native): the pane is closed via the manager before the vault op,
// and focus returns to the list.
func TestNativeComplete_ClosesPane(t *testing.T) {
	mgr := newStubManager()
	m := nativeModel(t, mgr)
	m.paneFocused = true
	tk := task.Task{ID: "T-003", Title: "Active task", Status: "active", Project: "proj"}
	m.completing = &tk

	out, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	um := out.(Model)

	if len(mgr.closedTasks) != 1 || mgr.closedTasks[0] != "T-003" {
		t.Errorf("expected CloseByTask(T-003), got %v", mgr.closedTasks)
	}
	if um.paneFocused {
		t.Error("completing a task should return focus to the list")
	}
	if cmd == nil {
		t.Error("expected a complete command")
	}
}

// Deactivate (native): same pane teardown path as complete.
func TestNativeDeactivate_ClosesPane(t *testing.T) {
	mgr := newStubManager()
	m := nativeModel(t, mgr)
	tk := task.Task{ID: "T-003", Title: "Active task", Status: "active", Project: "proj"}
	m.deactivating = &tk

	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	_ = out.(Model)

	if len(mgr.closedTasks) != 1 || mgr.closedTasks[0] != "T-003" {
		t.Errorf("expected CloseByTask(T-003) on deactivate, got %v", mgr.closedTasks)
	}
}

// statusTick (native): pane badge states are driven from the status files via
// the manager, and a present-then-absent entry collapses to idle.
func TestNativeStatusTick_DrivesPaneState(t *testing.T) {
	mgr := newStubManager()
	m := nativeModel(t, mgr) // T-003 is the active task

	// First tick: an input_required entry → pane goes input_required.
	out, _ := m.Update(statusTickMsg{statuses: map[string]status.File{
		"T-003": {TaskID: "T-003", State: "input_required"},
	}})
	m = out.(Model)
	if mgr.states["T-003"] != "input_required" {
		t.Errorf("state after present entry = %q, want input_required", mgr.states["T-003"])
	}

	// Second tick: entry gone (stale) → collapses to idle (T-023 invariant).
	out, _ = m.Update(statusTickMsg{statuses: map[string]status.File{}})
	_ = out.(Model)
	if mgr.states["T-003"] != pane.StateIdle {
		t.Errorf("state after entry went absent = %q, want idle", mgr.states["T-003"])
	}
}

// statusTick (native): a pending notify-click focus request focuses the pane and
// hands keyboard focus to the region.
func TestNativeStatusTick_HonoursFocusRequest(t *testing.T) {
	mgr := newStubManager()
	m := nativeModel(t, mgr)

	out, _ := m.Update(statusTickMsg{focusTaskID: "T-003"})
	um := out.(Model)

	if mgr.focusedTask != "T-003" {
		t.Errorf("FocusByTask = %q, want T-003", mgr.focusedTask)
	}
	if !um.paneFocused {
		t.Error("a notify-click focus request should hand focus to the pane region")
	}
}
