//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/squashbox/squash-ide/internal/testutil/gitfix"
	"github.com/squashbox/squash-ide/internal/testutil/vaultfix"
)

// TestSpawnCompleteLifecycle drives `spawn` (with a dummy terminal that
// exits immediately) and then `complete`, asserting that the vault walks
// through backlog → active → archive with matching board/log entries.
func TestSpawnCompleteLifecycle(t *testing.T) {
	// Stub a fake terminal on PATH so the spawner has something to exec.
	fakeTerm := t.TempDir()
	terminalBin := filepath.Join(fakeTerm, "fake-term")
	if err := os.WriteFile(terminalBin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	env := []string{"PATH=" + fakeTerm + ":" + os.Getenv("PATH")}

	origin := gitfix.NewBareOrigin(t)
	repo := gitfix.Clone(t, origin)

	v := vaultfix.New(t)
	v.AddBacklog("T-001", "Spawn me", vaultfix.TaskOpts{Project: "p", Repo: repo})

	// Spawn.
	_, stderr, err := runBin(t, env,
		"--vault", v.Path(),
		"--no-tmux",
		"--terminal", "fake-term",
		"--spawn-cmd", "/bin/true",
		"spawn", "T-001",
	)
	if err != nil {
		t.Fatalf("spawn: %v\nstderr: %s", err, stderr)
	}

	// Task moved to active.
	activeDir := filepath.Join(v.Path(), "tasks/active")
	entries, _ := os.ReadDir(activeDir)
	if len(entries) == 0 {
		t.Fatal("task not moved to active")
	}
	if !strings.Contains(v.ReadBoard(), "T-001") {
		t.Error("board missing T-001")
	}
	if !strings.Contains(v.ReadLog(), "T-001") {
		t.Error("log missing T-001")
	}
	// Worktree present on disk.
	wt := filepath.Join(filepath.Dir(repo), "worktrees", "feat/T-001-spawn-me")
	if _, err := os.Stat(wt); err != nil {
		t.Fatalf("worktree missing: %v", err)
	}
	// .mcp.json dropped in the worktree.
	if _, err := os.Stat(filepath.Join(wt, ".mcp.json")); err != nil {
		t.Errorf(".mcp.json missing: %v", err)
	}

	// Complete.
	_, stderr, err = runBin(t, env,
		"--vault", v.Path(),
		"--no-tmux",
		"complete", "T-001",
	)
	if err != nil {
		t.Fatalf("complete: %v\nstderr: %s", err, stderr)
	}

	// Task moved to archive.
	archiveEntries, _ := os.ReadDir(filepath.Join(v.Path(), "tasks/archive"))
	if len(archiveEntries) == 0 {
		t.Error("task not moved to archive")
	}
	// Worktree removed.
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Errorf("worktree should be removed: %v", err)
	}
}

// TestBlockLifecycle exercises the `block` subcommand — moves a task from
// active to blocked with a reason stamped in the file body.
func TestBlockLifecycle(t *testing.T) {
	fakeTerm := t.TempDir()
	terminalBin := filepath.Join(fakeTerm, "fake-term")
	if err := os.WriteFile(terminalBin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	env := []string{"PATH=" + fakeTerm + ":" + os.Getenv("PATH")}

	origin := gitfix.NewBareOrigin(t)
	repo := gitfix.Clone(t, origin)

	v := vaultfix.New(t)
	v.AddBacklog("T-002", "blocker", vaultfix.TaskOpts{Project: "p", Repo: repo})

	if _, _, err := runBin(t, env,
		"--vault", v.Path(), "--no-tmux",
		"--terminal", "fake-term", "--spawn-cmd", "/bin/true",
		"spawn", "T-002"); err != nil {
		t.Fatalf("spawn: %v", err)
	}

	if _, stderr, err := runBin(t, env,
		"--vault", v.Path(), "--no-tmux",
		"block", "T-002", "--reason", "API rate limited",
	); err != nil {
		t.Fatalf("block: %v\nstderr: %s", err, stderr)
	}

	// Task is now in blocked/.
	blocked, _ := os.ReadDir(filepath.Join(v.Path(), "tasks/blocked"))
	if len(blocked) == 0 {
		t.Fatal("task not moved to blocked/")
	}
	// Body contains the reason.
	body, err := os.ReadFile(filepath.Join(v.Path(), "tasks/blocked", blocked[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "API rate limited") {
		t.Errorf("block reason missing from file body")
	}
}
