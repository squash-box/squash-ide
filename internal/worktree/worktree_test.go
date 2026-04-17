package worktree

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/squashbox/squash-ide/internal/testutil/fakerunner"
	"github.com/squashbox/squash-ide/internal/testutil/gitfix"
)

// repoDir returns a stable subdir of t.TempDir() so canonical worktree
// paths resolve deterministically across fakerunner expectations.
func repoDir(t *testing.T) string {
	t.Helper()
	d := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	return d
}

// fakeCreate drives CreateWith on the happy fresh-path branch: fetch, then
// worktree add. The worktree dir is created by the test harness so the
// os.Stat "path exists" probe doesn't trigger.
func fakeCreate(t *testing.T, r *fakerunner.Runner, repo, branch string) string {
	t.Helper()
	r.Expect("git", "-C", repo, "fetch", "origin")
	r.Expect("git", "-C", repo, "worktree", "add",
		Path(repo, branch), "-b", branch, "origin/main")
	return Path(repo, branch)
}

// --- Path ---------------------------------------------------------------

func TestPath(t *testing.T) {
	got := Path("/a/b/myrepo", "feat/T-001-x")
	want := filepath.Join("/a/b", "worktrees", "feat/T-001-x")
	if got != want {
		t.Errorf("Path: got %q, want %q", got, want)
	}
}

func TestPath_TrailingSlash(t *testing.T) {
	// filepath.Clean drops the trailing slash so Dir() still yields the
	// repo's parent — not the repo itself.
	got := Path("/a/b/myrepo/", "feat/foo")
	want := filepath.Join("/a/b", "worktrees", "feat/foo")
	if got != want {
		t.Errorf("Path trailing slash: got %q, want %q", got, want)
	}
}

// --- CreateWith (fakerunner) -------------------------------------------

func TestCreate_HappyPath(t *testing.T) {
	repo := repoDir(t)
	r := fakerunner.New(t)
	fakeCreate(t, r, repo, "feat/T-001-x")

	got, err := CreateWith(r, repo, "feat/T-001-x")
	if err != nil {
		t.Fatalf("CreateWith: %v", err)
	}
	if got != Path(repo, "feat/T-001-x") {
		t.Errorf("returned path: %q", got)
	}
}

func TestCreate_RepoMissing(t *testing.T) {
	r := fakerunner.New(t)
	r.AllowUnexpected = true
	_, err := CreateWith(r, "/definitely/not/a/repo", "feat/T-001-x")
	if err == nil {
		t.Fatal("expected error for missing repo")
	}
}

func TestCreate_FetchFails(t *testing.T) {
	repo := repoDir(t)
	r := fakerunner.New(t)
	r.Expect("git", "-C", repo, "fetch", "origin").
		ReturnsExitErr(errors.New("network down"))
	_, err := CreateWith(r, repo, "feat/x")
	if err == nil {
		t.Fatal("expected fetch err")
	}
	if !strings.Contains(err.Error(), "fetch") {
		t.Errorf("error should mention fetch: %v", err)
	}
}

func TestCreate_WorktreeAddFails(t *testing.T) {
	repo := repoDir(t)
	r := fakerunner.New(t)
	r.Expect("git", "-C", repo, "fetch", "origin")
	r.Expect("git", "-C", repo, "worktree", "add",
		Path(repo, "feat/x"), "-b", "feat/x", "origin/main").
		ReturnsExitErr(errors.New("fatal: not a git repository"))
	_, err := CreateWith(r, repo, "feat/x")
	if err == nil {
		t.Fatal("expected err")
	}
	if !strings.Contains(err.Error(), "worktree add") {
		t.Errorf("error should mention worktree add: %v", err)
	}
}

// --- Remove (fakerunner) -----------------------------------------------

func TestRemove_HappyPath(t *testing.T) {
	repo := repoDir(t)
	wt := Path(repo, "feat/x")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	r := fakerunner.New(t)
	r.Expect("git", "-C", repo, "worktree", "remove", wt)
	r.Expect("git", "-C", repo, "branch", "-D", "feat/x")

	if err := RemoveWith(r, repo, "feat/x"); err != nil {
		t.Fatalf("remove: %v", err)
	}
}

func TestRemove_WorktreeMissing_StillDeletesBranch(t *testing.T) {
	repo := repoDir(t)
	r := fakerunner.New(t)
	// Worktree dir doesn't exist: skip remove, still call branch -D.
	r.Expect("git", "-C", repo, "branch", "-D", "feat/x")

	if err := RemoveWith(r, repo, "feat/x"); err != nil {
		t.Fatalf("remove: %v", err)
	}
}

func TestRemove_DirtyRetriesWithForce(t *testing.T) {
	repo := repoDir(t)
	wt := Path(repo, "feat/x")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	r := fakerunner.New(t)
	r.Expect("git", "-C", repo, "worktree", "remove", wt).
		ReturnsExitErr(errors.New("dirty working tree"))
	r.Expect("git", "-C", repo, "worktree", "remove", "--force", wt)
	r.Expect("git", "-C", repo, "branch", "-D", "feat/x")

	if err := RemoveWith(r, repo, "feat/x"); err != nil {
		t.Fatalf("remove dirty: %v", err)
	}
}

func TestRemove_BranchNotFound_Tolerated(t *testing.T) {
	repo := repoDir(t)
	r := fakerunner.New(t)
	r.Expect("git", "-C", repo, "branch", "-D", "feat/x").
		ReturnsExitErr(errors.New("error: branch 'feat/x' not found."))

	if err := RemoveWith(r, repo, "feat/x"); err != nil {
		t.Fatalf("expected tolerated, got: %v", err)
	}
}

// --- Runner swap -------------------------------------------------------

func TestSetRunner_RestoresDefault(t *testing.T) {
	orig := runner
	fake := fakerunner.New(t)
	prev := SetRunner(fake)
	if prev != orig {
		t.Error("SetRunner should return previous runner")
	}
	if runner != fake {
		t.Error("runner should be swapped")
	}
	SetRunner(orig)
}

func TestCreate_PublicWrapper(t *testing.T) {
	repo := repoDir(t)
	fake := fakerunner.New(t)
	fakeCreate(t, fake, repo, "feat/pub")

	prev := SetRunner(fake)
	t.Cleanup(func() { SetRunner(prev) })

	if _, err := Create(repo, "feat/pub"); err != nil {
		t.Fatalf("Create: %v", err)
	}
}

func TestRemove_PublicWrapper(t *testing.T) {
	repo := repoDir(t)
	fake := fakerunner.New(t)
	fake.Expect("git", "-C", repo, "branch", "-D", "feat/pub")

	prev := SetRunner(fake)
	t.Cleanup(func() { SetRunner(prev) })

	if err := Remove(repo, "feat/pub"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
}

// --- Real-git integration tests (T-020 path-collision classification) ---
//
// These tests drive the real `git` binary via gitfix so they exercise the
// actual output of `git worktree list --porcelain` and `git worktree
// repair` — no amount of fakerunner scripting can cover that correctly.

func TestCreate_Reuse_SameBranch(t *testing.T) {
	origin := gitfix.NewBareOrigin(t)
	repo := gitfix.Clone(t, origin)

	path, err := Create(repo, "feat/reuse")
	if err != nil {
		t.Fatalf("Create (first): %v", err)
	}
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
	// Marker survives — reuse did not re-run `worktree add`.
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("marker gone after reuse: %v", err)
	}
}

func TestCreate_BranchMismatch(t *testing.T) {
	origin := gitfix.NewBareOrigin(t)
	repo := gitfix.Clone(t, origin)

	path, err := Create(repo, "feat/alpha")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Move the alpha worktree dir to the canonical beta path, then ask git
	// to repair the bookkeeping so git now registers feat/alpha at the
	// beta path. A subsequent Create(feat/beta) must return a branch
	// mismatch.
	betaPath := Path(repo, "feat/beta")
	if err := os.MkdirAll(filepath.Dir(betaPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path, betaPath); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", repo, "worktree", "repair", betaPath).CombinedOutput(); err != nil {
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
	origin := gitfix.NewBareOrigin(t)
	repo := gitfix.Clone(t, origin)

	path, err := Create(repo, "feat/gamma")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Deregister the worktree but leave a directory with a stale .git file
	// behind — this is the classic orphan-with-git-ref shape.
	if out, err := exec.Command("git", "-C", repo, "worktree", "remove", "--force", path).CombinedOutput(); err != nil {
		t.Fatalf("git worktree remove: %v (%s)", err, out)
	}
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
	if _, err := os.Stat(path); err != nil {
		t.Errorf("orphan dir was removed: %v", err)
	}
}

func TestCreate_NonGitOrphan(t *testing.T) {
	origin := gitfix.NewBareOrigin(t)
	repo := gitfix.Clone(t, origin)

	path := Path(repo, "feat/delta")
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "some-file"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Create(repo, "feat/delta")
	if !errors.Is(err, ErrWorktreeNotAGitDir) {
		t.Fatalf("Create: expected ErrWorktreeNotAGitDir, got %v", err)
	}
}

// TestRemove_StaleCwd is the concrete "stale cwd safety" guard from T-020.
// If the caller's cwd has been removed from under them, Remove must still
// succeed because all of its git invocations run from repoPath via -C.
func TestRemove_StaleCwd(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chdir semantics differ on windows")
	}
	origin := gitfix.NewBareOrigin(t)
	repo := gitfix.Clone(t, origin)

	path, err := Create(repo, "feat/stalecwd")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

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
	origin := gitfix.NewBareOrigin(t)
	repo := gitfix.Clone(t, origin)
	branch := "feat/adopt"
	canonical := Path(repo, branch)

	// Register a worktree at a non-canonical path first, then rename it to
	// the canonical location. git still thinks it lives at "elsewhere";
	// the canonical path is an orphan on disk until Adopt repairs the
	// bookkeeping.
	elsewhere := filepath.Join(t.TempDir(), "elsewhere")
	if out, err := exec.Command("git", "-C", repo, "worktree", "add", elsewhere, "-b", branch).CombinedOutput(); err != nil {
		t.Fatalf("git worktree add: %v (%s)", err, out)
	}
	if err := os.MkdirAll(filepath.Dir(canonical), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(elsewhere, canonical); err != nil {
		t.Fatal(err)
	}

	if err := Adopt(repo, branch); err != nil {
		t.Fatalf("Adopt: %v", err)
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
	origin := gitfix.NewBareOrigin(t)
	repo := gitfix.Clone(t, origin)
	path := Path(repo, "feat/notgit")
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	err := Adopt(repo, "feat/notgit")
	if !errors.Is(err, ErrWorktreeNotAGitDir) {
		t.Errorf("Adopt on non-git dir: expected ErrWorktreeNotAGitDir, got %v", err)
	}
}

// --- Porcelain parser unit test ----------------------------------------

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
