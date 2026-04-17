package worktree

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	runexec "github.com/squashbox/squash-ide/internal/exec"
)

// runner is the process runner used by Create/Remove. Swap in tests via
// SetRunner / the exported WithRunner variants.
var runner runexec.Runner = runexec.Default

// SetRunner swaps the package-level runner. Returns the previous runner so
// callers can restore it in cleanup. Intended for tests only.
func SetRunner(r runexec.Runner) runexec.Runner {
	prev := runner
	runner = r
	return prev
}

// Path returns the conventional worktree path for a branch inside a given
// repo's parent directory (<repo-parent>/worktrees/<branch>).
func Path(repoPath, branch string) string {
	return filepath.Join(filepath.Dir(repoPath), "worktrees", branch)
}

// Create fetches from origin and creates a new git worktree. Returns the
// absolute path to the created worktree.
func Create(repoPath, branch string) (string, error) {
	return CreateWith(runner, repoPath, branch)
}

// CreateWith is Create with an explicit Runner, for tests.
func CreateWith(r runexec.Runner, repoPath, branch string) (string, error) {
	if _, err := os.Stat(repoPath); err != nil {
		return "", fmt.Errorf("repo path %s: %w", repoPath, err)
	}

	ctx := context.Background()

	if _, err := r.Output(ctx, "git", "-C", repoPath, "fetch", "origin"); err != nil {
		return "", fmt.Errorf("git fetch origin: %w", err)
	}

	worktreePath := Path(repoPath, branch)

	if _, err := os.Stat(worktreePath); err == nil {
		return "", fmt.Errorf("worktree path already exists: %s", worktreePath)
	}

	if _, err := r.Output(ctx, "git", "-C", repoPath, "worktree", "add",
		worktreePath, "-b", branch, "origin/main"); err != nil {
		return "", fmt.Errorf("git worktree add: %w", err)
	}

	return worktreePath, nil
}

// Remove removes the worktree for the given branch and deletes the local
// branch. Missing worktree/branch is tolerated (idempotent from the TUI).
func Remove(repoPath, branch string) error {
	return RemoveWith(runner, repoPath, branch)
}

// RemoveWith is Remove with an explicit Runner, for tests.
func RemoveWith(r runexec.Runner, repoPath, branch string) error {
	if _, err := os.Stat(repoPath); err != nil {
		return fmt.Errorf("repo path %s: %w", repoPath, err)
	}

	ctx := context.Background()
	worktreePath := Path(repoPath, branch)

	if _, err := os.Stat(worktreePath); err == nil {
		if _, err := r.Output(ctx, "git", "-C", repoPath, "worktree", "remove", worktreePath); err != nil {
			// Retry with --force for dirty worktrees.
			if _, err2 := r.Output(ctx, "git", "-C", repoPath, "worktree", "remove", "--force", worktreePath); err2 != nil {
				return fmt.Errorf("git worktree remove %s: %w", worktreePath, err)
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat worktree path %s: %w", worktreePath, err)
	}

	// Delete the local branch. Missing branch is fine.
	if _, err := r.Output(ctx, "git", "-C", repoPath, "branch", "-D", branch); err != nil {
		msg := err.Error()
		if strings.Contains(msg, "not found") || strings.Contains(msg, "no branch named") {
			return nil
		}
		return fmt.Errorf("git branch -D %s: %w", branch, err)
	}

	return nil
}
