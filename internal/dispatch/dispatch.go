package dispatch

import (
	"fmt"
	"strings"

	"github.com/squashbox/squash-ide/internal/config"
	"github.com/squashbox/squash-ide/internal/slug"
	"github.com/squashbox/squash-ide/internal/spawner"
	"github.com/squashbox/squash-ide/internal/task"
	"github.com/squashbox/squash-ide/internal/taskops"
	"github.com/squashbox/squash-ide/internal/tmux"
	"github.com/squashbox/squash-ide/internal/vault"
	"github.com/squashbox/squash-ide/internal/worktree"
)

// Result holds the output of a successful dispatch.
type Result struct {
	Branch       string
	WorktreePath string
}

// Run executes the full spawn workflow for a task: create worktree, move task
// to active, update board, append log, and spawn a terminal using the
// configured terminal + spawn command (with templating).
func Run(cfg config.Config, t task.Task) (Result, error) {
	if t.Status != "backlog" {
		return Result{}, fmt.Errorf("task %s has status %q — only backlog tasks can be spawned", t.ID, t.Status)
	}

	vaultRoot := vault.ExpandHome(cfg.Vault)

	repoPath, err := resolveRepo(cfg, t)
	if err != nil {
		return Result{}, err
	}

	branch := BranchFor(t)

	// Create worktree
	worktreePath, err := worktree.Create(repoPath, branch)
	if err != nil {
		return Result{}, fmt.Errorf("creating worktree: %w", err)
	}

	// Move task to active
	if _, err := taskops.MoveToActive(vaultRoot, t); err != nil {
		return Result{}, fmt.Errorf("moving task to active: %w", err)
	}

	// Update board
	if err := taskops.UpdateBoard(vaultRoot, t); err != nil {
		return Result{}, fmt.Errorf("updating board: %w", err)
	}

	// Append log
	if err := taskops.AppendLog(vaultRoot, t, branch, worktreePath); err != nil {
		return Result{}, fmt.Errorf("appending to log: %w", err)
	}

	// Spawn terminal
	vars := map[string]string{
		"cwd":      worktreePath,
		"task_id":  t.ID,
		"worktree": worktreePath,
		"repo":     repoPath,
		"branch":   branch,
	}
	if err := spawner.SpawnWith(cfg, vars); err != nil {
		return Result{}, fmt.Errorf("spawning terminal: %w", err)
	}

	return Result{Branch: branch, WorktreePath: worktreePath}, nil
}

// Complete archives an active task: removes the worktree, moves the task
// file from active/ to archive/, updates the board and log. The branch is
// derived from the task title (same rule used by Run).
func Complete(cfg config.Config, t task.Task) error {
	if t.Status != "active" {
		return fmt.Errorf("task %s has status %q — only active tasks can be completed", t.ID, t.Status)
	}

	vaultRoot := vault.ExpandHome(cfg.Vault)

	repoPath, err := resolveRepo(cfg, t)
	if err != nil {
		return err
	}

	branch := BranchFor(t)

	if err := worktree.Remove(repoPath, branch); err != nil {
		return fmt.Errorf("removing worktree: %w", err)
	}
	if _, err := taskops.MoveToArchive(vaultRoot, t, branch, ""); err != nil {
		return fmt.Errorf("archiving task: %w", err)
	}
	if err := taskops.UpdateBoardComplete(vaultRoot, t); err != nil {
		return fmt.Errorf("updating board: %w", err)
	}
	if err := taskops.AppendLogComplete(vaultRoot, t, branch); err != nil {
		return fmt.Errorf("appending to log: %w", err)
	}
	return nil
}

// Block moves an active task to the blocked/ directory with a one-line
// reason, updates the board and log. The worktree is left in place so work
// can resume after unblocking.
func Block(cfg config.Config, t task.Task, reason string) error {
	if t.Status != "active" {
		return fmt.Errorf("task %s has status %q — only active tasks can be blocked", t.ID, t.Status)
	}
	if strings.TrimSpace(reason) == "" {
		return fmt.Errorf("block reason required")
	}

	vaultRoot := vault.ExpandHome(cfg.Vault)

	if _, err := taskops.MoveToBlocked(vaultRoot, t, reason); err != nil {
		return fmt.Errorf("moving task to blocked: %w", err)
	}
	if err := taskops.UpdateBoardBlock(vaultRoot, t); err != nil {
		return fmt.Errorf("updating board: %w", err)
	}
	if err := taskops.AppendLogBlock(vaultRoot, t, reason); err != nil {
		return fmt.Errorf("appending to log: %w", err)
	}
	return nil
}

// Deactivate moves an active task back to the backlog: removes the worktree
// and branch, moves the task file from active/ to backlog/, and updates the
// board and log. This is the inverse of Run (spawn).
func Deactivate(cfg config.Config, t task.Task) error {
	if t.Status != "active" {
		return fmt.Errorf("task %s has status %q — only active tasks can be deactivated", t.ID, t.Status)
	}

	vaultRoot := vault.ExpandHome(cfg.Vault)

	repoPath, err := resolveRepo(cfg, t)
	if err != nil {
		return err
	}

	branch := BranchFor(t)

	// Kill the task's tmux pane if one is running. Best-effort — the task
	// may not have a pane (e.g. spawned in OS-window mode), and lookup
	// failures shouldn't block the deactivation.
	if cfg.Tmux.Enabled && tmux.InSession() {
		tuiPane := tmux.CurrentPaneID()
		if pane, err := tmux.FindPaneByTask(tuiPane, t.ID); err == nil && pane != "" {
			_ = tmux.KillPane(pane)
		}
	}

	if err := worktree.Remove(repoPath, branch); err != nil {
		return fmt.Errorf("removing worktree: %w", err)
	}
	if _, err := taskops.MoveToBacklog(vaultRoot, t); err != nil {
		return fmt.Errorf("moving task to backlog: %w", err)
	}
	if err := taskops.UpdateBoardDeactivate(vaultRoot, t); err != nil {
		return fmt.Errorf("updating board: %w", err)
	}
	if err := taskops.AppendLogDeactivate(vaultRoot, t, branch); err != nil {
		return fmt.Errorf("appending to log: %w", err)
	}
	return nil
}

// BranchFor returns the conventional feature-branch name for a task.
func BranchFor(t task.Task) string {
	return fmt.Sprintf("feat/%s-%s", t.ID, slug.FromTitle(t.Title))
}

// WorktreePathFor resolves the repo for a task and returns the expected
// worktree path (regardless of whether the worktree currently exists).
// Useful for the TUI detail view on active tasks.
func WorktreePathFor(cfg config.Config, t task.Task) (string, error) {
	repoPath, err := resolveRepo(cfg, t)
	if err != nil {
		return "", err
	}
	return worktree.Path(repoPath, BranchFor(t)), nil
}

// resolveRepo returns the absolute repo path for a task: the task's repo
// field takes precedence, falling back to the project's entity page.
func resolveRepo(cfg config.Config, t task.Task) (string, error) {
	if t.Repo != "" {
		return vault.ExpandHome(t.Repo), nil
	}
	resolved, err := vault.ReadEntityRepo(cfg.Vault, t.Project)
	if err != nil {
		return "", fmt.Errorf("resolving repo for project %s: %w", t.Project, err)
	}
	return resolved, nil
}
