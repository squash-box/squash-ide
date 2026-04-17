package worktree

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/squashbox/squash-ide/internal/testutil/fakerunner"
)

// pathsep helper — use a stable repo dir under t.TempDir() so worktree
// paths resolve deterministically.
func repoDir(t *testing.T) string {
	t.Helper()
	d := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestPath(t *testing.T) {
	got := Path("/a/b/myrepo", "feat/T-001-x")
	want := filepath.Join("/a/b", "worktrees", "feat/T-001-x")
	if got != want {
		t.Errorf("Path: got %q, want %q", got, want)
	}
}

func TestCreate_HappyPath(t *testing.T) {
	repo := repoDir(t)
	r := fakerunner.New(t)
	r.Expect("git", "-C", repo, "fetch", "origin")
	r.Expect("git", "-C", repo, "worktree", "add",
		Path(repo, "feat/T-001-x"), "-b", "feat/T-001-x", "origin/main")

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

func TestCreate_WorktreeExists(t *testing.T) {
	repo := repoDir(t)
	// Pre-create the worktree dir so Create bails early.
	wt := Path(repo, "feat/T-001-x")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	r := fakerunner.New(t)
	r.Expect("git", "-C", repo, "fetch", "origin")
	_, err := CreateWith(r, repo, "feat/T-001-x")
	if err == nil {
		t.Fatal("expected error when worktree already exists")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("got: %v", err)
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

// Exercise the public Create/Remove wrappers by swapping the package runner.
func TestCreate_PublicWrapper(t *testing.T) {
	repo := repoDir(t)
	fake := fakerunner.New(t)
	fake.Expect("git", "-C", repo, "fetch", "origin")
	fake.Expect("git", "-C", repo, "worktree", "add",
		Path(repo, "feat/pub"), "-b", "feat/pub", "origin/main")

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
