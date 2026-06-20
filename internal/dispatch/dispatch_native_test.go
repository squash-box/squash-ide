package dispatch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/squashbox/squash-ide/internal/config"
	"github.com/squashbox/squash-ide/internal/spawner"
	"github.com/squashbox/squash-ide/internal/task"
	"github.com/squashbox/squash-ide/internal/testutil/fakerunner"
	"github.com/squashbox/squash-ide/internal/testutil/gitfix"
	"github.com/squashbox/squash-ide/internal/testutil/vaultfix"
	"github.com/squashbox/squash-ide/internal/worktree"
)

// Under engine=native, Run does the full vault/worktree prep but hands the
// prepared command back instead of spawning — a native pane is owned by the
// running TUI, not by this call. The spawner's process runner must never fire.
func TestRun_Native_ReturnsSpawnSpecAndSkipsTerminal(t *testing.T) {
	origin := gitfix.NewBareOrigin(t)
	repo := gitfix.Clone(t, origin)

	v := vaultfix.New(t)
	v.AddBacklog("T-039", "Repoint to native", vaultfix.TaskOpts{Project: "squash-ide", Repo: repo})

	tk := task.Task{
		ID: "T-039", Title: "Repoint to native", Status: "backlog",
		Project: "squash-ide", Repo: repo,
	}

	cfg := config.Config{
		Vault:  v.Path(),
		Engine: config.EngineNative,
		Tmux:   config.Tmux{Enabled: true}, // even with tmux "enabled", native must not touch it
		Spawn:  config.Spawn{Command: "claude", Args: []string{"/implement {task_id}"}},
	}

	// Any call into the spawner's runner fails the test — native must not spawn
	// a terminal here.
	spFake := fakerunner.New(t)
	prev := spawner.SetRunner(spFake)
	t.Cleanup(func() { spawner.SetRunner(prev) })

	res, err := Run(cfg, tk)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if res.Native == nil {
		t.Fatal("native Run should return a Native spawn spec")
	}
	if res.Native.TaskID != "T-039" || res.Native.Title != "Repoint to native" || res.Native.Project != "squash-ide" {
		t.Errorf("Native metadata = %+v", res.Native)
	}
	if res.Native.Command == nil {
		t.Fatal("Native.Command is nil")
	}
	if got, want := res.Native.Command.Args, []string{"claude", "/implement T-039"}; !equalStrings(got, want) {
		t.Errorf("Native.Command.Args = %v, want %v", got, want)
	}
	if res.Native.Command.Dir != res.WorktreePath {
		t.Errorf("Native.Command.Dir = %q, want worktree %q", res.Native.Command.Dir, res.WorktreePath)
	}

	// Vault side effects still happen — the task is active, worktree prepared.
	actives, _ := os.ReadDir(filepath.Join(v.Path(), "tasks/active"))
	if len(actives) == 0 {
		t.Error("native Run should still move the task to active/")
	}
	if _, err := os.Stat(filepath.Join(res.WorktreePath, ".mcp.json")); err != nil {
		t.Errorf("native Run should still write .mcp.json: %v", err)
	}
}

// Complete under engine=native archives the task and removes the worktree
// without any tmux interaction (pane teardown is the TUI's job / a no-op here).
func TestComplete_Native_ArchivesWithoutTmux(t *testing.T) {
	f := newCompleteFixture(t)
	f.cfg.Engine = config.EngineNative
	f.cfg.Tmux.Enabled = true // native must ignore this, not shell tmux
	stubGHMissing(t)          // no PR URL; degrade gracefully

	if err := Complete(f.cfg, f.task); err != nil {
		t.Fatalf("Complete (native): %v", err)
	}

	if _, err := os.Stat(f.wtPath); !os.IsNotExist(err) {
		t.Errorf("worktree should be removed; stat err = %v", err)
	}
	matches, _ := filepath.Glob(filepath.Join(f.vault.Path(), "tasks/archive/T-099-*.md"))
	if len(matches) != 1 {
		t.Fatalf("expected one archived task file, got %v", matches)
	}
}

// Deactivate under engine=native returns the task to the backlog and removes
// the worktree, no tmux involved.
func TestDeactivate_Native_MovesToBacklogWithoutTmux(t *testing.T) {
	origin := gitfix.NewBareOrigin(t)
	repo := gitfix.Clone(t, origin)

	v := vaultfix.New(t)
	v.AddActive("T-099", "Back to backlog", vaultfix.TaskOpts{Project: "squash-ide", Repo: repo})
	tk := task.Task{ID: "T-099", Title: "Back to backlog", Status: "active", Project: "squash-ide", Repo: repo}
	branch := BranchFor(tk)
	wtPath, err := worktree.Create(repo, branch)
	if err != nil {
		t.Fatalf("seed worktree: %v", err)
	}

	cfg := config.Config{
		Vault:  v.Path(),
		Engine: config.EngineNative,
		Tmux:   config.Tmux{Enabled: true},
	}

	if err := Deactivate(cfg, tk); err != nil {
		t.Fatalf("Deactivate (native): %v", err)
	}

	if _, err := os.Stat(wtPath); !os.IsNotExist(err) {
		t.Errorf("worktree should be removed; stat err = %v", err)
	}
	backlog, _ := filepath.Glob(filepath.Join(v.Path(), "tasks/backlog/T-099-*.md"))
	if len(backlog) != 1 {
		t.Errorf("expected task back in backlog/, got %v", backlog)
	}
	if strings.Contains(v.ReadBoard(), "active") && !strings.Contains(v.ReadBoard(), "T-099") {
		// sanity: board still references the task somewhere (loose check)
		t.Log(v.ReadBoard())
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
