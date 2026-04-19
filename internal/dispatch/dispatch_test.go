package dispatch

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/squashbox/squash-ide/internal/config"
	"github.com/squashbox/squash-ide/internal/ghx"
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

// TestComplete_RejectsBlocked guards the second non-active status rejected
// by the same gate as the original TestComplete_RejectsNonActive — the
// new switch in CompleteWithPR could regress this in isolation.
func TestComplete_RejectsBlocked(t *testing.T) {
	err := Complete(config.Config{}, task.Task{ID: "T-001", Status: "blocked"})
	if err == nil {
		t.Fatal("expected err")
	}
}

// TestComplete_DoneStatusWithoutArchiveFile rejects the inconsistent state
// where someone hand-edited frontmatter to status: done but didn't move
// the file. Without this guard, the rollout-recovery arm would silently
// proceed against a task that's not actually archived anywhere.
func TestComplete_DoneStatusWithoutArchiveFile(t *testing.T) {
	v := vaultfix.New(t)
	// Active file (NOT in archive/) but status field says "done" — the
	// inconsistency we want to catch.
	v.AddActive("T-099", "Inconsistent state",
		vaultfix.TaskOpts{Project: "squash-ide"})

	err := Complete(
		config.Config{Vault: v.Path(), Tmux: config.Tmux{Enabled: false}},
		task.Task{ID: "T-099", Status: "done", Title: "Inconsistent state",
			Project: "squash-ide", Repo: "/tmp/no-such-repo"},
	)
	if err == nil {
		t.Fatal("expected err for inconsistent state")
	}
	if !strings.Contains(err.Error(), "out of sync") &&
		!strings.Contains(err.Error(), "no file in tasks/archive") {
		t.Errorf("error should describe the mismatch: %v", err)
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

	// .mcp.json and .claude/settings.json both written by dispatch.
	wt := res.WorktreePath
	if _, err := os.Stat(filepath.Join(wt, ".mcp.json")); err != nil {
		t.Errorf(".mcp.json missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(wt, ".claude", "settings.json")); err != nil {
		t.Errorf(".claude/settings.json missing: %v", err)
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

// --- T-029: Complete owns task lifecycle, captures PR URL --------------

// stubGH installs a fakerunner as the ghx package-level runner that returns
// the given PR URL (or err on the gh call). repoPath is whatever the test's
// real worktree.Create produced — the recorded git-remote call uses it.
func stubGH(t *testing.T, repoPath, branch, prURL string, ghErr error) *fakerunner.Runner {
	t.Helper()
	r := fakerunner.New(t)
	r.AllowUnexpected = true // tests assert on what they care about, not call counts
	r.ExpectLookPath("gh").ReturnsLookPath("/usr/bin/gh")
	r.Expect("git", "-C", repoPath, "remote", "get-url", "origin").
		ReturnsOutput([]byte("git@github.com:squash-box/squash-ide.git\n"))
	exp := r.Expect("gh", "pr", "list",
		"-R", "squash-box/squash-ide",
		"--head", branch,
		"--json", "url",
		"--jq", ".[0].url",
	)
	if ghErr != nil {
		exp.ReturnsExitErr(ghErr)
	} else {
		exp.ReturnsOutput([]byte(prURL + "\n"))
	}
	prev := ghx.SetRunner(r)
	t.Cleanup(func() { ghx.SetRunner(prev) })
	return r
}

// stubGHMissing installs a fakerunner that reports `gh` is not on PATH.
func stubGHMissing(t *testing.T) {
	t.Helper()
	r := fakerunner.New(t)
	r.AllowUnexpected = true
	r.ExpectLookPath("gh").ReturnsLookPath("")
	prev := ghx.SetRunner(r)
	t.Cleanup(func() { ghx.SetRunner(prev) })
}

// completeFixture seeds an active task with a real worktree and returns the
// pieces tests need to drive Complete.
type completeFixture struct {
	cfg    config.Config
	task   task.Task
	repo   string
	wtPath string
	branch string
	vault  *vaultfix.Vault
}

func newCompleteFixture(t *testing.T) *completeFixture {
	t.Helper()
	origin := gitfix.NewBareOrigin(t)
	repo := gitfix.Clone(t, origin)

	v := vaultfix.New(t)
	v.AddActive("T-099", "Wrap up the thing",
		vaultfix.TaskOpts{Project: "squash-ide", Repo: repo})
	tk := task.Task{
		ID: "T-099", Title: "Wrap up the thing", Status: "active",
		Project: "squash-ide", Repo: repo,
	}
	branch := BranchFor(tk)
	wtPath, err := worktree.Create(repo, branch)
	if err != nil {
		t.Fatalf("seed worktree: %v", err)
	}

	return &completeFixture{
		cfg: config.Config{
			Vault: v.Path(),
			Tmux:  config.Tmux{Enabled: false},
		},
		task:   tk,
		repo:   repo,
		wtPath: wtPath,
		branch: branch,
		vault:  v,
	}
}

func TestComplete_HappyPath_CapturesPRURL(t *testing.T) {
	f := newCompleteFixture(t)
	prURL := "https://github.com/squash-box/squash-ide/pull/99"
	stubGH(t, f.repo, f.branch, prURL, nil)

	if err := Complete(f.cfg, f.task); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	// Worktree gone.
	if _, err := os.Stat(f.wtPath); !os.IsNotExist(err) {
		t.Errorf("worktree dir should have been removed; got stat err = %v", err)
	}
	// Task moved to archive with pr stamp.
	matches, _ := filepath.Glob(filepath.Join(f.vault.Path(), "tasks/archive/T-099-*.md"))
	if len(matches) != 1 {
		t.Fatalf("expected exactly one archived task file, got %v", matches)
	}
	body, _ := os.ReadFile(matches[0])
	if !strings.Contains(string(body), "pr: "+prURL) {
		t.Errorf("archived frontmatter missing pr field: %s", body)
	}
	// Log entry in new format.
	if !strings.Contains(f.vault.ReadLog(), "PR: "+prURL) {
		t.Errorf("log entry missing PR URL: %s", f.vault.ReadLog())
	}
}

func TestComplete_NoPR_DegradesGracefully(t *testing.T) {
	f := newCompleteFixture(t)
	stubGH(t, f.repo, f.branch, "", nil) // empty stdout → ErrNoPR

	if err := Complete(f.cfg, f.task); err != nil {
		t.Fatalf("Complete should succeed in degraded state, got: %v", err)
	}

	matches, _ := filepath.Glob(filepath.Join(f.vault.Path(), "tasks/archive/T-099-*.md"))
	if len(matches) != 1 {
		t.Fatalf("expected exactly one archived task file, got %v", matches)
	}
	body, _ := os.ReadFile(matches[0])
	if strings.Contains(string(body), "pr:") {
		t.Errorf("frontmatter should NOT have pr field when gh returned no URL: %s", body)
	}
	if strings.Contains(f.vault.ReadLog(), "PR:") {
		t.Errorf("log should NOT have PR suffix when gh returned no URL: %s", f.vault.ReadLog())
	}
}

func TestComplete_GHMissing_DegradesGracefully(t *testing.T) {
	f := newCompleteFixture(t)
	stubGHMissing(t)

	if err := Complete(f.cfg, f.task); err != nil {
		t.Fatalf("Complete should succeed when gh is missing, got: %v", err)
	}

	matches, _ := filepath.Glob(filepath.Join(f.vault.Path(), "tasks/archive/T-099-*.md"))
	if len(matches) != 1 {
		t.Fatalf("expected exactly one archived task file, got %v", matches)
	}
}

func TestComplete_TransientGHFailureSurfaces(t *testing.T) {
	f := newCompleteFixture(t)
	stubGH(t, f.repo, f.branch, "", fmt.Errorf("HTTP 502 bad gateway"))

	err := Complete(f.cfg, f.task)
	if err == nil {
		t.Fatal("expected transient gh failure to surface")
	}
	if errors.Is(err, ghx.ErrNoPR) || errors.Is(err, ghx.ErrGHMissing) {
		t.Errorf("transient error misclassified as a typed sentinel: %v", err)
	}
	// Side-effect guard: task must NOT have been archived.
	matches, _ := filepath.Glob(filepath.Join(f.vault.Path(), "tasks/archive/T-099-*.md"))
	if len(matches) != 0 {
		t.Errorf("archive populated despite gh failure: %v", matches)
	}
}

func TestCompleteWithPR_OverrideSkipsGH(t *testing.T) {
	f := newCompleteFixture(t)
	// Install a runner that has NO gh expectations — any gh call would
	// fail the test (AllowUnexpected stays off here on purpose).
	r := fakerunner.New(t)
	r.AllowUnexpected = true // we don't want the test to fail on the unrelated git-remote etc.
	prev := ghx.SetRunner(r)
	t.Cleanup(func() { ghx.SetRunner(prev) })

	override := "https://example.com/pr/override"
	if err := CompleteWithPR(f.cfg, f.task, override); err != nil {
		t.Fatalf("CompleteWithPR: %v", err)
	}

	// gh must NOT have been invoked when the override is present.
	for _, c := range r.Calls() {
		if c.Name == "gh" {
			t.Errorf("gh was invoked despite explicit --pr override: %+v", c)
		}
	}

	matches, _ := filepath.Glob(filepath.Join(f.vault.Path(), "tasks/archive/T-099-*.md"))
	if len(matches) != 1 {
		t.Fatalf("expected exactly one archived task file, got %v", matches)
	}
	body, _ := os.ReadFile(matches[0])
	if !strings.Contains(string(body), "pr: "+override) {
		t.Errorf("frontmatter missing pr override: %s", body)
	}
}

func TestCompleteWithPR_EmptyOverrideFallsBackToGH(t *testing.T) {
	f := newCompleteFixture(t)
	prURL := "https://github.com/squash-box/squash-ide/pull/123"
	r := stubGH(t, f.repo, f.branch, prURL, nil)

	if err := CompleteWithPR(f.cfg, f.task, ""); err != nil {
		t.Fatalf("CompleteWithPR: %v", err)
	}

	sawGH := false
	for _, c := range r.Calls() {
		if c.Name == "gh" {
			sawGH = true
			break
		}
	}
	if !sawGH {
		t.Error("empty override should fall back to gh; no gh call recorded")
	}
}

// TestComplete_RolloutRecovery covers the case where a legacy /implement run
// already archived the task (frontmatter status=done, file in tasks/archive)
// but left the worktree on disk. Pressing `c` should tear down the worktree
// and append a `complete-after` log entry, but must NOT re-move the file.
func TestComplete_RolloutRecovery(t *testing.T) {
	origin := gitfix.NewBareOrigin(t)
	repo := gitfix.Clone(t, origin)

	v := vaultfix.New(t)
	// Simulate the legacy state: task already in tasks/archive/ with
	// status: done, but worktree still on disk.
	archivePath := filepath.Join(v.Path(), "tasks", "archive", "T-099-rollout.md")
	body := `---
id: T-099
type: feature
title: Rollout recovery
project: squash-ide
status: done
priority: medium
created: 2026-04-17
completed: 2026-04-18
branch: feat/T-099-rollout-recovery
---

# T-099 — Rollout recovery
`
	if err := os.WriteFile(archivePath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	tk := task.Task{
		ID: "T-099", Title: "Rollout recovery", Status: "done",
		Project: "squash-ide", Repo: repo,
	}
	branch := BranchFor(tk)
	wtPath, err := worktree.Create(repo, branch)
	if err != nil {
		t.Fatalf("seed worktree: %v", err)
	}

	cfg := config.Config{Vault: v.Path(), Tmux: config.Tmux{Enabled: false}}
	stubGHMissing(t) // no need for a real PR lookup in this scenario

	if err := Complete(cfg, tk); err != nil {
		t.Fatalf("Complete (rollout recovery): %v", err)
	}

	// Worktree torn down.
	if _, err := os.Stat(wtPath); !os.IsNotExist(err) {
		t.Errorf("worktree should be removed; stat err = %v", err)
	}
	// Task file UNCHANGED (still exactly one file in archive/, untouched body).
	matches, _ := filepath.Glob(filepath.Join(v.Path(), "tasks/archive/T-099-*.md"))
	if len(matches) != 1 {
		t.Fatalf("expected exactly one archived file, got %v", matches)
	}
	got, _ := os.ReadFile(matches[0])
	// completed: 2026-04-18 from the legacy state must still be there — proves
	// MoveToArchive did NOT run (which would have stamped today's date).
	if !strings.Contains(string(got), "completed: 2026-04-18") {
		t.Errorf("rollout-recovery should not re-stamp completed; got: %s", got)
	}
	// Log entry uses complete-after.
	logs := v.ReadLog()
	if !strings.Contains(logs, "complete-after | T-099") {
		t.Errorf("log missing complete-after entry; got: %s", logs)
	}
	// Should NOT have a `complete | T-099` entry (only complete-after is allowed
	// in the rollout-recovery arm).
	if strings.Contains(logs, "complete | T-099") {
		t.Errorf("rollout-recovery should emit complete-after only, not complete; got: %s", logs)
	}
}
