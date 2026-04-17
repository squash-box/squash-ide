package dispatch

import (
	"encoding/json"
	"errors"
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

func TestBranchFor(t *testing.T) {
	cases := []struct {
		id, title, want string
	}{
		{"T-001", "Fix the widget", "feat/T-001-fix-the-widget"},
		{"T-042", "  spaces  ", "feat/T-042-spaces"},
		{"T-100", "", "feat/T-100-"},
	}
	for _, c := range cases {
		got := BranchFor(task.Task{ID: c.id, Title: c.title})
		if got != c.want {
			t.Errorf("BranchFor(%q,%q) = %q, want %q", c.id, c.title, got, c.want)
		}
	}
}

func TestWriteMCPConfig_WritesValidJSON(t *testing.T) {
	wt := t.TempDir()
	if err := writeMCPConfig(wt, "T-042"); err != nil {
		t.Fatalf("writeMCPConfig: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(wt, ".mcp.json"))
	if err != nil {
		t.Fatalf("read .mcp.json: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, data)
	}
	servers, ok := out["mcpServers"].(map[string]any)
	if !ok {
		t.Fatalf("missing mcpServers key: %v", out)
	}
	squash, ok := servers["squash-ide"].(map[string]any)
	if !ok {
		t.Fatalf("missing squash-ide server: %v", servers)
	}
	env, ok := squash["env"].(map[string]any)
	if !ok {
		t.Fatalf("missing env: %v", squash)
	}
	if env["SQUASH_TASK_ID"] != "T-042" {
		t.Errorf("SQUASH_TASK_ID = %v, want T-042", env["SQUASH_TASK_ID"])
	}
}

func TestWriteMCPConfig_TargetNotWritable(t *testing.T) {
	err := writeMCPConfig("/definitely/not/a/real/dir/nope", "T-001")
	if err == nil {
		t.Fatal("expected err")
	}
}

func TestResolveRepo_PrefersTaskRepo(t *testing.T) {
	t.Setenv("HOME", "/home/me")
	tk := task.Task{ID: "T-001", Project: "proj", Repo: "~/repo-from-task"}
	got, err := resolveRepo(config.Config{}, tk)
	if err != nil {
		t.Fatal(err)
	}
	if got != "/home/me/repo-from-task" {
		t.Errorf("got %q", got)
	}
}

func TestResolveRepo_FallsBackToEntity(t *testing.T) {
	v := vaultfix.New(t)
	v.AddEntity("squash-ide", "/home/me/coderepo")

	got, err := resolveRepo(config.Config{Vault: v.Path()}, task.Task{Project: "squash-ide"})
	if err != nil {
		t.Fatalf("resolveRepo: %v", err)
	}
	if !strings.HasSuffix(got, "/home/me/coderepo") && got != "/home/me/coderepo" {
		t.Errorf("got %q", got)
	}
}

func TestResolveRepo_MissingEntity(t *testing.T) {
	v := vaultfix.New(t)
	_, err := resolveRepo(config.Config{Vault: v.Path()}, task.Task{Project: "ghost"})
	if err == nil {
		t.Fatal("expected err")
	}
}

func TestRun_RejectsNonBacklog(t *testing.T) {
	_, err := Run(config.Config{}, task.Task{ID: "T-001", Status: "active"})
	if err == nil {
		t.Fatal("expected err for active task")
	}
}

func TestComplete_RejectsNonActive(t *testing.T) {
	err := Complete(config.Config{}, task.Task{ID: "T-001", Status: "backlog"})
	if err == nil {
		t.Fatal("expected err")
	}
}

func TestBlock_RejectsNonActive(t *testing.T) {
	err := Block(config.Config{}, task.Task{ID: "T-001", Status: "backlog"}, "reason")
	if err == nil {
		t.Fatal("expected err")
	}
}

func TestBlock_RequiresReason(t *testing.T) {
	err := Block(config.Config{}, task.Task{ID: "T-001", Status: "active"}, "   ")
	if err == nil {
		t.Fatal("expected err for empty reason")
	}
	if !strings.Contains(err.Error(), "reason") {
		t.Errorf("should mention reason: %v", err)
	}
}

func TestDeactivate_RejectsNonActive(t *testing.T) {
	err := Deactivate(config.Config{}, task.Task{ID: "T-001", Status: "backlog"})
	if err == nil {
		t.Fatal("expected err")
	}
}

func TestRun_HappyPath(t *testing.T) {
	// Real git via gitfix — makes this an integration test (dispatch +
	// taskops + worktree + .mcp.json) with only the spawner stubbed.
	origin := gitfix.NewBareOrigin(t)
	repo := gitfix.Clone(t, origin)

	v := vaultfix.New(t)
	v.AddBacklog("T-001", "Ship the thing", vaultfix.TaskOpts{Project: "squash-ide", Repo: repo})

	tk := task.Task{
		ID:      "T-001",
		Title:   "Ship the thing",
		Status:  "backlog",
		Project: "squash-ide",
		Repo:    repo,
	}

	cfg := config.Config{
		Vault:    v.Path(),
		Tmux:     config.Tmux{Enabled: false},
		Terminal: config.Terminal{Command: "fake-term", Args: []string{"{exec}"}},
		Spawn:    config.Spawn{Command: "claude", Args: []string{}},
	}

	// Stub only the spawner's process runner.
	spFake := fakerunner.New(t)
	prevSP := spawner.SetRunner(spFake)
	t.Cleanup(func() { spawner.SetRunner(prevSP) })
	spFake.ExpectLookPath("fake-term").ReturnsLookPath("/bin/fake-term")
	spFake.Expect("/bin/fake-term", "claude")

	res, err := Run(cfg, tk)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Branch != "feat/T-001-ship-the-thing" {
		t.Errorf("branch = %q", res.Branch)
	}

	// Vault side effects.
	actives, _ := os.ReadDir(filepath.Join(v.Path(), "tasks/active"))
	if len(actives) == 0 {
		t.Fatal("task not moved to active/")
	}
	if !strings.Contains(v.ReadBoard(), "T-001") {
		t.Error("board missing T-001")
	}
	if !strings.Contains(v.ReadLog(), "T-001") {
		t.Error("log missing T-001")
	}

	wt := res.WorktreePath
	if _, err := os.Stat(filepath.Join(wt, ".mcp.json")); err != nil {
		t.Errorf(".mcp.json missing: %v", err)
	}
}

func TestWorktreePathFor(t *testing.T) {
	t.Setenv("HOME", "/home/me")
	tk := task.Task{ID: "T-001", Title: "test thing", Repo: "/tmp/repo"}
	got, err := WorktreePathFor(config.Config{}, tk)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(got, "worktrees/feat/T-001-test-thing") {
		t.Errorf("unexpected path: %q", got)
	}
}

// --- T-020: structured worktree-error pass-through ---------------------

// TestRun_OrphanErrorPassThrough confirms that when the worktree path is
// already occupied by a non-registered directory with a .git reference,
// Run surfaces the typed error (via errors.Is) AND annotates the message
// with operator-facing subcommand hints naming the exact recovery path.
func TestRun_OrphanErrorPassThrough(t *testing.T) {
	origin := gitfix.NewBareOrigin(t)
	repo := gitfix.Clone(t, origin)

	v := vaultfix.New(t)
	v.AddBacklog("T-020", "Dispatch orphan test", vaultfix.TaskOpts{Project: "squash-ide", Repo: repo})
	tk := task.Task{
		ID:      "T-020",
		Title:   "Dispatch orphan test",
		Status:  "backlog",
		Project: "squash-ide",
		Repo:    repo,
	}

	// Pre-seed an orphan directory at the canonical worktree path.
	branch := BranchFor(tk)
	orphanPath := worktree.Path(repo, branch)
	if err := os.MkdirAll(orphanPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(orphanPath, ".git"), []byte("gitdir: "+repo+"/.git\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := config.Config{Vault: v.Path(), Tmux: config.Tmux{Enabled: false}}

	_, err := Run(cfg, tk)
	if err == nil {
		t.Fatal("Run: expected error on orphan worktree path")
	}
	if !errors.Is(err, worktree.ErrWorktreeOrphan) {
		t.Errorf("expected errors.Is(err, ErrWorktreeOrphan), got %v", err)
	}
	if !strings.Contains(err.Error(), "squash-ide worktree adopt T-020") {
		t.Errorf("expected adopt hint in message, got: %v", err)
	}
	if !strings.Contains(err.Error(), "squash-ide worktree clean T-020") {
		t.Errorf("expected clean hint in message, got: %v", err)
	}

	// Orphan dir must not have been deleted by Run.
	if _, err := os.Stat(orphanPath); err != nil {
		t.Errorf("orphan dir disappeared: %v", err)
	}

	// Task must still be in backlog (no vault mutation on failure).
	matches, _ := filepath.Glob(filepath.Join(v.Path(), "tasks/backlog/T-020-*.md"))
	if len(matches) == 0 {
		t.Errorf("task file moved out of backlog after failed spawn")
	}
}

// TestRun_NonGitOrphanErrorPassThrough covers the sibling "no .git reference
// at all" classification, which gets a different operator hint — inspect
// manually, then clean.
func TestRun_NonGitOrphanErrorPassThrough(t *testing.T) {
	origin := gitfix.NewBareOrigin(t)
	repo := gitfix.Clone(t, origin)

	v := vaultfix.New(t)
	v.AddBacklog("T-020", "Dispatch orphan test", vaultfix.TaskOpts{Project: "squash-ide", Repo: repo})
	tk := task.Task{
		ID:      "T-020",
		Title:   "Dispatch orphan test",
		Status:  "backlog",
		Project: "squash-ide",
		Repo:    repo,
	}

	branch := BranchFor(tk)
	orphanPath := worktree.Path(repo, branch)
	if err := os.MkdirAll(orphanPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(orphanPath, "bystander.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := config.Config{Vault: v.Path(), Tmux: config.Tmux{Enabled: false}}

	_, err := Run(cfg, tk)
	if !errors.Is(err, worktree.ErrWorktreeNotAGitDir) {
		t.Fatalf("expected errors.Is(err, ErrWorktreeNotAGitDir), got %v", err)
	}
	if !strings.Contains(err.Error(), "inspect the directory manually") {
		t.Errorf("expected manual-inspect hint, got: %v", err)
	}
}
