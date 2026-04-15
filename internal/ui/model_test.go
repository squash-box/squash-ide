package ui

import (
	"fmt"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/squashbox/squash-ide/internal/config"
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
	cfg := config.Defaults()
	cfg.Vault = "/fake/vault"
	m := New(cfg)
	m.allTasks = tasks
	m.width = 80
	m.height = 24
	m.buildItems()
	m.applyFilter()
	m.clampCursor()
	return m
}

func keyMsg(k string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
}

func enterKeyMsg() tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyEnter}
}

func escKeyMsg() tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyEsc}
}

func tabKeyMsg() tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyTab}
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

// --- T-008: dispatch wiring tests ---

func TestEnterOnBacklogTask_ShowsConfirm(t *testing.T) {
	m := modelWithTasks(testTasks())
	// Cursor is on T-001 (backlog)

	result, _ := m.Update(enterKeyMsg())
	updated := result.(Model)

	if updated.confirming == nil {
		t.Fatal("expected confirming to be set for backlog task")
	}
	if updated.confirming.ID != "T-001" {
		t.Errorf("expected confirming T-001, got %s", updated.confirming.ID)
	}
}

func TestEnterOnActiveTask_ShowsAlreadyStatus(t *testing.T) {
	m := modelWithTasks(testTasks())
	// Move cursor to T-003 (active)
	m.moveCursor(1) // T-002
	m.moveCursor(1) // T-003

	if m.filtered[m.cursor].task.ID != "T-003" {
		t.Fatalf("expected cursor on T-003, got %s", m.filtered[m.cursor].task.ID)
	}

	result, _ := m.Update(enterKeyMsg())
	updated := result.(Model)

	if updated.confirming != nil {
		t.Error("should not show confirmation for active task")
	}
	if updated.statusMsg != "T-003 is already active" {
		t.Errorf("expected 'T-003 is already active', got %q", updated.statusMsg)
	}
}

func TestEnterOnBlockedTask_ShowsAlreadyStatus(t *testing.T) {
	m := modelWithTasks(testTasks())
	// Move cursor to T-004 (blocked)
	m.moveCursor(1) // T-002
	m.moveCursor(1) // T-003
	m.moveCursor(1) // T-004

	if m.filtered[m.cursor].task.ID != "T-004" {
		t.Fatalf("expected cursor on T-004, got %s", m.filtered[m.cursor].task.ID)
	}

	result, _ := m.Update(enterKeyMsg())
	updated := result.(Model)

	if updated.confirming != nil {
		t.Error("should not show confirmation for blocked task")
	}
	if updated.statusMsg != "T-004 is already blocked" {
		t.Errorf("expected 'T-004 is already blocked', got %q", updated.statusMsg)
	}
}

func TestConfirmDialog_YConfirms(t *testing.T) {
	m := modelWithTasks(testTasks())
	backlogTask := m.filtered[m.cursor].task
	m.confirming = &backlogTask

	result, cmd := m.Update(keyMsg("y"))
	updated := result.(Model)

	if updated.confirming != nil {
		t.Error("confirming should be nil after y")
	}
	if !updated.dispatching {
		t.Error("dispatching should be true after confirm")
	}
	if cmd == nil {
		t.Error("expected a tea.Cmd for async dispatch")
	}
}

func TestConfirmDialog_EnterConfirms(t *testing.T) {
	m := modelWithTasks(testTasks())
	backlogTask := m.filtered[m.cursor].task
	m.confirming = &backlogTask

	result, cmd := m.Update(enterKeyMsg())
	updated := result.(Model)

	if updated.confirming != nil {
		t.Error("confirming should be nil after enter")
	}
	if !updated.dispatching {
		t.Error("dispatching should be true after confirm")
	}
	if cmd == nil {
		t.Error("expected a tea.Cmd for async dispatch")
	}
}

func TestConfirmDialog_NCancels(t *testing.T) {
	m := modelWithTasks(testTasks())
	backlogTask := m.filtered[m.cursor].task
	m.confirming = &backlogTask

	result, _ := m.Update(keyMsg("n"))
	updated := result.(Model)

	if updated.confirming != nil {
		t.Error("confirming should be nil after n")
	}
	if updated.dispatching {
		t.Error("dispatching should be false after cancel")
	}
}

func TestConfirmDialog_EscCancels(t *testing.T) {
	m := modelWithTasks(testTasks())
	backlogTask := m.filtered[m.cursor].task
	m.confirming = &backlogTask

	result, _ := m.Update(escKeyMsg())
	updated := result.(Model)

	if updated.confirming != nil {
		t.Error("confirming should be nil after esc")
	}
	if updated.dispatching {
		t.Error("dispatching should be false after cancel")
	}
}

func TestConfirmDialog_RendersInView(t *testing.T) {
	m := modelWithTasks(testTasks())
	backlogTask := m.filtered[m.cursor].task
	m.confirming = &backlogTask

	v := m.View()
	if !containsAll(v, "Spawn T-001?", "[y/N]") {
		t.Errorf("confirm dialog not rendered in view: %s", v)
	}
}

func TestConfirmDialog_HelpTextChanges(t *testing.T) {
	m := modelWithTasks(testTasks())
	backlogTask := m.filtered[m.cursor].task
	m.confirming = &backlogTask

	v := m.View()
	if !containsAll(v, "confirm", "cancel") {
		t.Errorf("help text should show confirm/cancel options: %s", v)
	}
}

func TestDispatchDoneMsg_ShowsSuccess(t *testing.T) {
	m := modelWithTasks(testTasks())
	m.dispatching = true

	result, _ := m.Update(dispatchDoneMsg{taskID: "T-001", branch: "feat/T-001-test"})
	updated := result.(Model)

	if updated.dispatching {
		t.Error("dispatching should be false after done")
	}
	if updated.statusMsg != "spawned T-001" {
		t.Errorf("expected 'spawned T-001', got %q", updated.statusMsg)
	}
	if updated.statusIsErr {
		t.Error("status should not be error after success")
	}
}

func TestDispatchErrMsg_ShowsError(t *testing.T) {
	m := modelWithTasks(testTasks())
	m.dispatching = true

	result, _ := m.Update(dispatchErrMsg{err: fmt.Errorf("git fetch failed")})
	updated := result.(Model)

	if updated.dispatching {
		t.Error("dispatching should be false after error")
	}
	if updated.statusMsg != "git fetch failed" {
		t.Errorf("expected 'git fetch failed', got %q", updated.statusMsg)
	}
	if !updated.statusIsErr {
		t.Error("status should be error")
	}
}

func TestStatusBar_ShowsStatusMsg(t *testing.T) {
	m := modelWithTasks(testTasks())
	m.statusMsg = "spawned T-001"
	m.statusIsErr = false

	bar := m.renderStatusBar()
	if !containsAll(bar, "spawned T-001") {
		t.Errorf("status bar should show status message: %s", bar)
	}
}

func TestStatusBar_ClearsOnKeypress(t *testing.T) {
	m := modelWithTasks(testTasks())
	m.statusMsg = "spawned T-001"

	// Press down arrow to clear status
	result, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated := result.(Model)

	if updated.statusMsg != "" {
		t.Errorf("status message should be cleared after keypress, got %q", updated.statusMsg)
	}
}

func TestTabOpensDetailView(t *testing.T) {
	m := modelWithTasks(testTasks())

	result, _ := m.Update(tabKeyMsg())
	updated := result.(Model)

	if updated.view != detailView {
		t.Error("tab should open detail view")
	}
}

func TestEnterDuringDispatch_ShowsWarning(t *testing.T) {
	m := modelWithTasks(testTasks())
	m.dispatching = true

	result, _ := m.Update(enterKeyMsg())
	updated := result.(Model)

	if updated.confirming != nil {
		t.Error("should not show confirmation while dispatching")
	}
	if updated.statusMsg != "dispatch already in progress" {
		t.Errorf("expected warning about in-progress dispatch, got %q", updated.statusMsg)
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
