package ui

import (
	"testing"

	"github.com/squashbox/squash-ide/internal/task"
)

func testTasks() []task.Task {
	return []task.Task{
		{ID: "T-001", Type: "feature", Title: "First task", Project: "proj", Status: "backlog", Priority: "high"},
		{ID: "T-002", Type: "bug", Title: "Second task", Project: "proj", Status: "backlog", Priority: "medium"},
		{ID: "T-003", Type: "feature", Title: "Active task", Project: "proj", Status: "active", Priority: "high"},
		{ID: "T-004", Type: "chore", Title: "Blocked task", Project: "proj", Status: "blocked", Priority: "low"},
		{ID: "T-005", Type: "feature", Title: "Done task", Project: "proj", Status: "done", Priority: "low"},
	}
}

func modelWithTasks(tasks []task.Task) Model {
	m := New("/fake/vault")
	m.allTasks = tasks
	m.width = 80
	m.height = 24
	m.buildItems()
	m.applyFilter()
	m.clampCursor()
	return m
}

func TestBuildItems_GroupsByStatus(t *testing.T) {
	m := modelWithTasks(testTasks())

	// Should have headers for backlog, active, blocked (not done/archive)
	headers := 0
	taskItems := 0
	for _, item := range m.items {
		if item.isHeader {
			headers++
		} else {
			taskItems++
		}
	}
	if headers != 3 {
		t.Errorf("expected 3 status headers, got %d", headers)
	}
	// 4 tasks in backlog+active+blocked (done is excluded from display)
	if taskItems != 4 {
		t.Errorf("expected 4 task items, got %d", taskItems)
	}
}

func TestBuildItems_StatusOrder(t *testing.T) {
	m := modelWithTasks(testTasks())

	var headerOrder []string
	for _, item := range m.items {
		if item.isHeader {
			headerOrder = append(headerOrder, item.header)
		}
	}
	expected := []string{"backlog", "active", "blocked"}
	if len(headerOrder) != len(expected) {
		t.Fatalf("header count %d != expected %d", len(headerOrder), len(expected))
	}
	for i, h := range headerOrder {
		if h != expected[i] {
			t.Errorf("header[%d] = %q, want %q", i, h, expected[i])
		}
	}
}

func TestCursorSkipsHeaders(t *testing.T) {
	m := modelWithTasks(testTasks())

	// Cursor should start on first task, not the header
	if m.cursor < 0 || m.cursor >= len(m.filtered) {
		t.Fatalf("cursor %d out of range", m.cursor)
	}
	if m.filtered[m.cursor].isHeader {
		t.Error("cursor should not be on a header")
	}
	if m.filtered[m.cursor].task.ID != "T-001" {
		t.Errorf("cursor should be on T-001, got %s", m.filtered[m.cursor].task.ID)
	}
}

func TestMoveCursor(t *testing.T) {
	m := modelWithTasks(testTasks())

	// Move down to second task
	m.moveCursor(1)
	if m.filtered[m.cursor].task.ID != "T-002" {
		t.Errorf("expected T-002, got %s", m.filtered[m.cursor].task.ID)
	}

	// Move down again — should skip "active" header to T-003
	m.moveCursor(1)
	if m.filtered[m.cursor].task.ID != "T-003" {
		t.Errorf("expected T-003 (skipping header), got %s", m.filtered[m.cursor].task.ID)
	}

	// Move up — should skip header back to T-002
	m.moveCursor(-1)
	if m.filtered[m.cursor].task.ID != "T-002" {
		t.Errorf("expected T-002 (skipping header), got %s", m.filtered[m.cursor].task.ID)
	}
}

func TestMoveCursor_BoundsCheck(t *testing.T) {
	m := modelWithTasks(testTasks())

	// Move up from first item — should stay
	m.moveCursor(-1)
	if m.filtered[m.cursor].task.ID != "T-001" {
		t.Errorf("should stay on T-001, got %s", m.filtered[m.cursor].task.ID)
	}

	// Move to last item
	for i := 0; i < 10; i++ {
		m.moveCursor(1)
	}
	if m.filtered[m.cursor].task.ID != "T-004" {
		t.Errorf("should be on last task T-004, got %s", m.filtered[m.cursor].task.ID)
	}

	// Move down from last item — should stay
	m.moveCursor(1)
	if m.filtered[m.cursor].task.ID != "T-004" {
		t.Errorf("should stay on T-004, got %s", m.filtered[m.cursor].task.ID)
	}
}

func TestFilter(t *testing.T) {
	m := modelWithTasks(testTasks())

	m.filter = "active"
	m.applyFilter()
	m.clampCursor()

	taskCount := 0
	for _, item := range m.filtered {
		if !item.isHeader {
			taskCount++
		}
	}
	if taskCount != 1 {
		t.Errorf("filter 'active' should match 1 task, got %d", taskCount)
	}
}

func TestFilter_ByID(t *testing.T) {
	m := modelWithTasks(testTasks())

	m.filter = "T-002"
	m.applyFilter()
	m.clampCursor()

	taskCount := 0
	for _, item := range m.filtered {
		if !item.isHeader {
			taskCount++
		}
	}
	if taskCount != 1 {
		t.Errorf("filter 'T-002' should match 1 task, got %d", taskCount)
	}
}

func TestFilter_CaseInsensitive(t *testing.T) {
	m := modelWithTasks(testTasks())

	m.filter = "FIRST"
	m.applyFilter()
	m.clampCursor()

	taskCount := 0
	for _, item := range m.filtered {
		if !item.isHeader {
			taskCount++
		}
	}
	if taskCount != 1 {
		t.Errorf("filter 'FIRST' should case-insensitively match 1 task, got %d", taskCount)
	}
}

func TestFilter_EmptyResult(t *testing.T) {
	m := modelWithTasks(testTasks())

	m.filter = "nonexistent"
	m.applyFilter()
	m.clampCursor()

	if len(m.filtered) != 0 {
		t.Errorf("filter 'nonexistent' should match 0 items, got %d", len(m.filtered))
	}
}

func TestFilter_ClearRestoresAll(t *testing.T) {
	m := modelWithTasks(testTasks())
	originalCount := len(m.filtered)

	m.filter = "T-001"
	m.applyFilter()

	m.filter = ""
	m.applyFilter()

	if len(m.filtered) != originalCount {
		t.Errorf("clearing filter should restore all %d items, got %d", originalCount, len(m.filtered))
	}
}

func TestEmptyVault(t *testing.T) {
	m := modelWithTasks(nil)

	if len(m.items) != 0 {
		t.Errorf("empty vault should have 0 items, got %d", len(m.items))
	}

	// View should render without panic
	v := m.View()
	if v == "" {
		t.Error("View() returned empty string for empty vault")
	}
}

func TestStatusBar_Counts(t *testing.T) {
	m := modelWithTasks(testTasks())
	bar := m.renderStatusBar()

	if bar == "" {
		t.Error("status bar is empty")
	}
	// Should contain counts
	if !containsAll(bar, "2 backlog", "1 active", "1 blocked") {
		t.Errorf("status bar missing expected counts: %s", bar)
	}
}

func containsAll(s string, substrings ...string) bool {
	for _, sub := range substrings {
		found := false
		for i := 0; i <= len(s)-len(sub); i++ {
			if s[i:i+len(sub)] == sub {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
