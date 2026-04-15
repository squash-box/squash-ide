package worktree

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Path returns the conventional worktree path for a branch inside a given
// repo's parent directory (<repo-parent>/worktrees/<branch>).
func Path(repoPath, branch string) string {
	return filepath.Join(filepath.Dir(repoPath), "worktrees", branch)
}

// Create fetches from origin and creates a new git worktree.
// repoPath is the main repo directory, branch is the new branch name.
// Returns the absolute path to the created worktree.
func Create(repoPath, branch string) (string, error) {
	if _, err := os.Stat(repoPath); err != nil {
		return "", fmt.Errorf("repo path %s: %w", repoPath, err)
	}

	// git fetch origin
	fetch := exec.Command("git", "-C", repoPath, "fetch", "origin")
	fetch.Stderr = os.Stderr
	if err := fetch.Run(); err != nil {
		return "", fmt.Errorf("git fetch origin: %w", err)
	}

	worktreePath := Path(repoPath, branch)

	if _, err := os.Stat(worktreePath); err == nil {
		return "", fmt.Errorf("worktree path already exists: %s", worktreePath)
	}

	// git worktree add <path> -b <branch> origin/main
	add := exec.Command("git", "-C", repoPath, "worktree", "add",
		worktreePath, "-b", branch, "origin/main")
	add.Stderr = os.Stderr
	if err := add.Run(); err != nil {
		return "", fmt.Errorf("git worktree add: %w", err)
	}

	return worktreePath, nil
}

// Remove removes the worktree for the given branch and deletes the local
// branch. If the worktree path does not exist (e.g. already removed), it is
// treated as a no-op — this matches the "cleanup is idempotent" expectation
// from the TUI. The local branch is deleted with -D; a missing branch is
// also tolerated.
func Remove(repoPath, branch string) error {
	if _, err := os.Stat(repoPath); err != nil {
		return fmt.Errorf("repo path %s: %w", repoPath, err)
	}

	worktreePath := Path(repoPath, branch)
	if _, err := os.Stat(worktreePath); err == nil {
		rm := exec.Command("git", "-C", repoPath, "worktree", "remove", worktreePath)
		var stderr strings.Builder
		rm.Stderr = &stderr
		if err := rm.Run(); err != nil {
			// Retry with --force if the working tree has modifications. We
			// surface the stderr so the caller can see what happened.
			forceRm := exec.Command("git", "-C", repoPath, "worktree", "remove", "--force", worktreePath)
			forceRm.Stderr = os.Stderr
			if err2 := forceRm.Run(); err2 != nil {
				return fmt.Errorf("git worktree remove %s: %w (stderr: %s)", worktreePath, err, stderr.String())
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat worktree path %s: %w", worktreePath, err)
	}

	// Delete the local branch. Missing branch is fine.
	del := exec.Command("git", "-C", repoPath, "branch", "-D", branch)
	var stderr strings.Builder
	del.Stderr = &stderr
	if err := del.Run(); err != nil {
		msg := stderr.String()
		if strings.Contains(msg, "not found") || strings.Contains(msg, "no branch named") {
			return nil
		}
		return fmt.Errorf("git branch -D %s: %w (stderr: %s)", branch, err, msg)
	}

	return nil
}
