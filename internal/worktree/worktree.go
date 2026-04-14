package worktree

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

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

	// Worktree path: <repo-parent>/worktrees/<branch>
	worktreePath := filepath.Join(filepath.Dir(repoPath), "worktrees", branch)

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
