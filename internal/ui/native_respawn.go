package ui

import (
	"fmt"
	"sort"

	"github.com/squashbox/squash-ide/internal/dispatch"
	"github.com/squashbox/squash-ide/internal/pane"
	"github.com/squashbox/squash-ide/internal/spawner"
	"github.com/squashbox/squash-ide/internal/task"
)

// respawnActivePanes re-creates a native pane for every task already in "active"
// status when the TUI starts, restoring the tmux engine's respawn-on-launch
// behaviour (respawnActive in cmd/squash-ide/main.go) for the in-process pane
// manager. It runs once, on the first tasksLoadedMsg, behind the needsRespawn
// gate — see the engine fork in the tasksLoadedMsg handler.
//
// Why it lives in the model, not main.go: the native manager is owned by the
// running tea.Program (Model.manager), so a pane can only be spawned from inside
// the model on the UI goroutine. The tmux path can use a free function because
// tmux panes live in a process outside the TUI; the native manager cannot. This
// mirrors the native Enter-spawn precedent in the dispatchDoneMsg handler.
//
// Command construction is shared with the Enter-spawn path
// (dispatch.WorktreePathFor + spawner.BuildSpawnCmd), so there is no second
// source of truth for how a task's process launches. No vault mutation: each
// task is already active and its worktree already exists — unlike dispatch.Run,
// which rejects non-backlog tasks and would re-mutate the vault.
//
// Best-effort and non-fatal: a per-task Spawn failure (a layout hard-reject
// under layout: columns, a PTY error, a vanished worktree) logs and continues
// to the next task. It never rolls a task back to backlog — the load-bearing
// contrast with the Enter path, where a failed spawn rolls a just-promoted task
// back to avoid an orphan. Here the tasks were *already* active from a prior
// session, so a rollback would silently demote real in-flight work. Focus is
// left on the list (manager.Spawn does not steal focus — [[T-031]]).
func (m *Model) respawnActivePanes() {
	var active []task.Task
	for _, t := range m.allTasks {
		if t.Status == "active" {
			active = append(active, t)
		}
	}
	if len(active) == 0 {
		return
	}
	// Deterministic left-to-right pane order across launches (and deterministic
	// test assertions on mgr.spawned). The tmux path inherits vault.ReadAll
	// order; native sorts explicitly because the pane order is directly
	// observable here.
	sort.Slice(active, func(i, j int) bool { return active[i].ID < active[j].ID })

	spawned := 0
	for _, t := range active {
		cwd, err := dispatch.WorktreePathFor(m.cfg, t)
		if err != nil {
			// Mirror tmux respawnActive: an unresolvable worktree spawns with
			// cwd="" rather than skipping the task.
			uidebugf("respawn: worktree unresolved for %s: %v", t.ID, err)
			cwd = ""
		}
		vars := map[string]string{
			"task_id": t.ID, "title": t.Title, "project": t.Project,
			"cwd": cwd, "worktree": cwd,
		}
		spec := pane.SpawnSpec{
			Command: spawner.BuildSpawnCmd(m.cfg, vars),
			TaskID:  t.ID,
			Title:   t.Title,
			Project: t.Project,
		}
		uidebugf("respawn: spawning %s (cwd=%q)", t.ID, cwd)
		if _, err := m.manager.Spawn(spec); err != nil {
			uidebugf("respawn: spawn failed for %s: %v", t.ID, err)
			continue
		}
		spawned++
	}

	if spawned == len(active) {
		m.statusMsg = fmt.Sprintf("respawned %d active pane(s)", spawned)
	} else {
		m.statusMsg = fmt.Sprintf("respawned %d/%d active pane(s)", spawned, len(active))
	}
	m.statusIsErr = false
}
