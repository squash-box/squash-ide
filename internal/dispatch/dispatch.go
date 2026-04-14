package dispatch

import (
	"fmt"

	"github.com/squashbox/squash-ide/internal/slug"
	"github.com/squashbox/squash-ide/internal/spawner"
	"github.com/squashbox/squash-ide/internal/task"
	"github.com/squashbox/squash-ide/internal/taskops"
	"github.com/squashbox/squash-ide/internal/vault"
	"github.com/squashbox/squash-ide/internal/worktree"
)

// Result holds the output of a successful dispatch.
type Result struct {
	Branch       string
	WorktreePath string
}

// Run executes the full spawn workflow for a task: create worktree, move task
// to active, update board, append log, and spawn a terminal.
// vaultPath may contain ~ (it will be expanded).
func Run(vaultPath string, t task.Task) (Result, error) {
	if t.Status != "backlog" {
		return Result{}, fmt.Errorf("task %s has status %q — only backlog tasks can be spawned", t.ID, t.Status)
	}

	vaultRoot := vault.ExpandHome(vaultPath)

	// Resolve repo path
	repoPath := t.Repo
	if repoPath == "" {
		resolved, err := vault.ReadEntityRepo(vaultPath, t.Project)
		if err != nil {
			return Result{}, fmt.Errorf("resolving repo for project %s: %w", t.Project, err)
		}
		repoPath = resolved
	} else {
		repoPath = vault.ExpandHome(repoPath)
	}

	// Derive slug and branch
	taskSlug := slug.FromTitle(t.Title)
	branch := fmt.Sprintf("feat/%s-%s", t.ID, taskSlug)

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
	if err := spawner.Spawn(worktreePath, t.ID); err != nil {
		return Result{}, fmt.Errorf("spawning terminal: %w", err)
	}

	return Result{Branch: branch, WorktreePath: worktreePath}, nil
}
