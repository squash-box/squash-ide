//go:build e2e

// Package e2e drives the built squash-ide binary against a fixture vault
// and an ephemeral git repo, verifying the vault/worktree lifecycle
// end-to-end. Tests are gated behind the `e2e` build tag so plain
// `go test ./...` stays fast and free of git side effects.
//
// Run:
//
//	make test-e2e
//	# or
//	go test -tags=e2e ./e2e/...
package e2e

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/squashbox/squash-ide/internal/task"
	"github.com/squashbox/squash-ide/internal/testutil/gitfix"
	"github.com/squashbox/squash-ide/internal/testutil/vaultfix"
)

// bin is the absolute path to the built squash-ide binary. It is built
// once per test binary in TestMain and reused across tests.
var bin string

func TestMain(m *testing.M) {
	// Build a fresh copy of the binary into a temp dir — the e2e tests run
	// it via os/exec and inspect its side effects.
	tmp, err := os.MkdirTemp("", "squash-ide-e2e-")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(tmp)

	bin = filepath.Join(tmp, "squash-ide")
	build := exec.Command("go", "build", "-o", bin, "../cmd/squash-ide")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		panic("building squash-ide: " + err.Error())
	}
	os.Exit(m.Run())
}

// runBin runs the binary with args + env and returns stdout, stderr, and err.
func runBin(t *testing.T, env []string, args ...string) (string, string, error) {
	t.Helper()
	c := exec.Command(bin, args...)
	c.Env = append(os.Environ(), env...)
	var stdout, stderr strings.Builder
	c.Stdout = &stdout
	c.Stderr = &stderr
	err := c.Run()
	return stdout.String(), stderr.String(), err
}

// TestList_EmptyVault asserts that list against a brand-new vault emits
// an empty JSON array and exits zero.
func TestList_EmptyVault(t *testing.T) {
	v := vaultfix.New(t)
	stdout, stderr, err := runBin(t, nil, "--vault", v.Path(), "list")
	if err != nil {
		t.Fatalf("list: %v\nstderr: %s", err, stderr)
	}
	// Empty vault: JSON should be "null" or "[]".
	out := strings.TrimSpace(stdout)
	if out != "null" && out != "[]" {
		t.Errorf("unexpected output: %q", out)
	}
}

// TestList_BacklogTask asserts that a seeded backlog task is visible.
func TestList_BacklogTask(t *testing.T) {
	v := vaultfix.New(t)
	v.AddBacklog("T-001", "Hello world", vaultfix.TaskOpts{Project: "p"})
	stdout, stderr, err := runBin(t, nil, "--vault", v.Path(), "list")
	if err != nil {
		t.Fatalf("list: %v\nstderr: %s", err, stderr)
	}
	var tasks []task.Task
	if err := json.Unmarshal([]byte(stdout), &tasks); err != nil {
		t.Fatalf("json: %v\n%s", err, stdout)
	}
	if len(tasks) != 1 || tasks[0].ID != "T-001" {
		t.Fatalf("got %+v", tasks)
	}
}

// TestList_StatusFilter asserts that --status=backlog narrows results.
func TestList_StatusFilter(t *testing.T) {
	v := vaultfix.New(t)
	v.AddBacklog("T-001", "one")
	v.AddActive("T-002", "two")

	stdout, stderr, err := runBin(t, nil, "--vault", v.Path(), "list", "--status=backlog")
	if err != nil {
		t.Fatalf("list --status: %v\nstderr: %s", err, stderr)
	}
	var tasks []task.Task
	if err := json.Unmarshal([]byte(stdout), &tasks); err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || tasks[0].ID != "T-001" {
		t.Errorf("got %+v", tasks)
	}
}

// TestConfig_ShowsVault verifies the `config` subcommand emits the
// configured vault path.
func TestConfig_ShowsVault(t *testing.T) {
	v := vaultfix.New(t)
	stdout, _, err := runBin(t, nil, "--vault", v.Path(), "config")
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	if !strings.Contains(stdout, v.Path()) {
		t.Errorf("config output missing vault path: %s", stdout)
	}
}

// TestSpawn_DryRun exercises the spawn subcommand's --dry-run mode —
// no worktree, no vault mutation, just a report. Fastest end-to-end
// check that the cobra wiring + vault load + task lookup work.
func TestSpawn_DryRun(t *testing.T) {
	origin := gitfix.NewBareOrigin(t)
	repo := gitfix.Clone(t, origin)

	v := vaultfix.New(t)
	v.AddBacklog("T-001", "dry run", vaultfix.TaskOpts{Project: "p", Repo: repo})

	stdout, stderr, err := runBin(t,
		nil,
		"--vault", v.Path(), "--no-tmux",
		"spawn", "T-001", "--dry-run",
	)
	if err != nil {
		t.Fatalf("spawn --dry-run: %v\nstderr: %s", err, stderr)
	}
	if !strings.Contains(stdout, "DRY RUN") {
		t.Errorf("missing DRY RUN header: %s", stdout)
	}
	// Dry run must not move the file.
	if _, err := os.Stat(filepath.Join(v.Path(), "tasks/backlog")); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(filepath.Join(v.Path(), "tasks/active")); len(entries) != 0 {
		t.Errorf("dry run must not create active entries, got %d", len(entries))
	}
}

// TestSpawn_TaskNotFound verifies a readable error for a missing task ID.
func TestSpawn_TaskNotFound(t *testing.T) {
	v := vaultfix.New(t)
	_, stderr, err := runBin(t, nil,
		"--vault", v.Path(), "--no-tmux", "spawn", "T-999",
	)
	if err == nil {
		t.Fatal("expected non-zero exit for missing task")
	}
	if !strings.Contains(stderr, "T-999") {
		t.Errorf("stderr should mention T-999: %s", stderr)
	}
}
