//go:build e2e

package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/squashbox/squash-ide/internal/config"
	"github.com/squashbox/squash-ide/internal/status"
	"github.com/squashbox/squash-ide/internal/testutil/vaultfix"
	"github.com/squashbox/squash-ide/internal/ui"
	"github.com/squashbox/squash-ide/internal/vault"
)

// TestStaleBadge_RendersIdleNotWorking is the T-023 end-to-end guard.
//
// It writes a status file for an active task with Updated deliberately set
// past status.StaleDuration, then drives the UI model through a single tick
// that uses the real status.ReadAll (which filters the stale file out).
// The rendered list view must show ○ IDLE for that task — never ● WORKING.
//
// Before the T-023 fix this test would fail: status.ReadAll drops the aged
// file, the list render path receives a nil sub for the task, and
// activeBadge's "state := working" default paints ● WORKING.
func TestStaleBadge_RendersIdleNotWorking(t *testing.T) {
	// Redirect the status dir to a clean temp path so we don't collide with
	// other e2e tests or leave state behind.
	statusDir := t.TempDir()
	restore := status.SetDirForTesting(statusDir)
	t.Cleanup(restore)

	taskID := "T-023e2e"

	// Seed a vault with one active task whose ID matches the stale file.
	v := vaultfix.New(t)
	v.AddActive(taskID, "Stale badge repro", vaultfix.TaskOpts{Project: "squash-ide"})

	// Write the stale status file directly. Updated is 10 minutes in the
	// past — well past status.StaleDuration (5 min).
	stale := status.File{
		TaskID:  taskID,
		State:   "idle",
		Message: "Turn complete (10m ago)",
		Updated: time.Now().Add(-10 * time.Minute).Unix(),
	}
	data, err := json.Marshal(stale)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(statusDir, taskID+".json"), data, 0o644); err != nil {
		t.Fatalf("write stale file: %v", err)
	}

	// Sanity check: ReadAll must filter the stale file out — this is the
	// precondition that makes the render test meaningful.
	all, err := status.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if _, present := all[taskID]; present {
		t.Fatalf("expected stale file to be filtered by ReadAll, got %+v", all)
	}

	// Load the vault and build a model matching what the TUI would have
	// on-screen one tick later.
	tasks, err := vault.ReadAll(v.Path())
	if err != nil {
		t.Fatalf("vault.ReadAll: %v", err)
	}

	cfg := config.Defaults()
	cfg.Vault = v.Path()
	m := ui.NewForTest(cfg, tasks, all)

	rendered := m.View()

	if strings.Contains(rendered, "WORKING") {
		t.Errorf("stale task %s should NOT render WORKING, view:\n%s", taskID, rendered)
	}
	if !strings.Contains(rendered, "IDLE") {
		t.Errorf("stale task %s should render IDLE, view:\n%s", taskID, rendered)
	}
}
