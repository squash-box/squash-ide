package ui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/squashbox/squash-ide/internal/config"
)

// withClaudeOnPath forces the /log-task pre-flight to succeed for the duration of
// a test, so the spawn/exec paths run without `claude` actually installed.
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

// Core (T-054): pressing `t` under native opens the add-task form as a floating
// overlay composited over the list + pane region — not a fullscreen takeover. The
// list chrome (the "▌ ACTIVE" header, to the left of the centered box) and the
// pane region behind it both survive.
func TestNativeNewTaskFormOverlays(t *testing.T) {
	mgr := newStubManager()
	mgr.renderOut = "PANE-REGION-SENTINEL"
	m := nativeModel(t, mgr)

	// `t` opens the form.
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("t")})
	m = out.(Model)
	if m.creatingTask == nil {
		t.Fatal("`t` should open the add-task form")
	}

	view := m.View()
	if !strings.Contains(view, "new task") {
		t.Error("native view should render the floating add-task form")
	}
	if !strings.Contains(view, "ACTIVE") {
		t.Error("list chrome (ACTIVE header) should remain visible behind the form")
	}
	if !strings.Contains(view, "PANE-REGION-SENTINEL") {
		t.Error("the pane region should remain visible behind the form (not a fullscreen takeover)")
	}
}

// ctrl+d on a valid form (native) spawns a regular tab via manager.Spawn keyed by
// the synthetic id log-task-1, clears the form in place, and leaves the modal open.
func TestNativeLogTaskSpawnsTabAndResetsForm(t *testing.T) {
	withClaudeOnPath(t)
	mgr := newStubManager()
	m := nativeModel(t, mgr)

	m, cmd := submitValidForm(t, m)

	if len(mgr.spawned) != 1 {
		t.Fatalf("expected exactly one Spawn, got %d", len(mgr.spawned))
	}
	spec := mgr.spawned[0]
	if spec.TaskID != "log-task-1" {
		t.Errorf("tab TaskID = %q, want log-task-1", spec.TaskID)
	}
	if spec.Title != "My new task" || spec.Project != "squash-ide" {
		t.Errorf("tab metadata = (%q, %q), want (My new task, squash-ide)", spec.Title, spec.Project)
	}
	if spec.Command == nil || len(spec.Command.Args) == 0 || spec.Command.Args[0] != "claude" {
		t.Errorf("tab command = %v, want a claude invocation", spec.Command)
	}
	if m.creatingTask == nil {
		t.Fatal("the add-task modal should stay open after a tab spawn")
	}
	if m.creatingTask.name != "" || m.creatingTask.typeIdx != 1 {
		t.Errorf("form should be reset (name=%q typeIdx=%d), want empty/feature", m.creatingTask.name, m.creatingTask.typeIdx)
	}
	if m.dispatching {
		t.Error("the tab path must not set the dispatching lock (concurrent adds)")
	}
	if cmd == nil {
		t.Error("a successful spawn should return the auto-close watcher command")
	}
}

// A second ctrl+d without closing the modal spawns log-task-2 (counter increments;
// both tabs coexist).
func TestNativeLogTaskConcurrentTabs(t *testing.T) {
	withClaudeOnPath(t)
	mgr := newStubManager()
	m := nativeModel(t, mgr)

	m, _ = submitValidForm(t, m)
	if len(mgr.spawned) != 1 || mgr.spawned[0].TaskID != "log-task-1" {
		t.Fatalf("first submit: spawned=%+v", mgr.spawned)
	}
	if m.creatingTask == nil {
		t.Fatal("modal should stay open after the first submit")
	}

	// Enter a second task into the still-open, cleared form.
	m.creatingTask.name = "Another task"
	m.creatingTask.repo = "squash-ide"
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = out.(Model)

	if len(mgr.spawned) != 2 || mgr.spawned[1].TaskID != "log-task-2" {
		t.Fatalf("second submit: spawned=%+v", mgr.spawned)
	}
	if m.creatingTask == nil {
		t.Error("modal should still be open after the second submit")
	}
}

// logTaskTabDoneMsg auto-closes the tab and reloads the vault.
func TestLogTaskTabDoneClosesPaneAndReloads(t *testing.T) {
	mgr := newStubManager()
	m := nativeModel(t, mgr)

	out, cmd := m.Update(logTaskTabDoneMsg{taskID: "log-task-1"})
	m = out.(Model)

	if len(mgr.closedTasks) != 1 || mgr.closedTasks[0] != "log-task-1" {
		t.Errorf("expected CloseByTask(log-task-1), got %v", mgr.closedTasks)
	}
	if !m.resetCursorOnLoad {
		t.Error("done should scroll the list to the freshly-filed task")
	}
	if cmd == nil {
		t.Error("done should return a vault-reload command")
	}
}

// An invalid form (repo empty) + ctrl+d does not submit: no Spawn, modal stays.
func TestNativeLogTaskInvalidNoSpawn(t *testing.T) {
	withClaudeOnPath(t)
	mgr := newStubManager()
	m := nativeModel(t, mgr)

	f := newNewTaskForm()
	f.name = "only a name" // repo empty -> invalid
	f.focus = fieldPrompt  // ctrl+d submits from the prompt field
	m.creatingTask = &f

	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	m = out.(Model)

	if len(mgr.spawned) != 0 {
		t.Errorf("an invalid form must not spawn a tab, got %d", len(mgr.spawned))
	}
	if m.creatingTask == nil {
		t.Error("the modal should stay open on an invalid submit")
	}
}

// esc closes the add-task modal and leaves any running log-task tabs alone.
func TestNativeNewTaskEscKeepsTabs(t *testing.T) {
	withClaudeOnPath(t)
	mgr := newStubManager()
	m := nativeModel(t, mgr)

	m, _ = submitValidForm(t, m)
	if len(mgr.spawned) != 1 {
		t.Fatalf("precondition: one tab spawned, got %d", len(mgr.spawned))
	}

	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = out.(Model)

	if m.creatingTask != nil {
		t.Error("esc should close the add-task modal")
	}
	if len(mgr.closedTasks) != 0 {
		t.Errorf("esc must not close a running tab, got %v", mgr.closedTasks)
	}
}

// waitForLogTaskExit on an unknown id (DoneByTask returns nil) is a no-op command.
func TestWaitForLogTaskExitNilChannel(t *testing.T) {
	mgr := newStubManager()
	m := nativeModel(t, mgr)

	if cmd := m.waitForLogTaskExit("log-task-404"); cmd != nil {
		t.Error("waitForLogTaskExit on an unknown id should return a nil (no-op) command")
	}
}

// Exception: claude missing from PATH surfaces logTaskErrMsg, opens no tab, and
// leaves the modal open with its contents for a retry.
func TestNativeLogTaskNoClaudeOnPath(t *testing.T) {
	orig := lookPath
	lookPath = func(string) (string, error) { return "", errors.New("not found") }
	t.Cleanup(func() { lookPath = orig })

	mgr := newStubManager()
	m := nativeModel(t, mgr)

	m, cmd := submitValidForm(t, m)

	if len(mgr.spawned) != 0 {
		t.Error("no tab should spawn when claude is missing")
	}
	if m.creatingTask == nil {
		t.Error("the modal should stay open (with its contents) on a claude-missing error")
	}
	if cmd == nil {
		t.Fatal("expected a command emitting logTaskErrMsg")
	}
	msg, ok := cmd().(logTaskErrMsg)
	if !ok {
		t.Fatalf("expected logTaskErrMsg, got %T", cmd())
	}
	out, _ := m.Update(msg)
	m = out.(Model)
	if !m.statusIsErr {
		t.Error("logTaskErrMsg should set the error status")
	}
	if m.creatingTask == nil {
		t.Error("logTaskErrMsg must not close the add-task modal under native")
	}
}

// Exception: a Spawn failure (layout reject / PTY error) surfaces logTaskErrMsg
// with no rollback (nothing was mutated) and the modal stays open.
func TestNativeLogTaskSpawnRejectNoRollback(t *testing.T) {
	withClaudeOnPath(t)
	mgr := newStubManager()
	mgr.spawnErr = errors.New("layout rejected new pane")
	m := nativeModel(t, mgr)

	m, cmd := submitValidForm(t, m)

	if m.creatingTask == nil {
		t.Error("the modal should stay open when Spawn fails")
	}
	if m.dispatching {
		t.Error("dispatching must never be set on the tab path")
	}
	if cmd == nil {
		t.Fatal("expected a command emitting logTaskErrMsg")
	}
	if _, ok := cmd().(logTaskErrMsg); !ok {
		t.Errorf("expected logTaskErrMsg, got %T", cmd())
	}
}

// Regression: the tmux / --no-tmux submit path is unchanged — it never spawns a
// native tab, clears the form, sets the dispatching lock, and hands off to
// runLogTask; the fullscreen form block still renders.
func TestTmuxNewTaskFullscreenUnchanged(t *testing.T) {
	withClaudeOnPath(t)
	mgr := newStubManager()
	m := tmuxModel(t, mgr)

	m, cmd := submitValidForm(t, m)

	if len(mgr.spawned) != 0 {
		t.Errorf("tmux submit must not spawn a native tab, got %d", len(mgr.spawned))
	}
	if m.creatingTask != nil {
		t.Error("tmux submit clears the form for the fullscreen handoff")
	}
	if !m.dispatching {
		t.Error("tmux submit sets the dispatching lock")
	}
	if cmd == nil {
		t.Error("tmux submit should return the runLogTask command")
	}

	// The fullscreen form block still renders for the tmux path.
	m2 := tmuxModel(t, mgr)
	f := newNewTaskForm()
	f.name, f.repo = "x", "y"
	m2.creatingTask = &f
	if view := m2.View(); !strings.Contains(view, "new task") {
		t.Error("tmux new-task form should render fullscreen")
	}
}
