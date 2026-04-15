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

// setupActiveTestVault returns a vault with T-099 already in active/ and
// its row in the Active section of board.md. Used for the cleanup tests.
func setupActiveTestVault(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	for _, dir := range []string{
		"tasks/backlog", "tasks/active", "tasks/blocked", "tasks/archive",
		"wiki/entities", "wiki",
	} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	taskContent := `---
id: T-099
type: feature
title: Test task for cleanup
project: test-proj
status: active
created: 2026-04-01
priority: high
started: 2026-04-14
---

# T-099 — Test task for cleanup

Body content here.
`
	if err := os.WriteFile(filepath.Join(root, "tasks/active/T-099-test-task.md"), []byte(taskContent), 0o644); err != nil {
		t.Fatal(err)
	}

	boardContent := `---
type: board
title: Test Board
last_updated: 2026-04-01

---

# Task Board

## Active

| ID | Project | Title | Type |
|----|---------|-------|------|
| [[T-099]] | test-proj | Test task for cleanup | feature |

## Backlog

_None_

## Blocked

_None_

## Recently Completed

_None_
`
	if err := os.WriteFile(filepath.Join(root, "tasks/board.md"), []byte(boardContent), 0o644); err != nil {
		t.Fatal(err)
	}

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

func TestMoveToArchive(t *testing.T) {
	root := setupActiveTestVault(t)
	tk := task.Task{
		ID:     "T-099",
		Status: "active",
		Title:  "Test task for cleanup",
	}

	newPath, err := MoveToArchive(root, tk, "feat/T-099-test", "")
	if err != nil {
		t.Fatalf("MoveToArchive: %v", err)
	}

	oldPath := filepath.Join(root, "tasks/active/T-099-test-task.md")
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Error("old task file still exists in active/")
	}
	if _, err := os.Stat(newPath); err != nil {
		t.Fatalf("archived task file missing: %v", err)
	}

	data, _ := os.ReadFile(newPath)
	content := string(data)
	if !strings.Contains(content, "status: done") {
		t.Errorf("status not set to done: %s", content)
	}
	if !strings.Contains(content, "completed: ") {
		t.Error("completed field not added")
	}
	if !strings.Contains(content, "branch: feat/T-099-test") {
		t.Error("branch field not recorded")
	}
}

func TestMoveToBlocked(t *testing.T) {
	root := setupActiveTestVault(t)
	tk := task.Task{
		ID:     "T-099",
		Status: "active",
		Title:  "Test task for cleanup",
	}

	newPath, err := MoveToBlocked(root, tk, "waiting on upstream fix")
	if err != nil {
		t.Fatalf("MoveToBlocked: %v", err)
	}

	oldPath := filepath.Join(root, "tasks/active/T-099-test-task.md")
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Error("old task file still exists in active/")
	}
	if _, err := os.Stat(newPath); err != nil {
		t.Fatalf("blocked task file missing: %v", err)
	}

	data, _ := os.ReadFile(newPath)
	content := string(data)
	if !strings.Contains(content, "status: blocked") {
		t.Error("status not set to blocked")
	}
	if !strings.Contains(content, "## Blocked") {
		t.Error("body not appended with Blocked section")
	}
	if !strings.Contains(content, "waiting on upstream fix") {
		t.Error("reason not recorded in body")
	}
}

func TestUpdateBoardComplete(t *testing.T) {
	root := setupActiveTestVault(t)
	tk := task.Task{
		ID:      "T-099",
		Project: "test-proj",
		Title:   "Test task for cleanup",
		Type:    "feature",
	}

	if err := UpdateBoardComplete(root, tk); err != nil {
		t.Fatalf("UpdateBoardComplete: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(root, "tasks/board.md"))
	content := string(data)

	completedIdx := strings.Index(content, "## Recently Completed")
	taskIdx := strings.Index(content, "[[T-099]]")
	if completedIdx < 0 || taskIdx < completedIdx {
		t.Errorf("task row not in Recently Completed section; content:\n%s", content)
	}

	// Active section should not contain the task anymore.
	activeIdx := strings.Index(content, "## Active")
	activeEnd := strings.Index(content, "## Backlog")
	if strings.Contains(content[activeIdx:activeEnd], "[[T-099]]") {
		t.Error("task still present in Active section")
	}
}

func TestUpdateBoardBlock(t *testing.T) {
	root := setupActiveTestVault(t)
	tk := task.Task{
		ID:      "T-099",
		Project: "test-proj",
		Title:   "Test task for cleanup",
		Type:    "feature",
	}

	if err := UpdateBoardBlock(root, tk); err != nil {
		t.Fatalf("UpdateBoardBlock: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(root, "tasks/board.md"))
	content := string(data)

	blockedIdx := strings.Index(content, "## Blocked")
	completedIdx := strings.Index(content, "## Recently Completed")
	taskIdx := strings.Index(content, "[[T-099]]")
	if taskIdx < blockedIdx || taskIdx > completedIdx {
		t.Errorf("task row not in Blocked section; content:\n%s", content)
	}

	// Active section should not contain the task anymore.
	activeIdx := strings.Index(content, "## Active")
	activeEnd := strings.Index(content, "## Backlog")
	if strings.Contains(content[activeIdx:activeEnd], "[[T-099]]") {
		t.Error("task still present in Active section")
	}
}

func TestAppendLogComplete(t *testing.T) {
	root := setupActiveTestVault(t)
	tk := task.Task{ID: "T-099", Title: "Test task for cleanup"}
	if err := AppendLogComplete(root, tk, "feat/T-099-test"); err != nil {
		t.Fatalf("AppendLogComplete: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(root, "wiki/log.md"))
	content := string(data)
	if !strings.Contains(content, "complete | T-099") {
		t.Errorf("log entry missing complete operation: %s", content)
	}
}

func TestAppendLogBlock(t *testing.T) {
	root := setupActiveTestVault(t)
	tk := task.Task{ID: "T-099", Title: "Test task for cleanup"}
	if err := AppendLogBlock(root, tk, "waiting on X"); err != nil {
		t.Fatalf("AppendLogBlock: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(root, "wiki/log.md"))
	content := string(data)
	if !strings.Contains(content, "block | T-099") {
		t.Errorf("log entry missing block operation: %s", content)
	}
	if !strings.Contains(content, "waiting on X") {
		t.Error("reason missing from log entry")
	}
}

func TestHasFrontmatterField(t *testing.T) {
	input := "---\nid: T-001\nstatus: backlog\n---\n# Body\n"
	if !hasFrontmatterField(input, "status") {
		t.Error("should detect existing field")
	}
	if hasFrontmatterField(input, "completed") {
		t.Error("should not detect missing field")
	}
	// Ensure we don't match the field when it appears in body.
	bodyOnly := "---\nid: T-001\n---\n# Body\ncompleted: yes\n"
	if hasFrontmatterField(bodyOnly, "completed") {
		t.Error("should not match fields outside frontmatter")
	}
}

func TestAddFrontmatterFieldReplacesExisting(t *testing.T) {
	input := "---\nid: T-001\nstatus: backlog\n---\n# Body\n"
	got := addFrontmatterField(input, "status", "done")
	if !strings.Contains(got, "status: done") {
		t.Error("existing field not updated")
	}
	if strings.Contains(got, "status: backlog") {
		t.Error("old value still present after replacement")
	}
}
