package taskops

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/squashbox/squash-ide/internal/task"
)

func setupTestVault(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	// Create directory structure
	for _, dir := range []string{
		"tasks/backlog", "tasks/active", "tasks/blocked", "tasks/archive",
		"wiki/entities", "wiki",
	} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	// Create a backlog task file
	taskContent := `---
id: T-099
type: feature
title: Test task for spawn
project: test-proj
status: backlog
created: 2026-04-01
priority: high
related:
  - test-proj
---

# T-099 — Test task for spawn

Body content here.
`
	if err := os.WriteFile(filepath.Join(root, "tasks/backlog/T-099-test-task.md"), []byte(taskContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create board.md
	boardContent := `---
type: board
title: Test Board
last_updated: 2026-04-01

---

# Task Board

## Active

_None_

## Backlog

| ID | Project | Title | Type |
|----|---------|-------|------|
| [[T-099]] | test-proj | Test task for spawn | feature |

## Blocked

_None_

## Recently Completed

_None_
`
	if err := os.WriteFile(filepath.Join(root, "tasks/board.md"), []byte(boardContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create log.md
	logContent := `---
type: log
title: Test Log
---

# Activity Log

## [2026-04-01] init | Test vault created
Initial setup.
`
	if err := os.WriteFile(filepath.Join(root, "wiki/log.md"), []byte(logContent), 0o644); err != nil {
		t.Fatal(err)
	}

	return root
}

func TestMoveToActive(t *testing.T) {
	root := setupTestVault(t)
	tk := task.Task{
		ID:     "T-099",
		Status: "backlog",
		Title:  "Test task for spawn",
	}

	newPath, err := MoveToActive(root, tk)
	if err != nil {
		t.Fatalf("MoveToActive: %v", err)
	}

	// Verify old file is gone
	oldPath := filepath.Join(root, "tasks/backlog/T-099-test-task.md")
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Error("old task file still exists")
	}

	// Verify new file exists
	if _, err := os.Stat(newPath); err != nil {
		t.Fatalf("new task file missing: %v", err)
	}

	// Verify frontmatter was updated
	data, _ := os.ReadFile(newPath)
	content := string(data)
	if !strings.Contains(content, "status: active") {
		t.Error("status not updated to active")
	}
	if !strings.Contains(content, "started: ") {
		t.Error("started field not added")
	}
}

func TestUpdateBoard(t *testing.T) {
	root := setupTestVault(t)
	tk := task.Task{
		ID:      "T-099",
		Project: "test-proj",
		Title:   "Test task for spawn",
		Type:    "feature",
	}

	if err := UpdateBoard(root, tk); err != nil {
		t.Fatalf("UpdateBoard: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(root, "tasks/board.md"))
	content := string(data)

	// Task should be in Active section
	activeIdx := strings.Index(content, "## Active")
	backlogIdx := strings.Index(content, "## Backlog")
	taskIdx := strings.Index(content, "[[T-099]]")

	if taskIdx < activeIdx || taskIdx > backlogIdx {
		t.Error("task row not in Active section")
	}

	// _None_ should be replaced
	if strings.Contains(content[activeIdx:backlogIdx], "_None_") {
		t.Error("_None_ placeholder still present in Active section")
	}

	// Task should NOT be in Backlog anymore
	backlogEnd := strings.Index(content, "## Blocked")
	backlogSection := content[backlogIdx:backlogEnd]
	if strings.Contains(backlogSection, "[[T-099]]") {
		t.Error("task row still in Backlog section")
	}
}

func TestAppendLog(t *testing.T) {
	root := setupTestVault(t)
	tk := task.Task{
		ID:    "T-099",
		Title: "Test task for spawn",
	}

	if err := AppendLog(root, tk, "feat/T-099-test", "/tmp/worktree"); err != nil {
		t.Fatalf("AppendLog: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(root, "wiki/log.md"))
	content := string(data)

	if !strings.Contains(content, "spawn | T-099") {
		t.Error("log entry missing spawn operation")
	}
	if !strings.Contains(content, "feat/T-099-test") {
		t.Error("log entry missing branch name")
	}
}

func TestReplaceFrontmatterField(t *testing.T) {
	input := "---\nid: T-001\nstatus: backlog\n---\n"
	got := replaceFrontmatterField(input, "status", "active")
	if !strings.Contains(got, "status: active") {
		t.Errorf("field not replaced: %s", got)
	}
	if strings.Contains(got, "status: backlog") {
		t.Error("old value still present")
	}
}

func TestAddFrontmatterField(t *testing.T) {
	input := "---\nid: T-001\nstatus: backlog\n---\n# Body\n"
	got := addFrontmatterField(input, "started", "2026-04-14")
	if !strings.Contains(got, "started: 2026-04-14") {
		t.Errorf("field not added: %s", got)
	}
	// started should appear before closing ---
	startedIdx := strings.Index(got, "started:")
	closingIdx := strings.LastIndex(got, "---")
	if startedIdx > closingIdx {
		t.Error("started field added after closing delimiter")
	}
}

func TestInsertActiveRow_NonePlaceholder(t *testing.T) {
	board := "## Active\n\n_None_\n\n## Backlog\n"
	row := "| [[T-001]] | proj | Title | feature |"
	got := insertActiveRow(board, row)

	if strings.Contains(got, "_None_") {
		t.Error("_None_ placeholder not replaced")
	}
	if !strings.Contains(got, "[[T-001]]") {
		t.Error("task row not inserted")
	}
}

func TestInsertActiveRow_ExistingTable(t *testing.T) {
	board := "## Active\n\n| ID | Project | Title | Type |\n|----|---------|-------|------|\n| [[T-001]] | proj | First | feature |\n\n## Backlog\n"
	row := "| [[T-002]] | proj | Second | bug |"
	got := insertActiveRow(board, row)

	if !strings.Contains(got, "[[T-001]]") {
		t.Error("existing row removed")
	}
	if !strings.Contains(got, "[[T-002]]") {
		t.Error("new row not inserted")
	}
}
