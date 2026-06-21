package ui

import (
	"fmt"
	"testing"

	"github.com/squashbox/squash-ide/internal/config"
	"github.com/squashbox/squash-ide/internal/dispatch"
	"github.com/squashbox/squash-ide/internal/task"
)

// respawnVia builds a native model wired to mgr and drives the first
// tasksLoadedMsg with the given tasks, returning the updated model. The model
// starts with needsRespawn=true (set by New), so this is exactly the
// first-load respawn path (T-049).
func respawnVia(t *testing.T, mgr *stubManager, tasks []task.Task) Model {
	t.Helper()
	m := nativeModel(t, mgr)
	out, _ := m.Update(tasksLoadedMsg{tasks: tasks})
	return out.(Model)
}

func spawnedIDs(mgr *stubManager) []string {
	ids := make([]string, len(mgr.spawned))
	for i, s := range mgr.spawned {
		ids[i] = s.TaskID
	}
	return ids
}

// Happy path: two active tasks spawn two panes, in ID order, carrying the right
// metadata; the one-shot gate is cleared and list focus is retained.
func TestRespawn_TwoActiveSpawnsTwoPanes(t *testing.T) {
	mgr := newStubManager()
	tasks := []task.Task{
		{ID: "T-001", Title: "Backlog", Project: "proj", Status: "backlog"},
		{ID: "T-003", Title: "Active A", Project: "proj", Status: "active"},
		{ID: "T-007", Title: "Active B", Project: "proj", Status: "active"},
		{ID: "T-004", Title: "Blocked", Project: "proj", Status: "blocked"},
	}
	m := respawnVia(t, mgr, tasks)

	if len(mgr.spawned) != 2 {
		t.Fatalf("expected 2 spawned panes, got %d (%v)", len(mgr.spawned), spawnedIDs(mgr))
	}
	if got := mgr.spawned[0]; got.TaskID != "T-003" || got.Title != "Active A" || got.Project != "proj" {
		t.Errorf("first spawn = %+v, want T-003/Active A/proj", got)
	}
	if got := mgr.spawned[1]; got.TaskID != "T-007" || got.Title != "Active B" {
		t.Errorf("second spawn = %+v, want T-007/Active B", got)
	}
	if m.needsRespawn {
		t.Error("needsRespawn should be cleared after the first load")
	}
	if m.paneFocused {
		t.Error("respawn must not move focus into a pane (list keeps focus)")
	}
	if m.statusIsErr {
		t.Error("a fully successful respawn should not flag an error")
	}
	if m.statusMsg != "respawned 2 active pane(s)" {
		t.Errorf("statusMsg = %q, want %q", m.statusMsg, "respawned 2 active pane(s)")
	}
}

// One active task: the SpawnSpec's command Dir is the resolved worktree path and
// the argv is the config-default `claude /implement T-NNN`.
func TestRespawn_OneActive_CommandDirAndArgv(t *testing.T) {
	mgr := newStubManager()
	tk := task.Task{ID: "T-003", Title: "Active task", Project: "proj", Status: "active", Repo: "/repo/proj"}
	m := respawnVia(t, mgr, []task.Task{tk})

	if len(mgr.spawned) != 1 {
		t.Fatalf("expected 1 spawned pane, got %d", len(mgr.spawned))
	}
	cmd := mgr.spawned[0].Command
	if cmd == nil {
		t.Fatal("spawn spec carried a nil command")
	}

	// Repo set => resolveRepo returns it directly, so WorktreePathFor resolves
	// without touching the (fake) vault.
	wantDir, err := dispatch.WorktreePathFor(m.cfg, tk)
	if err != nil {
		t.Fatalf("WorktreePathFor errored unexpectedly: %v", err)
	}
	if cmd.Dir != wantDir {
		t.Errorf("Command.Dir = %q, want %q", cmd.Dir, wantDir)
	}

	wantArgv := []string{"claude", "/implement T-003"}
	if len(cmd.Args) != len(wantArgv) {
		t.Fatalf("argv = %v, want %v", cmd.Args, wantArgv)
	}
	for i := range wantArgv {
		if cmd.Args[i] != wantArgv[i] {
			t.Errorf("argv[%d] = %q, want %q", i, cmd.Args[i], wantArgv[i])
		}
	}
}

// Zero active tasks: no panes, no panic, gate cleared, no status churn.
func TestRespawn_ZeroActive(t *testing.T) {
	mgr := newStubManager()
	tasks := []task.Task{
		{ID: "T-001", Title: "Backlog", Project: "proj", Status: "backlog"},
		{ID: "T-004", Title: "Blocked", Project: "proj", Status: "blocked"},
	}
	m := respawnVia(t, mgr, tasks)

	if len(mgr.spawned) != 0 {
		t.Errorf("expected no spawns with zero active tasks, got %v", spawnedIDs(mgr))
	}
	if m.needsRespawn {
		t.Error("needsRespawn should be cleared even when there is nothing to respawn")
	}
	if m.statusMsg != "" {
		t.Errorf("no-op respawn should leave statusMsg empty, got %q", m.statusMsg)
	}
}

// Mixed statuses: only active tasks spawn; backlog and blocked are absent.
func TestRespawn_MixedOnlyActive(t *testing.T) {
	mgr := newStubManager()
	tasks := []task.Task{
		{ID: "T-001", Title: "Backlog", Project: "proj", Status: "backlog"},
		{ID: "T-003", Title: "Active", Project: "proj", Status: "active"},
		{ID: "T-004", Title: "Blocked", Project: "proj", Status: "blocked"},
		{ID: "T-005", Title: "Done", Project: "proj", Status: "done"},
	}
	respawnVia(t, mgr, tasks)

	got := spawnedIDs(mgr)
	if len(got) != 1 || got[0] != "T-003" {
		t.Errorf("spawned = %v, want only [T-003]", got)
	}
}

// Active tasks supplied out of ID order spawn in ascending-ID order.
func TestRespawn_DeterministicOrder(t *testing.T) {
	mgr := newStubManager()
	tasks := []task.Task{
		{ID: "T-009", Title: "C", Project: "proj", Status: "active"},
		{ID: "T-002", Title: "A", Project: "proj", Status: "active"},
		{ID: "T-005", Title: "B", Project: "proj", Status: "active"},
	}
	respawnVia(t, mgr, tasks)

	got := spawnedIDs(mgr)
	want := []string{"T-002", "T-005", "T-009"}
	if len(got) != len(want) {
		t.Fatalf("spawned = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("spawn order = %v, want %v", got, want)
			break
		}
	}
}

// An unresolvable worktree (no Repo + fake vault) spawns with Command.Dir == ""
// rather than skipping the task — mirrors tmux respawnActive's cwd="" fallback.
func TestRespawn_WorktreeUnresolvable_EmptyDir(t *testing.T) {
	mgr := newStubManager()
	// project "proj" has no entity in /fake/vault and Repo is empty, so
	// WorktreePathFor errors and respawn falls back to cwd="".
	tk := task.Task{ID: "T-003", Title: "Active", Project: "proj", Status: "active"}
	respawnVia(t, mgr, []task.Task{tk})

	if len(mgr.spawned) != 1 {
		t.Fatalf("task with an unresolvable worktree should still spawn, got %d", len(mgr.spawned))
	}
	if dir := mgr.spawned[0].Command.Dir; dir != "" {
		t.Errorf("Command.Dir = %q, want empty on unresolvable worktree", dir)
	}
}

// Every Spawn fails: all active tasks are still attempted, nothing is rolled
// back (no CloseByTask), tasks stay active, and the status reflects 0/N.
func TestRespawn_AllSpawnFail_NoRollback(t *testing.T) {
	mgr := newStubManager()
	mgr.spawnErr = fmt.Errorf("pane: layout rejected new pane")
	tasks := []task.Task{
		{ID: "T-003", Title: "A", Project: "proj", Status: "active"},
		{ID: "T-007", Title: "B", Project: "proj", Status: "active"},
	}
	m := respawnVia(t, mgr, tasks)

	if len(mgr.spawned) != 2 {
		t.Errorf("all active tasks should be attempted, got %v", spawnedIDs(mgr))
	}
	if len(mgr.closedTasks) != 0 {
		t.Errorf("respawn must not roll back / close any pane, got closed %v", mgr.closedTasks)
	}
	// No task demoted: both remain active in the model's loaded set.
	activeCount := 0
	for _, tk := range m.allTasks {
		if tk.Status == "active" {
			activeCount++
		}
	}
	if activeCount != 2 {
		t.Errorf("active tasks must stay active after spawn failure, got %d active", activeCount)
	}
	if m.statusMsg != "respawned 0/2 active pane(s)" {
		t.Errorf("statusMsg = %q, want %q", m.statusMsg, "respawned 0/2 active pane(s)")
	}
}

// One task failing does not abort the others: with the first task's Spawn
// failing, the second still spawns and the status reflects the partial count.
func TestRespawn_PartialFailure_OthersStillSpawn(t *testing.T) {
	mgr := newStubManager()
	mgr.spawnErrFor = map[string]error{"T-003": fmt.Errorf("pty error")}
	tasks := []task.Task{
		{ID: "T-003", Title: "A", Project: "proj", Status: "active"},
		{ID: "T-007", Title: "B", Project: "proj", Status: "active"},
	}
	m := respawnVia(t, mgr, tasks)

	// Both attempted (the stub appends before returning the error).
	got := spawnedIDs(mgr)
	if len(got) != 2 || got[0] != "T-003" || got[1] != "T-007" {
		t.Errorf("both tasks should be attempted in order, got %v", got)
	}
	if len(mgr.closedTasks) != 0 {
		t.Errorf("a partial failure must not roll back, got closed %v", mgr.closedTasks)
	}
	if m.statusMsg != "respawned 1/2 active pane(s)" {
		t.Errorf("statusMsg = %q, want %q", m.statusMsg, "respawned 1/2 active pane(s)")
	}
}

// One-shot gate: a second tasksLoadedMsg does not respawn again.
func TestRespawn_OneShotGate(t *testing.T) {
	mgr := newStubManager()
	tasks := []task.Task{{ID: "T-003", Title: "A", Project: "proj", Status: "active"}}
	m := respawnVia(t, mgr, tasks)
	if len(mgr.spawned) != 1 {
		t.Fatalf("first load should spawn once, got %d", len(mgr.spawned))
	}

	out, _ := m.Update(tasksLoadedMsg{tasks: tasks})
	_ = out.(Model)
	if len(mgr.spawned) != 1 {
		t.Errorf("second load must not respawn (one-shot gate), got %d total", len(mgr.spawned))
	}
}

// Regression: the tmux path is unchanged — a non-native model with RespawnFunc
// set still invokes it exactly once on first load, and respawnActivePanes (which
// needs a manager) is never reached.
func TestRespawn_TmuxPathUnchanged(t *testing.T) {
	cfg := config.Defaults() // engine defaults to tmux
	cfg.Vault = "/fake/vault"
	m := New(cfg)
	if m.engineNative {
		t.Fatal("precondition: model should be tmux-engine")
	}
	calls := 0
	var sawTasks []task.Task
	m.RespawnFunc = func(tasks []task.Task) {
		calls++
		sawTasks = tasks
	}
	tasks := []task.Task{{ID: "T-003", Title: "A", Project: "proj", Status: "active"}}

	out, _ := m.Update(tasksLoadedMsg{tasks: tasks})
	um := out.(Model)
	if calls != 1 {
		t.Errorf("tmux RespawnFunc should run exactly once, got %d", calls)
	}
	if len(sawTasks) != 1 || sawTasks[0].ID != "T-003" {
		t.Errorf("RespawnFunc should receive the loaded tasks, got %v", sawTasks)
	}
	if um.needsRespawn {
		t.Error("needsRespawn should be cleared after the tmux respawn")
	}

	// A second load does not re-fire the tmux callback either.
	out, _ = um.Update(tasksLoadedMsg{tasks: tasks})
	if calls != 1 {
		t.Errorf("tmux RespawnFunc one-shot gate broken, got %d calls", calls)
	}
}

// No tmux coupling: respawning active panes in native mode never sets the
// tmux-only tooNarrow/compact state (the native engine owns its own layout).
func TestRespawn_NoTmuxCoupling(t *testing.T) {
	mgr := newStubManager()
	tasks := []task.Task{
		{ID: "T-003", Title: "A", Project: "proj", Status: "active"},
		{ID: "T-007", Title: "B", Project: "proj", Status: "active"},
	}
	m := respawnVia(t, mgr, tasks)

	if m.tooNarrow {
		t.Error("native respawn must not set tooNarrow (tmux-only state)")
	}
	if m.compact {
		t.Error("native respawn must not set compact (tmux-only state)")
	}
}
