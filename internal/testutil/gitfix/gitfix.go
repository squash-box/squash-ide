// Package gitfix builds real ephemeral git repos for tests.
//
// The internal/worktree tests and the e2e suite need an on-disk origin and
// a working clone — faking git subprocess output is brittle for the
// happy-path flow where the repo state after `git worktree add` is
// inspected. gitfix shells out to the real `git` binary (tests that need
// hermeticity should use fakerunner + exec.Runner instead).
package gitfix

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// NewBareOrigin creates a temp bare repo at <TempDir>/origin.git with one
// initial commit on 'main' and returns its path. The repo is deleted with
// the surrounding t.TempDir cleanup.
func NewBareOrigin(t *testing.T) string {
	t.Helper()
	skipIfNoGit(t)

	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	run(t, root, "git", "init", "--bare", "-b", "main", origin)

	// Seed via an intermediate clone so we get a proper initial commit.
	seed := filepath.Join(root, "seed")
	run(t, root, "git", "clone", origin, seed)
	runCfg(t, seed)
	if err := os.WriteFile(filepath.Join(seed, "README.md"), []byte("seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, seed, "git", "add", "README.md")
	run(t, seed, "git", "commit", "-m", "initial")
	run(t, seed, "git", "push", "origin", "main")

	return origin
}

// Clone clones origin into a working directory under <TempDir>/clone-<N>
// and returns its path. Useful for worktree tests that need a non-bare
// repo to operate in.
func Clone(t *testing.T, origin string) string {
	t.Helper()
	skipIfNoGit(t)
	root := t.TempDir()
	clone := filepath.Join(root, "clone")
	run(t, root, "git", "clone", origin, clone)
	runCfg(t, clone)
	return clone
}

func skipIfNoGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}
}

func run(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	c := exec.Command(name, args...)
	c.Dir = dir
	out, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v (dir=%s): %v\n%s", name, args, dir, err, out)
	}
}

// runCfg sets up a minimal user.email / user.name on a cloned repo so
// commits succeed even when the host machine has no global git identity
// (CI is the usual offender).
func runCfg(t *testing.T, dir string) {
	t.Helper()
	run(t, dir, "git", "config", "user.email", "test@example.invalid")
	run(t, dir, "git", "config", "user.name", "Test User")
	run(t, dir, "git", "config", "commit.gpgsign", "false")
}
