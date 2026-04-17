package dispatch

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/squashbox/squash-ide/internal/config"
	"github.com/squashbox/squash-ide/internal/task"
	"github.com/squashbox/squash-ide/internal/worktree"
)

// setupRepo mirrors the helper in worktree_test: creates a throwaway git repo
// with a `main` branch and a self-referential `origin` so fetches succeed.
func setupRepo(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	repo := filepath.Join(tmp, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
		)
		var stderr strings.Builder
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("git %s: %v (%s)", strings.Join(args, " "), err, stderr.String())
		}
	}
	run("init", "-b", "main")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "README.md")
	run("commit", "-m", "seed")
	run("remote", "add", "origin", repo)
	run("fetch", "origin")
	return repo
}

// setupVault builds a vault skeleton with a single backlog task file
// referencing the given repo. Returns (vaultRoot, task).
func setupVault(t *testing.T, repoPath string) (string, task.Task) {
	t.Helper()
	root := t.TempDir()
	for _, d := range []string{"tasks/backlog", "tasks/active", "tasks/blocked", "tasks/archive", "wiki/entities", "wiki"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	taskBody := `---
id: T-999
type: feature
title: Dispatch orphan test
project: test-proj
status: backlog
created: 2026-04-17
priority: high
repo: ` + repoPath + `
related:
  - test-proj
---

# T-999
`
	if err := os.WriteFile(filepath.Join(root, "tasks/backlog/T-999-dispatch.md"), []byte(taskBody), 0o644); err != nil {
		t.Fatal(err)
	}
	board := `---
type: board
title: Test Board
last_updated: 2026-04-17
---

# Task Board

## Active

_None_

## Backlog

| ID | Project | Title | Type |
|----|---------|-------|------|
| [[T-999]] | test-proj | Dispatch orphan test | feature |

## Blocked

_None_

## Recently Completed

_None_
`
	if err := os.WriteFile(filepath.Join(root, "tasks/board.md"), []byte(board), 0o644); err != nil {
		t.Fatal(err)
	}
	log := `---
type: log
title: Test Log
---

# Activity Log
`
	if err := os.WriteFile(filepath.Join(root, "wiki/log.md"), []byte(log), 0o644); err != nil {
		t.Fatal(err)
	}

	tk := task.Task{
		ID:      "T-999",
		Title:   "Dispatch orphan test",
		Project: "test-proj",
		Status:  "backlog",
		Repo:    repoPath,
	}
	return root, tk
}

// TestRun_OrphanErrorPassThrough confirms that when the worktree path is
// already occupied by a non-registered directory, Run surfaces the typed
// error (via errors.Is) AND annotates the message with the operator-facing
// subcommand hint naming the exact recovery path.
func TestRun_OrphanErrorPassThrough(t *testing.T) {
	repo := setupRepo(t)
	vaultRoot, tk := setupVault(t, repo)

	// Pre-seed an orphan directory at the canonical worktree path.
	branch := BranchFor(tk)
	orphanPath := worktree.Path(repo, branch)
	if err := os.MkdirAll(orphanPath, 0o755); err != nil {
		t.Fatal(err)
	}
	// Give it a .git reference so the orphan is classified as
	// ErrWorktreeOrphan (not ErrWorktreeNotAGitDir).
	if err := os.WriteFile(filepath.Join(orphanPath, ".git"), []byte("gitdir: "+repo+"/.git\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := config.Config{Vault: vaultRoot}
	cfg.Tmux.Enabled = false

	_, err := Run(cfg, tk)
	if err == nil {
		t.Fatal("Run: expected error on orphan worktree path")
	}
	if !errors.Is(err, worktree.ErrWorktreeOrphan) {
		t.Errorf("expected errors.Is(err, ErrWorktreeOrphan), got %v", err)
	}
	if !strings.Contains(err.Error(), "squash-ide worktree adopt T-999") {
		t.Errorf("expected adopt hint in message, got: %v", err)
	}
	if !strings.Contains(err.Error(), "squash-ide worktree clean T-999") {
		t.Errorf("expected clean hint in message, got: %v", err)
	}

	// Orphan dir must not have been deleted by Run.
	if _, err := os.Stat(orphanPath); err != nil {
		t.Errorf("orphan dir disappeared: %v", err)
	}

	// Task must still be in backlog (no vault mutation on failure).
	if _, err := os.Stat(filepath.Join(vaultRoot, "tasks/backlog/T-999-dispatch.md")); err != nil {
		t.Errorf("task file moved out of backlog after failed spawn: %v", err)
	}
}

// TestRun_NonGitOrphanErrorPassThrough covers the sibling "no .git reference
// at all" classification, which needs a different operator hint — inspect
// manually, then clean.
func TestRun_NonGitOrphanErrorPassThrough(t *testing.T) {
	repo := setupRepo(t)
	vaultRoot, tk := setupVault(t, repo)

	branch := BranchFor(tk)
	orphanPath := worktree.Path(repo, branch)
	if err := os.MkdirAll(orphanPath, 0o755); err != nil {
		t.Fatal(err)
	}
	// No .git reference — ErrWorktreeNotAGitDir path.
	if err := os.WriteFile(filepath.Join(orphanPath, "bystander.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := config.Config{Vault: vaultRoot}
	cfg.Tmux.Enabled = false

	_, err := Run(cfg, tk)
	if !errors.Is(err, worktree.ErrWorktreeNotAGitDir) {
		t.Fatalf("expected errors.Is(err, ErrWorktreeNotAGitDir), got %v", err)
	}
	if !strings.Contains(err.Error(), "inspect the directory manually") {
		t.Errorf("expected manual-inspect hint, got: %v", err)
	}
}
