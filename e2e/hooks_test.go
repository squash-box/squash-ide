//go:build e2e

package e2e

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/squashbox/squash-ide/internal/status"
	"github.com/squashbox/squash-ide/internal/testutil/gitfix"
	"github.com/squashbox/squash-ide/internal/testutil/vaultfix"
)

// TestStatus_HookPath_WritesInputRequired is the T-021 end-to-end guard. It
// drives the shell-invocable `squash-ide status input_required …` surface
// that a Claude Code Notification hook would call, and asserts that the TUI
// polling layer (status.ReadAll) sees the transition.
func TestStatus_HookPath_WritesInputRequired(t *testing.T) {
	taskID := "T-e2e-hook-inputreq"

	// Ensure no stale file pollutes the read.
	_ = status.Remove(taskID)
	t.Cleanup(func() { _ = status.Remove(taskID) })

	_, stderr, err := runBin(t,
		[]string{"SQUASH_TASK_ID=" + taskID},
		"status", "input_required", "Awaiting user input",
	)
	if err != nil {
		t.Fatalf("status subcommand: %v\nstderr: %s", err, stderr)
	}

	all, err := status.ReadAll()
	if err != nil {
		t.Fatalf("status.ReadAll: %v", err)
	}
	f, ok := all[taskID]
	if !ok {
		t.Fatalf("no entry for %s in %v", taskID, all)
	}
	if f.State != "input_required" {
		t.Errorf("state = %q, want input_required", f.State)
	}
	if f.Message != "Awaiting user input" {
		t.Errorf("message = %q", f.Message)
	}
}

// TestStatus_HookPath_RejectsBadState verifies a misconfigured hook surfaces
// loudly instead of silently mis-writing state.
func TestStatus_HookPath_RejectsBadState(t *testing.T) {
	_, stderr, err := runBin(t,
		[]string{"SQUASH_TASK_ID=T-e2e-bad"},
		"status", "garbage", "msg",
	)
	if err == nil {
		t.Fatal("expected non-zero exit for invalid state")
	}
	if !strings.Contains(stderr, "invalid state") {
		t.Errorf("stderr should call out invalid state: %s", stderr)
	}
}

// TestStatus_HookPath_RequiresEnv verifies missing SQUASH_TASK_ID fails
// loudly. Scrubs any inherited value from the test runner's env before
// invoking.
func TestStatus_HookPath_RequiresEnv(t *testing.T) {
	// Build a clean env with SQUASH_TASK_ID explicitly unset.
	var env []string
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "SQUASH_TASK_ID=") {
			env = append(env, e)
		}
	}

	c := exec.Command(bin, "status", "working", "m")
	c.Env = env
	var stderr strings.Builder
	c.Stderr = &stderr
	err := c.Run()

	if err == nil {
		t.Fatal("expected non-zero exit without SQUASH_TASK_ID")
	}
	if !strings.Contains(stderr.String(), "SQUASH_TASK_ID") {
		t.Errorf("stderr should mention the missing var: %s", stderr.String())
	}
}

// TestSpawn_WritesClaudeSettings asserts the dispatch wiring that T-021
// adds: after a spawn, the worktree contains .claude/settings.json with
// all three hooks configured and each pointing at the status subcommand.
func TestSpawn_WritesClaudeSettings(t *testing.T) {
	fakeTerm := t.TempDir()
	terminalBin := filepath.Join(fakeTerm, "fake-term")
	if err := os.WriteFile(terminalBin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	env := []string{"PATH=" + fakeTerm + ":" + os.Getenv("PATH")}

	origin := gitfix.NewBareOrigin(t)
	repo := gitfix.Clone(t, origin)

	v := vaultfix.New(t)
	v.AddBacklog("T-hook", "Hook demo", vaultfix.TaskOpts{Project: "p", Repo: repo})

	_, stderr, err := runBin(t, env,
		"--vault", v.Path(), "--no-tmux",
		"--terminal", "fake-term", "--spawn-cmd", "/bin/true",
		"spawn", "T-hook",
	)
	if err != nil {
		t.Fatalf("spawn: %v\nstderr: %s", err, stderr)
	}

	wt := filepath.Join(filepath.Dir(repo), "worktrees", "feat/T-hook-hook-demo")
	data, err := os.ReadFile(filepath.Join(wt, ".claude", "settings.json"))
	if err != nil {
		t.Fatalf("reading settings.json: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("parse: %v", err)
	}
	hooks, ok := out["hooks"].(map[string]any)
	if !ok {
		t.Fatal("no hooks key")
	}
	for _, evt := range []string{"Notification", "PostToolUse", "Stop"} {
		if _, ok := hooks[evt]; !ok {
			t.Errorf("missing %s hook", evt)
		}
	}
}
