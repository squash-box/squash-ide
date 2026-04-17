package worktree

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Typed errors surfaced by Create when the target path already exists on
// disk. Callers (dispatch, CLI) use errors.Is / errors.As to branch on them
// rather than string-matching.
var (
	// ErrWorktreeOrphan is returned when the target path exists on disk but
	// git does not know about it as a worktree. The directory is left
	// untouched — it may hold uncommitted work from a crashed session.
	ErrWorktreeOrphan = errors.New("worktree path exists but is not a registered git worktree")

	// ErrWorktreeNotAGitDir is returned when the target path exists on disk,
	// is not a registered worktree, and has no .git reference at all. Adopt
	// cannot repair such a directory.
	ErrWorktreeNotAGitDir = errors.New("worktree path is not a git worktree (no .git reference)")
)

// ErrWorktreeBranchMismatch is returned when the target path is a registered
// worktree but on a different branch than the caller asked for. The struct
// carries both names so the caller can format a meaningful message.
type ErrWorktreeBranchMismatch struct {
	Path     string
	Existing string
	Expected string
}

func (e *ErrWorktreeBranchMismatch) Error() string {
	return fmt.Sprintf("worktree at %s is registered on branch %q, expected %q",
		e.Path, e.Existing, e.Expected)
}

// Path returns the conventional worktree path for a branch inside a given
// repo's parent directory (<repo-parent>/worktrees/<branch>).
func Path(repoPath, branch string) string {
	return filepath.Join(filepath.Dir(filepath.Clean(repoPath)), "worktrees", branch)
}

// Create fetches from origin and creates a new git worktree for the given
// branch, or adopts an existing registered worktree on the same branch.
//
// Behaviour on path collision:
//   - fresh path: `git worktree add -b <branch> origin/main`, returns path.
//   - path registered on the same branch: returns (path, nil); logs an
//     adoption notice to stderr. Callers must be safe to re-enter.
//   - path registered on a different branch: returns *ErrWorktreeBranchMismatch.
//   - path exists but is not a registered worktree (orphan dir): returns
//     ErrWorktreeOrphan (or ErrWorktreeNotAGitDir if there is no .git ref
//     at all). The directory is left in place — operators can recover with
//     `squash-ide worktree clean` or `worktree adopt`.
func Create(repoPath, branch string) (string, error) {
	if _, err := os.Stat(repoPath); err != nil {
		return "", fmt.Errorf("repo path %s: %w", repoPath, err)
	}

	fetch := exec.Command("git", "-C", repoPath, "fetch", "origin")
	fetch.Stderr = os.Stderr
	if err := fetch.Run(); err != nil {
		return "", fmt.Errorf("git fetch origin: %w", err)
	}

	worktreePath := Path(repoPath, branch)

	if _, statErr := os.Stat(worktreePath); statErr == nil {
		// Path already present — decide between reuse, branch mismatch,
		// orphan, or non-git-dir.
		entries, err := listWorktrees(repoPath)
		if err != nil {
			return "", fmt.Errorf("listing worktrees: %w", err)
		}
		if existing, ok := findWorktree(entries, worktreePath); ok {
			if existing.Branch == branch {
				fmt.Fprintf(os.Stderr,
					"worktree: adopted existing %s on branch %s\n",
					worktreePath, branch)
				return worktreePath, nil
			}
			return "", &ErrWorktreeBranchMismatch{
				Path:     worktreePath,
				Existing: existing.Branch,
				Expected: branch,
			}
		}
		// Not registered. Distinguish "git repo the caller can repair" from
		// "totally unrelated directory".
		if hasGitReference(worktreePath) {
			return "", fmt.Errorf("%w: %s", ErrWorktreeOrphan, worktreePath)
		}
		return "", fmt.Errorf("%w: %s", ErrWorktreeNotAGitDir, worktreePath)
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return "", fmt.Errorf("stat worktree path %s: %w", worktreePath, statErr)
	}

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
// also tolerated. All git commands run from repoPath via -C, so Remove is
// safe to call even when the caller's cwd is stale or gone.
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

// Adopt attempts to re-register an orphan directory (one that was previously
// a valid worktree but is missing from `git worktree list`) via
// `git worktree repair`. On success the directory becomes reusable by
// Create. Returns ErrWorktreeNotAGitDir if the directory has no .git
// reference at all — repair cannot help in that case, so the operator must
// inspect manually.
func Adopt(repoPath, branch string) error {
	if _, err := os.Stat(repoPath); err != nil {
		return fmt.Errorf("repo path %s: %w", repoPath, err)
	}
	worktreePath := Path(repoPath, branch)
	if _, err := os.Stat(worktreePath); err != nil {
		return fmt.Errorf("stat worktree path %s: %w", worktreePath, err)
	}
	if !hasGitReference(worktreePath) {
		return fmt.Errorf("%w: %s", ErrWorktreeNotAGitDir, worktreePath)
	}
	repair := exec.Command("git", "-C", repoPath, "worktree", "repair", worktreePath)
	var stderr strings.Builder
	repair.Stderr = &stderr
	if err := repair.Run(); err != nil {
		return fmt.Errorf("git worktree repair %s: %w (stderr: %s)", worktreePath, err, stderr.String())
	}
	// Verify the repair registered something.
	entries, err := listWorktrees(repoPath)
	if err != nil {
		return fmt.Errorf("listing worktrees after repair: %w", err)
	}
	if _, ok := findWorktree(entries, worktreePath); !ok {
		return fmt.Errorf("worktree repair did not register %s (stderr: %s)", worktreePath, stderr.String())
	}
	return nil
}

// worktreeEntry is one record from `git worktree list --porcelain`.
type worktreeEntry struct {
	Path   string
	Branch string // short name ("main"), not "refs/heads/main"; "" if detached
}

// listWorktrees runs `git worktree list --porcelain` and returns the parsed
// entries. Stderr is captured (not inherited) so noisy porcelain output
// can't pollute the TUI.
func listWorktrees(repoPath string) ([]worktreeEntry, error) {
	cmd := exec.Command("git", "-C", repoPath, "worktree", "list", "--porcelain")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git worktree list --porcelain: %w (stderr: %s)", err, stderr.String())
	}
	return parseWorktreeList(string(out)), nil
}

// parseWorktreeList parses the porcelain output into entries. Records are
// separated by blank lines. Each record has lines like:
//
//	worktree /abs/path
//	HEAD <sha>
//	branch refs/heads/<name>
//
// A "detached" line replaces the branch line for detached-HEAD worktrees.
func parseWorktreeList(out string) []worktreeEntry {
	var entries []worktreeEntry
	var cur worktreeEntry
	flush := func() {
		if cur.Path != "" {
			entries = append(entries, cur)
		}
		cur = worktreeEntry{}
	}
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			flush()
			continue
		}
		switch {
		case strings.HasPrefix(line, "worktree "):
			cur.Path = strings.TrimPrefix(line, "worktree ")
		case strings.HasPrefix(line, "branch "):
			ref := strings.TrimPrefix(line, "branch ")
			cur.Branch = strings.TrimPrefix(ref, "refs/heads/")
		}
	}
	flush()
	return entries
}

// findWorktree returns the entry whose Path matches the given worktreePath
// (after resolving symlinks and cleaning both sides).
func findWorktree(entries []worktreeEntry, worktreePath string) (worktreeEntry, bool) {
	want := canonicalPath(worktreePath)
	for _, e := range entries {
		if canonicalPath(e.Path) == want {
			return e, true
		}
	}
	return worktreeEntry{}, false
}

// canonicalPath returns an absolute, symlink-resolved form of p. Falls back
// to Clean(p) if either step fails — used only for equality comparison, so
// a best-effort canonical form is fine.
func canonicalPath(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return filepath.Clean(p)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return abs
	}
	return resolved
}

// hasGitReference reports whether the given directory looks like a git
// worktree — i.e. contains a .git entry (file for worktrees, directory for
// primary checkouts).
func hasGitReference(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}
