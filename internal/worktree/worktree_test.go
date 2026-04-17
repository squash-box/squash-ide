package worktree

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// setupRepo creates a self-contained git repo with a `main` branch and
// one seed commit, plus a fake `origin` remote pointing back at itself so
// `git fetch origin` succeeds. Returns the repo path.
func setupRepo(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	repo := filepath.Join(tmp, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}

	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
		)
		var stderr strings.Builder
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("git %s: %v (stderr: %s)", strings.Join(args, " "), err, stderr.String())
		}
	}

	runGit("init", "-b", "main")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("add", "README.md")
	runGit("commit", "-m", "seed")
	// Point "origin" at ourselves so `git fetch origin` works.
	runGit("remote", "add", "origin", repo)
	runGit("fetch", "origin")

	return repo
}

// worktreeIsRegistered reports whether git considers path a worktree of repo.
func worktreeIsRegistered(t *testing.T, repo, path string) bool {
	t.Helper()
	entries, err := listWorktrees(repo)
	if err != nil {
		t.Fatalf("listWorktrees: %v", err)
	}
	_, ok := findWorktree(entries, path)
	return ok
}

func TestPath_TypicalRepoPath(t *testing.T) {
	got := Path("/home/alice/GIT/proj", "feat/foo")
	want := "/home/alice/GIT/worktrees/feat/foo"
	if got != want {
		t.Errorf("Path = %q, want %q", got, want)
	}
}

func TestPath_TrailingSlash(t *testing.T) {
	// Trailing slash on repo path should still produce <repo-parent>/worktrees/...
	// filepath.Clean drops the trailing slash before Dir.
	got := Path("/home/alice/GIT/proj/", "feat/foo")
	want := "/home/alice/GIT/worktrees/feat/foo"
	if got != want {
		t.Errorf("Path = %q, want %q", got, want)
	}
}

func TestCreate_HappyPath(t *testing.T) {
	repo := setupRepo(t)
	path, err := Create(repo, "feat/new")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("worktree dir missing: %v", err)
	}
	if !worktreeIsRegistered(t, repo, path) {
		t.Errorf("worktree not registered with git")
	}
}

func TestCreate_Reuse_SameBranch(t *testing.T) {
	repo := setupRepo(t)
	path, err := Create(repo, "feat/reuse")
	if err != nil {
		t.Fatalf("Create (first): %v", err)
	}
	// Drop a marker file we can verify survived the second call.
	marker := filepath.Join(path, "marker.txt")
	if err := os.WriteFile(marker, []byte("kept"), 0o644); err != nil {
		t.Fatal(err)
	}

	path2, err := Create(repo, "feat/reuse")
	if err != nil {
		t.Fatalf("Create (reuse): %v", err)
	}
	if path2 != path {
		t.Errorf("Create reuse returned different path: %q vs %q", path2, path)
	}
	// Marker must still be there — reuse must not re-run `worktree add`.
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("marker gone after reuse — worktree was recreated: %v", err)
	}
}

func TestCreate_BranchMismatch(t *testing.T) {
	repo := setupRepo(t)
	path, err := Create(repo, "feat/alpha")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Second call targeting the canonical path for a different branch.
	// The conventional path depends on the branch — Path(repo, "feat/beta")
	// is different from Path(repo, "feat/alpha"). To force the mismatch we
	// have to place a worktree at the beta path that git knows about, but
	// registered on a different branch. Easiest: rename the branch on the
	// existing checkout and re-query.
	betaPath := Path(repo, "feat/beta")
	if err := os.Rename(path, betaPath); err != nil {
		t.Fatal(err)
	}
	// Tell git where the worktree moved to — `git worktree repair` fixes up
	// the gitdir references both ways.
	repair := exec.Command("git", "-C", repo, "worktree", "repair", betaPath)
	if out, err := repair.CombinedOutput(); err != nil {
		t.Fatalf("git worktree repair: %v (%s)", err, out)
	}

	_, err = Create(repo, "feat/beta")
	var mismatch *ErrWorktreeBranchMismatch
	if !errors.As(err, &mismatch) {
		t.Fatalf("Create: expected *ErrWorktreeBranchMismatch, got %v", err)
	}
	if mismatch.Existing != "feat/alpha" {
		t.Errorf("Existing = %q, want feat/alpha", mismatch.Existing)
	}
	if mismatch.Expected != "feat/beta" {
		t.Errorf("Expected = %q, want feat/beta", mismatch.Expected)
	}
}

func TestCreate_Orphan_HasGitReference(t *testing.T) {
	repo := setupRepo(t)
	path, err := Create(repo, "feat/gamma")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Deregister the worktree (administratively) but keep the directory —
	// this is the "orphan with a .git reference" state.
	rm := exec.Command("git", "-C", repo, "worktree", "remove", "--force", path)
	rm.Env = os.Environ()
	if out, err := rm.CombinedOutput(); err != nil {
		t.Fatalf("git worktree remove: %v (%s)", err, out)
	}
	// Recreate a bare directory at the same spot with a .git file.
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	gitFileContent := "gitdir: " + filepath.Join(repo, ".git", "worktrees", "gamma") + "\n"
	if err := os.WriteFile(filepath.Join(path, ".git"), []byte(gitFileContent), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err = Create(repo, "feat/gamma")
	if !errors.Is(err, ErrWorktreeOrphan) {
		t.Fatalf("Create: expected ErrWorktreeOrphan, got %v", err)
	}
	// Directory must be left in place.
	if _, err := os.Stat(path); err != nil {
		t.Errorf("orphan dir was removed: %v", err)
	}
}

func TestCreate_NonGitOrphan(t *testing.T) {
	repo := setupRepo(t)
	path := Path(repo, "feat/delta")
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	// No .git reference of any kind.
	if err := os.WriteFile(filepath.Join(path, "some-file"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Create(repo, "feat/delta")
	if !errors.Is(err, ErrWorktreeNotAGitDir) {
		t.Fatalf("Create: expected ErrWorktreeNotAGitDir, got %v", err)
	}
}

func TestCreate_FetchFailure_ShortCircuits(t *testing.T) {
	repo := t.TempDir()
	// Not a git repo — fetch will fail before we ever stat the worktree path.
	_, err := Create(repo, "feat/whatever")
	if err == nil {
		t.Fatal("Create: expected error on non-git repo")
	}
	// Should mention git fetch, not the worktree path check.
	if !strings.Contains(err.Error(), "fetch") {
		t.Errorf("expected fetch failure, got: %v", err)
	}
}

func TestRemove_HappyPath(t *testing.T) {
	repo := setupRepo(t)
	path, err := Create(repo, "feat/remove")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := Remove(repo, "feat/remove"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("worktree dir still present: %v", err)
	}
	if worktreeIsRegistered(t, repo, path) {
		t.Errorf("worktree still registered with git")
	}
}

func TestRemove_Idempotent_MissingPath(t *testing.T) {
	repo := setupRepo(t)
	// Never called Create — nothing there.
	if err := Remove(repo, "feat/never-created"); err != nil {
		t.Errorf("Remove on missing path should be no-op, got: %v", err)
	}
}

func TestRemove_Idempotent_MissingBranch(t *testing.T) {
	repo := setupRepo(t)
	// Call Remove twice — second call should still return nil even though
	// the branch is already gone.
	_, err := Create(repo, "feat/twice")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := Remove(repo, "feat/twice"); err != nil {
		t.Fatalf("Remove (first): %v", err)
	}
	if err := Remove(repo, "feat/twice"); err != nil {
		t.Errorf("Remove (second) should be no-op, got: %v", err)
	}
}

func TestRemove_ForceRetry_ModifiedTree(t *testing.T) {
	repo := setupRepo(t)
	path, err := Create(repo, "feat/dirty")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Dirty the worktree — first `git worktree remove` should balk; the
	// --force retry path should kick in and succeed.
	if err := os.WriteFile(filepath.Join(path, "dirty.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Remove(repo, "feat/dirty"); err != nil {
		t.Fatalf("Remove with dirty tree: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("dirty worktree dir still present")
	}
}

// TestRemove_StaleCwd is the concrete "stale cwd safety" guard from T-020.
// If the caller's cwd has been removed from under them, Remove must still
// succeed because all of its git invocations run from repoPath via -C.
func TestRemove_StaleCwd(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chdir semantics differ on windows")
	}
	repo := setupRepo(t)
	path, err := Create(repo, "feat/stalecwd")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Preserve original cwd; restore after test so subsequent tests aren't
	// poisoned.
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })

	throwaway := filepath.Join(t.TempDir(), "doomed")
	if err := os.MkdirAll(throwaway, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(throwaway); err != nil {
		t.Fatal(err)
	}
	// Yank the dir out from under us.
	if err := os.RemoveAll(throwaway); err != nil {
		t.Fatal(err)
	}

	if err := Remove(repo, "feat/stalecwd"); err != nil {
		t.Fatalf("Remove with stale cwd should succeed, got: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("worktree dir still present after Remove")
	}
}

func TestAdopt_HappyPath(t *testing.T) {
	repo := setupRepo(t)
	branch := "feat/adopt"
	canonical := Path(repo, branch)

	// Register a worktree at a non-canonical path first, then rename it to
	// the canonical location. git still thinks it lives at "elsewhere" —
	// the canonical path is an orphan on disk until Adopt repairs the
	// bookkeeping.
	elsewhere := filepath.Join(t.TempDir(), "elsewhere")
	add := exec.Command("git", "-C", repo, "worktree", "add", elsewhere, "-b", branch)
	if out, err := add.CombinedOutput(); err != nil {
		t.Fatalf("git worktree add: %v (%s)", err, out)
	}
	if err := os.MkdirAll(filepath.Dir(canonical), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(elsewhere, canonical); err != nil {
		t.Fatal(err)
	}

	// Sanity: canonical is an orphan from git's POV right now.
	if worktreeIsRegistered(t, repo, canonical) {
		t.Fatalf("precondition: canonical should not be registered yet")
	}

	if err := Adopt(repo, branch); err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	if !worktreeIsRegistered(t, repo, canonical) {
		t.Errorf("Adopt did not register worktree at canonical path")
	}

	// Create after Adopt must take the reuse path.
	path2, err := Create(repo, branch)
	if err != nil {
		t.Fatalf("Create after Adopt: %v", err)
	}
	if path2 != canonical {
		t.Errorf("reuse path mismatch: %q vs %q", path2, canonical)
	}
}

func TestAdopt_NonGitDir(t *testing.T) {
	repo := setupRepo(t)
	path := Path(repo, "feat/notgit")
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	err := Adopt(repo, "feat/notgit")
	if !errors.Is(err, ErrWorktreeNotAGitDir) {
		t.Errorf("Adopt on non-git dir: expected ErrWorktreeNotAGitDir, got %v", err)
	}
}

func TestParseWorktreeList_Porcelain(t *testing.T) {
	input := `worktree /home/a/proj
HEAD deadbeefdeadbeefdeadbeefdeadbeefdeadbeef
branch refs/heads/main

worktree /home/a/worktrees/feat/foo
HEAD cafef00dcafef00dcafef00dcafef00dcafef00d
branch refs/heads/feat/foo

worktree /home/a/worktrees/detached
HEAD 1234567812345678123456781234567812345678
detached
`
	entries := parseWorktreeList(input)
	if len(entries) != 3 {
		t.Fatalf("entries: %d, want 3", len(entries))
	}
	if entries[1].Path != "/home/a/worktrees/feat/foo" || entries[1].Branch != "feat/foo" {
		t.Errorf("feat/foo mis-parsed: %+v", entries[1])
	}
	if entries[2].Branch != "" {
		t.Errorf("detached entry should have empty branch, got %q", entries[2].Branch)
	}
}
