package ui

import (
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/squashbox/squash-ide/internal/config"
	"github.com/squashbox/squash-ide/internal/dispatch"
	"github.com/squashbox/squash-ide/internal/spawner"
	"github.com/squashbox/squash-ide/internal/status"
	"github.com/squashbox/squash-ide/internal/task"
	"github.com/squashbox/squash-ide/internal/tmux"
	"github.com/squashbox/squash-ide/internal/vault"
)

// view represents which screen the user is on.
type view int

const (
	listView view = iota
	detailView
)

// activeIndicator is the glyph used to mark active tasks in the list
// and detail view.
const activeIndicator = "●"

// displayItem is a row in the list: a status header, a task card, or a
// dimmed placeholder shown when a section has no tasks (currently only
// the ACTIVE section, which we always want visible so the user has a
// clear "nothing running" cue and knows how to activate something).
type displayItem struct {
	isHeader      bool
	isPlaceholder bool
	header        string
	placeholder   string
	task          task.Task
}

// selectable reports whether the cursor can land on this item.
func (d displayItem) selectable() bool {
	return !d.isHeader && !d.isPlaceholder
}

// Model is the top-level Bubble Tea model.
type Model struct {
	cfg       config.Config
	vaultPath string // cfg.Vault — cached for rendering

	allTasks []task.Task   // all loaded tasks
	items    []displayItem // grouped display items (headers + tasks)
	filtered []displayItem // items after filter applied
	cursor   int           // index into filtered

	filter       string
	filterActive bool

	view     view
	viewport viewport.Model

	width  int
	height int

	// windowWidth caches the tmux window column count (total terminal width
	// inside tmux), refreshed at the same handler sites that call
	// checkCompactPane. isCompact keys on this — not m.width — because in
	// tmux m.width is the *pane* width, which gets pinned to CompactListWidth
	// once compact engages and would otherwise leave the predicate stuck.
	// Outside tmux this stays 0 and isCompact falls back to m.width.
	windowWidth int

	err error

	resetCursorOnLoad bool              // scroll to top after next tasksLoadedMsg
	tooNarrow         bool              // true when zoomed due to narrow terminal
	compact           bool              // true while pane is shrunk to CompactListWidth
	needsRespawn      bool              // true until the first task load triggers respawn
	RespawnFunc       func([]task.Task) // called once after first load to respawn active panes

	// MCP status polling
	subStatuses map[string]status.File // keyed by task ID

	// Dispatch / cleanup state
	confirming   *task.Task   // non-nil when spawn confirmation dialog is showing
	completing   *task.Task   // non-nil when complete confirmation dialog is showing
	deactivating *task.Task   // non-nil when deactivate confirmation dialog is showing
	blocking     *task.Task   // non-nil when block-reason input is active
	blockReason  string       // current text buffer for the block reason
	creatingTask *newTaskForm // non-nil when the new-task form is open
	dispatching  bool         // true while an async op is in progress
	statusMsg    string       // transient message for status bar
	statusIsErr  bool         // whether statusMsg is an error
}

// New creates a new Model from the resolved config.
func New(cfg config.Config) Model {
	return Model{
		cfg:          cfg,
		vaultPath:    cfg.Vault,
		needsRespawn: true,
	}
}

// NewForTest constructs a Model pre-loaded with tasks and status entries,
// ready to render without the async Init → loadTasks → tasksLoadedMsg
// round-trip. Intended for e2e tests that want to assert the rendered view
// directly; production callers should use New.
func NewForTest(cfg config.Config, tasks []task.Task, statuses map[string]status.File) Model {
	m := New(cfg)
	m.allTasks = tasks
	m.subStatuses = statuses
	m.width = 80
	m.height = 24
	m.needsRespawn = false
	m.buildItems()
	m.applyFilter()
	m.clampCursor()
	return m
}

// Init loads tasks from the vault and starts the status polling ticker.
func (m Model) Init() tea.Cmd {
	return tea.Batch(m.loadTasks, m.tickStatus())
}

type tasksLoadedMsg struct {
	tasks []task.Task
	err   error
}

type dispatchDoneMsg struct {
	taskID string
	branch string
}

type dispatchErrMsg struct {
	err error
}

type completeDoneMsg struct {
	taskID string
}

type blockDoneMsg struct {
	taskID string
}

type deactivateDoneMsg struct {
	taskID string
}

type statusTickMsg struct {
	statuses map[string]status.File
}

type logTaskDoneMsg struct{}

type logTaskErrMsg struct {
	err error
}

func (m Model) loadTasks() tea.Msg {
	tasks, err := vault.ReadAll(m.vaultPath)
	return tasksLoadedMsg{tasks: tasks, err: err}
}

func (m Model) tickStatus() tea.Cmd {
	return tea.Tick(1*time.Second, func(t time.Time) tea.Msg {
		statuses, _ := status.ReadAll()
		return statusTickMsg{statuses: statuses}
	})
}

func (m Model) runDispatch(t task.Task) tea.Cmd {
	cfg := m.cfg
	return func() tea.Msg {
		res, err := dispatch.Run(cfg, t)
		if err != nil {
			return dispatchErrMsg{err: err}
		}
		return dispatchDoneMsg{taskID: t.ID, branch: res.Branch}
	}
}

func (m Model) runComplete(t task.Task) tea.Cmd {
	cfg := m.cfg
	return func() tea.Msg {
		if err := dispatch.Complete(cfg, t); err != nil {
			return dispatchErrMsg{err: err}
		}
		return completeDoneMsg{taskID: t.ID}
	}
}

func (m Model) runBlock(t task.Task, reason string) tea.Cmd {
	cfg := m.cfg
	return func() tea.Msg {
		if err := dispatch.Block(cfg, t, reason); err != nil {
			return dispatchErrMsg{err: err}
		}
		return blockDoneMsg{taskID: t.ID}
	}
}

func (m Model) runDeactivate(t task.Task) tea.Cmd {
	cfg := m.cfg
	return func() tea.Msg {
		if err := dispatch.Deactivate(cfg, t); err != nil {
			return dispatchErrMsg{err: err}
		}
		return deactivateDoneMsg{taskID: t.ID}
	}
}

// runLogTask hands the form data off to the `/log-task` Claude skill, which
// owns task creation + codebase enrichment. The subprocess runs on the TUI
// pane's TTY via tea.ExecProcess, so the skill can ask clarifying questions
// interactively. On completion we reload the vault — the skill has written
// the new task file, board row, log entry, and entity-page updates itself.
//
// Hard-fails if `claude` isn't on PATH; the form deliberately doesn't fall
// back to a local writer so the single-source-of-truth contract with the
// skill is preserved.
func (m Model) runLogTask(f newTaskForm) tea.Cmd {
	if _, err := exec.LookPath("claude"); err != nil {
		return func() tea.Msg {
			return logTaskErrMsg{err: fmt.Errorf("claude CLI not found on PATH — install it to create tasks")}
		}
	}

	prompt := buildLogTaskPrompt(f)
	cmd := exec.Command("claude", prompt)
	// vault.ExpandHome resolves a leading `~` — the OS's chdir doesn't.
	cmd.Dir = vault.ExpandHome(m.vaultPath)

	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		if err != nil {
			return logTaskErrMsg{err: fmt.Errorf("/log-task: %w", err)}
		}
		return logTaskDoneMsg{}
	})
}

// buildLogTaskPrompt assembles the free-form $ARGUMENTS string that
// /log-task parses. Passed as a single argv entry, so no shell quoting is
// needed — newlines and punctuation are preserved verbatim.
func buildLogTaskPrompt(f newTaskForm) string {
	var b strings.Builder
	b.WriteString("/log-task ")
	b.WriteString(strings.TrimSpace(f.name))
	b.WriteString("\n\n")
	if p := strings.TrimSpace(f.repo); p != "" {
		b.WriteString("Project: ")
		b.WriteString(p)
		b.WriteString("\n")
	}
	b.WriteString("Type hint: ")
	b.WriteString(f.taskType())
	b.WriteString("\n")
	if body := strings.TrimSpace(f.prompt); body != "" {
		b.WriteString("\n")
		b.WriteString(body)
		b.WriteString("\n")
	}
	return b.String()
}

// Update handles messages.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.viewport = viewport.New(msg.Width, msg.Height-4)
		if m.view == detailView {
			m.updateDetailContent()
		}
		// Synchronous "too narrow" check — zoom/unzoom the TUI pane to
		// show a full-screen overlay when the terminal can't fit all panes.
		// Compact-mode check piggybacks on the same tmux call path.
		if tmux.InSession() {
			m.checkTooNarrow()
			m.checkCompactPane(tmux.CurrentPaneID())
		}
		return m, nil

	case tasksLoadedMsg:
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.allTasks = msg.tasks
		m.buildItems()
		m.applyFilter()
		if m.resetCursorOnLoad {
			m.cursor = 0
			m.resetCursorOnLoad = false
		}
		m.clampCursor()
		// Active count may have changed (dispatch / complete / deactivate /
		// block all route through loadTasks) — re-evaluate compact mode.
		if tmux.InSession() {
			m.checkCompactPane(tmux.CurrentPaneID())
		}
		// On the first load, respawn tmux panes for active tasks (or
		// create the placeholder). Done here rather than before the TUI
		// starts so the session is fully attached and sized.
		if m.needsRespawn && m.RespawnFunc != nil {
			m.needsRespawn = false
			m.RespawnFunc(m.allTasks)
		}
		return m, tea.ClearScreen

	case dispatchDoneMsg:
		m.dispatching = false
		m.statusMsg = fmt.Sprintf("spawned %s", msg.taskID)
		m.statusIsErr = false
		m.resetCursorOnLoad = true
		return m, m.loadTasks

	case completeDoneMsg:
		m.dispatching = false
		m.statusMsg = fmt.Sprintf("completed %s", msg.taskID)
		m.statusIsErr = false
		m.resetCursorOnLoad = true
		return m, m.loadTasks

	case blockDoneMsg:
		m.dispatching = false
		m.statusMsg = fmt.Sprintf("blocked %s", msg.taskID)
		m.statusIsErr = false
		return m, m.loadTasks

	case deactivateDoneMsg:
		m.dispatching = false
		m.statusMsg = fmt.Sprintf("deactivated %s → backlog", msg.taskID)
		m.statusIsErr = false
		m.resetCursorOnLoad = true
		return m, m.loadTasks

	case dispatchErrMsg:
		m.dispatching = false
		m.statusMsg = msg.err.Error()
		m.statusIsErr = true
		// Reload tasks — the dispatch may have partially succeeded (e.g.
		// task moved to active before the spawn failed), so the vault
		// state may have changed. This also lets the "too narrow" overlay
		// trigger with the correct active-task count.
		return m, m.loadTasks

	case logTaskDoneMsg:
		m.dispatching = false
		m.statusMsg = "/log-task finished"
		m.statusIsErr = false
		m.resetCursorOnLoad = true
		return m, m.loadTasks

	case logTaskErrMsg:
		m.dispatching = false
		m.creatingTask = nil
		m.statusMsg = msg.err.Error()
		m.statusIsErr = true
		return m, nil

	case statusTickMsg:
		old := m.subStatuses
		m.subStatuses = msg.statuses

		// Update tmux pane-border-format for any task whose state changed.
		//
		// "No entry" on this tick is treated as IDLE — matching activeBadge's
		// default for a nil sub — so the two consumers agree on what "no
		// live status report" means. Without the old-present / new-absent
		// branch, the pane border would silently retain its last-painted
		// format past the staleness horizon and diverge from the list badge.
		if tmux.InSession() {
			tuiPane := tmux.CurrentPaneID()
			for _, t := range m.allTasks {
				if t.Status != "active" {
					continue
				}
				newSub, newOK := msg.statuses[t.ID]
				oldSub, oldOK := old[t.ID]
				newState := "idle"
				if newOK {
					newState = newSub.State
				}
				oldState := "idle"
				if oldOK {
					oldState = oldSub.State
				}
				// Skip when we've never seen an entry for this task (both
				// old and new absent) — nothing has changed visually and we
				// don't want to paint "idle" on every tick forever.
				if !newOK && !oldOK {
					continue
				}
				if newState == oldState {
					continue
				}
				if pane, err := tmux.FindPaneByTask(tuiPane, t.ID); err == nil && pane != "" {
					_ = tmux.SetPaneBorderFormat(pane,
						spawner.TaskBorderFormatWithState(t.ID, t.Title, t.Project, newState))
				}
			}
		}
		return m, m.tickStatus()

	case tea.KeyMsg:
		newModel, cmd := m.handleKey(msg)
		// A keypress may have opened or closed a modal dialog, which
		// changes isCompact()'s truth value — re-check so the pane
		// expands back to normal while dialogs render and re-shrinks
		// once they close.
		if updated, ok := newModel.(Model); ok {
			if tmux.InSession() {
				updated.checkCompactPane(tmux.CurrentPaneID())
			}
			return updated, cmd
		}
		return newModel, cmd
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// New-task form — takes precedence so keys (including the letters used
	// elsewhere for list actions) don't leak into the list while the form
	// is open.
	if m.creatingTask != nil {
		return m.handleNewTaskKey(msg)
	}

	// Block reason input
	if m.blocking != nil {
		return m.handleBlockInputKey(msg)
	}

	// Complete confirmation dialog
	if m.completing != nil {
		return m.handleCompleteConfirmKey(msg)
	}

	// Deactivate confirmation dialog
	if m.deactivating != nil {
		return m.handleDeactivateConfirmKey(msg)
	}

	// Spawn confirmation dialog
	if m.confirming != nil {
		return m.handleConfirmKey(msg)
	}

	// Filter input mode
	if m.filterActive {
		return m.handleFilterKey(msg)
	}

	// Detail view
	if m.view == detailView {
		return m.handleDetailKey(msg)
	}

	// List view
	return m.handleListKey(msg)
}

func (m Model) handleConfirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, keys.Confirm), key.Matches(msg, keys.Enter):
		t := *m.confirming
		m.confirming = nil
		m.dispatching = true
		m.statusMsg = fmt.Sprintf("spawning %s...", t.ID)
		m.statusIsErr = false
		return m, m.runDispatch(t)
	case key.Matches(msg, keys.Deny), key.Matches(msg, keys.Back):
		m.confirming = nil
		return m, nil
	}
	return m, nil
}

func (m Model) handleCompleteConfirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, keys.Confirm), key.Matches(msg, keys.Enter):
		t := *m.completing
		m.completing = nil
		m.dispatching = true
		m.statusMsg = fmt.Sprintf("completing %s...", t.ID)
		m.statusIsErr = false
		return m, m.runComplete(t)
	case key.Matches(msg, keys.Deny), key.Matches(msg, keys.Back):
		m.completing = nil
		return m, nil
	}
	return m, nil
}

func (m Model) handleDeactivateConfirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, keys.Confirm), key.Matches(msg, keys.Enter):
		t := *m.deactivating
		m.deactivating = nil
		m.dispatching = true
		m.statusMsg = fmt.Sprintf("deactivating %s...", t.ID)
		m.statusIsErr = false
		return m, m.runDeactivate(t)
	case key.Matches(msg, keys.Deny), key.Matches(msg, keys.Back):
		m.deactivating = nil
		return m, nil
	}
	return m, nil
}

func (m Model) handleNewTaskKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	form, submitted, cancelled := m.creatingTask.handleKey(msg)
	if cancelled {
		m.creatingTask = nil
		return m, nil
	}
	if submitted {
		if m.dispatching {
			m.statusMsg = "another operation is in progress"
			m.statusIsErr = true
			return m, nil
		}
		// Hand off to /log-task. We clear the form state before running so
		// the TTY handover is clean — tea.ExecProcess tears down the alt
		// screen for the duration, and we want the list to be what renders
		// underneath if anything flickers.
		m.creatingTask = nil
		m.dispatching = true
		m.statusMsg = "running /log-task..."
		m.statusIsErr = false
		return m, m.runLogTask(form)
	}
	m.creatingTask = &form
	return m, nil
}

func (m Model) handleBlockInputKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, keys.Back):
		m.blocking = nil
		m.blockReason = ""
		return m, nil
	case msg.Type == tea.KeyEnter:
		if strings.TrimSpace(m.blockReason) == "" {
			m.statusMsg = "block reason cannot be empty"
			m.statusIsErr = true
			return m, nil
		}
		t := *m.blocking
		reason := m.blockReason
		m.blocking = nil
		m.blockReason = ""
		m.dispatching = true
		m.statusMsg = fmt.Sprintf("blocking %s...", t.ID)
		m.statusIsErr = false
		return m, m.runBlock(t, reason)
	case msg.Type == tea.KeyBackspace:
		if len(m.blockReason) > 0 {
			m.blockReason = m.blockReason[:len(m.blockReason)-1]
		}
		return m, nil
	case msg.Type == tea.KeyRunes:
		m.blockReason += string(msg.Runes)
		return m, nil
	case msg.Type == tea.KeySpace:
		m.blockReason += " "
		return m, nil
	}
	return m, nil
}

func (m Model) handleFilterKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, keys.Back):
		m.filterActive = false
		m.filter = ""
		m.applyFilter()
		m.clampCursor()
	case msg.Type == tea.KeyBackspace:
		if len(m.filter) > 0 {
			m.filter = m.filter[:len(m.filter)-1]
			m.applyFilter()
			m.clampCursor()
		}
	case msg.Type == tea.KeyEnter:
		m.filterActive = false
	case msg.Type == tea.KeyRunes:
		m.filter += string(msg.Runes)
		m.applyFilter()
		m.clampCursor()
	}
	return m, nil
}

func (m Model) handleDetailKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, keys.Back), key.Matches(msg, keys.Enter):
		m.view = listView
		return m, nil
	case key.Matches(msg, keys.Quit):
		return m, tea.Quit
	default:
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return m, cmd
	}
}

func (m Model) handleListKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Clear status message on any keypress
	m.statusMsg = ""

	switch {
	case key.Matches(msg, keys.Quit):
		return m, tea.Quit

	case key.Matches(msg, keys.Up):
		m.moveCursor(-1)

	case key.Matches(msg, keys.Down):
		m.moveCursor(1)

	case key.Matches(msg, keys.Enter):
		if item := m.selectedItem(); item != nil && !item.isHeader {
			if item.task.Status == "backlog" {
				if m.dispatching {
					m.statusMsg = "dispatch already in progress"
					m.statusIsErr = true
					return m, nil
				}
				t := item.task
				m.confirming = &t
			} else {
				m.statusMsg = fmt.Sprintf("%s is already %s", item.task.ID, item.task.Status)
				m.statusIsErr = false
			}
		}

	case key.Matches(msg, keys.Complete):
		if item := m.selectedItem(); item != nil && !item.isHeader {
			if item.task.Status != "active" {
				m.statusMsg = fmt.Sprintf("%s is %s — only active tasks can be completed",
					item.task.ID, item.task.Status)
				m.statusIsErr = true
				return m, nil
			}
			if m.dispatching {
				m.statusMsg = "another operation is in progress"
				m.statusIsErr = true
				return m, nil
			}
			t := item.task
			m.completing = &t
		}

	case key.Matches(msg, keys.Block):
		if item := m.selectedItem(); item != nil && !item.isHeader {
			if item.task.Status != "active" {
				m.statusMsg = fmt.Sprintf("%s is %s — only active tasks can be blocked",
					item.task.ID, item.task.Status)
				m.statusIsErr = true
				return m, nil
			}
			if m.dispatching {
				m.statusMsg = "another operation is in progress"
				m.statusIsErr = true
				return m, nil
			}
			t := item.task
			m.blocking = &t
			m.blockReason = ""
		}

	case key.Matches(msg, keys.Deactivate):
		if item := m.selectedItem(); item != nil && !item.isHeader {
			if item.task.Status != "active" {
				m.statusMsg = fmt.Sprintf("%s is %s — only active tasks can be deactivated",
					item.task.ID, item.task.Status)
				m.statusIsErr = true
				return m, nil
			}
			if m.dispatching {
				m.statusMsg = "another operation is in progress"
				m.statusIsErr = true
				return m, nil
			}
			t := item.task
			m.deactivating = &t
		}

	case key.Matches(msg, keys.Detail):
		if item := m.selectedItem(); item != nil && !item.isHeader {
			m.view = detailView
			m.updateDetailContent()
		}

	case key.Matches(msg, keys.Filter):
		m.filterActive = true
		m.filter = ""

	case key.Matches(msg, keys.Refresh):
		return m, m.loadTasks

	case key.Matches(msg, keys.NewTask):
		if m.dispatching {
			m.statusMsg = "another operation is in progress"
			m.statusIsErr = true
			return m, nil
		}
		f := newNewTaskForm()
		m.creatingTask = &f
		return m, nil
	}
	return m, nil
}

// moveCursor moves the cursor by delta, skipping non-selectable rows
// (section headers and empty-section placeholders).
func (m *Model) moveCursor(delta int) {
	if len(m.filtered) == 0 {
		return
	}
	next := m.cursor + delta
	for next >= 0 && next < len(m.filtered) && !m.filtered[next].selectable() {
		next += delta
	}
	if next >= 0 && next < len(m.filtered) {
		m.cursor = next
	}
}

func (m *Model) clampCursor() {
	if len(m.filtered) == 0 {
		m.cursor = 0
		return
	}
	if m.cursor >= len(m.filtered) {
		m.cursor = len(m.filtered) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	// If cursor is on a header or placeholder, walk forward to the first
	// selectable row; if none exists ahead, walk backward.
	if !m.filtered[m.cursor].selectable() {
		m.moveCursor(1)
		if !m.filtered[m.cursor].selectable() {
			m.moveCursor(-1)
		}
	}
}

func (m *Model) selectedItem() *displayItem {
	if m.cursor >= 0 && m.cursor < len(m.filtered) {
		return &m.filtered[m.cursor]
	}
	return nil
}

// buildItems groups tasks by status into display items with headers.
// The order — active, backlog, blocked — surfaces the work in flight
// first, matching the card-layout mockup.
//
// The ACTIVE section is always emitted, even when empty, with a dimmed
// placeholder item so launching tasks feels like a first-class affordance
// on an empty board. Other sections remain hidden when they have no
// content, to keep the board tight.
func (m *Model) buildItems() {
	m.items = nil
	if len(m.allTasks) == 0 {
		// The empty-vault path in View() renders its own message; don't
		// emit the ACTIVE placeholder as if tasks were loaded.
		return
	}
	statusOrder := []string{"active", "backlog", "blocked"}
	grouped := map[string][]task.Task{}
	for _, t := range m.allTasks {
		grouped[t.Status] = append(grouped[t.Status], t)
	}
	for _, status := range statusOrder {
		tasks := grouped[status]
		if len(tasks) == 0 && status != "active" {
			continue
		}
		m.items = append(m.items, displayItem{isHeader: true, header: status})
		if len(tasks) == 0 {
			m.items = append(m.items, displayItem{
				isPlaceholder: true,
				placeholder:   "No active tasks — select a task and press enter to launch",
			})
			continue
		}
		for _, t := range tasks {
			m.items = append(m.items, displayItem{task: t})
		}
	}
}

// applyFilter filters items by the current filter string.
func (m *Model) applyFilter() {
	if m.filter == "" {
		m.filtered = m.items
		return
	}
	query := strings.ToLower(m.filter)
	m.filtered = nil
	var lastHeader *displayItem
	for i := range m.items {
		item := m.items[i]
		if item.isHeader {
			lastHeader = &m.items[i]
			continue
		}
		// Placeholders never match a filter — they're an empty-section
		// hint, not searchable content.
		if item.isPlaceholder {
			continue
		}
		match := strings.Contains(strings.ToLower(item.task.ID), query) ||
			strings.Contains(strings.ToLower(item.task.Title), query)
		if match {
			if lastHeader != nil {
				m.filtered = append(m.filtered, *lastHeader)
				lastHeader = nil
			}
			m.filtered = append(m.filtered, item)
		}
	}
}

func (m *Model) updateDetailContent() {
	item := m.selectedItem()
	if item == nil || !item.selectable() {
		return
	}
	t := item.task

	title := fmt.Sprintf("%s — %s", t.ID, t.Title)
	if t.Status == "active" {
		title = activeIndicatorStyle.Render(activeIndicator) + " " + title
	}
	header := detailTitleStyle.Render(title)

	meta := fmt.Sprintf("  Type: %s  Project: %s  Status: %s  Priority: %s",
		t.Type, t.Project, t.Status, t.Priority)

	var extra string
	if t.Status == "active" {
		if wt, err := dispatch.WorktreePathFor(m.cfg, t); err == nil {
			extra = "\n" + worktreeStyle.Render("Worktree: "+wt)
		}
	}

	body := detailBodyStyle.Render(t.Body)
	content := header + "\n" + meta + extra + "\n\n" + body

	m.viewport.SetContent(content)
	m.viewport.GotoTop()
}

// View renders the UI.
// checkTooNarrow queries the tmux window width and zooms/unzooms the TUI
// pane to show a "too narrow" overlay. Called synchronously from the
// WindowSizeMsg handler (~5ms tmux roundtrip, no async race).
func (m *Model) checkTooNarrow() {
	pane := tmux.CurrentPaneID()
	if pane == "" {
		return
	}
	ww, err := tmux.WindowWidth(pane)
	if err != nil || ww == 0 {
		return
	}
	activeCount := 0
	for _, t := range m.allTasks {
		if t.Status == "active" {
			activeCount++
		}
	}
	if activeCount == 0 {
		if m.tooNarrow {
			m.tooNarrow = false
			tmux.ToggleZoom(pane)
		}
		return
	}
	needed := m.cfg.Tmux.TUIWidth + activeCount*(m.cfg.Tmux.PaneWidth+1)
	if ww < needed && !m.tooNarrow {
		m.tooNarrow = true
		tmux.ToggleZoom(pane)
	} else if ww >= needed && m.tooNarrow {
		m.tooNarrow = false
		tmux.ToggleZoom(pane)
	}
}

func (m Model) View() string {
	if m.err != nil {
		return fmt.Sprintf("\n  Error: %v\n\n  Press q to quit.\n", m.err)
	}

	if m.tooNarrow {
		activeCount := 0
		for _, t := range m.allTasks {
			if t.Status == "active" {
				activeCount++
			}
		}
		needed := m.cfg.Tmux.TUIWidth + activeCount*(m.cfg.Tmux.PaneWidth+1)
		msg := fmt.Sprintf(
			"Terminal too narrow\n\nNeeded: %d cols\n\nWiden the terminal or\ndeactivate a task with d",
			needed,
		)
		styled := lipgloss.NewStyle().
			Foreground(lipgloss.Color("204")).
			Bold(true).
			Align(lipgloss.Center).
			Render(msg)
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, styled)
	}

	if m.view == detailView {
		return m.detailViewRender()
	}
	return m.listViewRender()
}

func (m Model) listViewRender() string {
	// The task list is normally rendered at cfg.Tmux.TUIWidth (default 60).
	// In compact mode — narrow terminal + 2+ active spawns — it collapses
	// to CompactListWidth to free horizontal space for the tiled panes.
	compact := m.isCompact()
	width := m.width
	maxWidth := m.cfg.Tmux.TUIWidth
	if maxWidth <= 0 {
		maxWidth = 60
	}
	if compact {
		width = CompactListWidth
	} else {
		if width > maxWidth {
			width = maxWidth
		}
		if width < 40 {
			width = 40
		}
	}

	var b strings.Builder

	// Top bar: app name + per-status counts. Compact variant uses a
	// single-char stub + two-char counts to fit CompactListWidth.
	counts := m.statusCounts()
	if compact {
		b.WriteString(renderTopBarCompact(width, counts))
	} else {
		b.WriteString(renderTopBar(width, "squash-ide", "", counts))
	}
	b.WriteString("\n")

	// New-task form takes over the whole pane — there's usually a lot of
	// information to enter (particularly the prompt), so the bottom-overlay
	// pattern used for simple y/N dialogs isn't enough.
	if m.creatingTask != nil {
		formHeight := m.height - 2 // leave room for the status bar + help
		if formHeight < 16 {
			formHeight = 16
		}
		b.Reset()
		b.WriteString(m.creatingTask.view(width, formHeight))
		b.WriteString("\n")
		b.WriteString(m.renderStatusBar())
		b.WriteString("\n")
		b.WriteString(helpStyle.Render("[tab] field  [←/→] type  [enter] submit  [ctrl+d] submit from prompt  [esc] cancel"))
		return b.String()
	}

	if len(m.allTasks) == 0 {
		b.WriteString(emptyStyle.Render("No tasks found in vault."))
		b.WriteString("\n")
		b.WriteString(emptyStyle.Render(fmt.Sprintf("Vault: %s", m.vaultPath)))
		b.WriteString("\n\n")
		b.WriteString(helpStyle.Render("[r] refresh  [q] quit"))
		b.WriteString("\n")
		return b.String()
	}

	// Reserve rows: top bar + divider + (filter row?) + status bar + help.
	chrome := 3 // top bar + status bar + help line
	if m.filterActive || m.filter != "" {
		chrome++
	}
	if m.confirming != nil || m.completing != nil || m.deactivating != nil || m.blocking != nil {
		chrome += 3 // dialog box height (border + content + border)
	}
	listHeight := m.height - chrome
	if listHeight < 5 {
		listHeight = 5
	}

	b.WriteString(m.renderCardList(width, listHeight, compact))

	// Dialog overlays.
	if m.confirming != nil {
		b.WriteString(confirmBoxStyle.Render(
			fmt.Sprintf("Spawn %s? [y/N]", m.confirming.ID)))
		b.WriteString("\n")
	} else if m.completing != nil {
		b.WriteString(confirmBoxStyle.Render(
			fmt.Sprintf("Complete %s? [y/N]", m.completing.ID)))
		b.WriteString("\n")
	} else if m.deactivating != nil {
		b.WriteString(confirmBoxStyle.Render(
			fmt.Sprintf("Deactivate %s → backlog? [y/N]", m.deactivating.ID)))
		b.WriteString("\n")
	} else if m.blocking != nil {
		prompt := fmt.Sprintf("Block %s — reason: %s█", m.blocking.ID, m.blockReason)
		b.WriteString(inputBoxStyle.Render(prompt))
		b.WriteString("\n")
	}

	// Filter bar
	if m.filterActive {
		b.WriteString(filterPromptStyle.Render("/") + filterInputStyle.Render(m.filter+"█"))
		b.WriteString("\n")
	} else if m.filter != "" {
		b.WriteString(filterPromptStyle.Render(fmt.Sprintf("filter: %s", m.filter)) + "  ")
		b.WriteString(helpStyle.Render("[/] edit  [esc] clear"))
		b.WriteString("\n")
	}

	// Status bar (transient messages only — counts moved to top bar).
	b.WriteString(m.renderStatusBar())
	b.WriteString("\n")

	// Help
	switch {
	case m.confirming != nil, m.completing != nil, m.deactivating != nil:
		b.WriteString(helpStyle.Render("[y/enter] confirm  [n/esc] cancel"))
	case m.blocking != nil:
		b.WriteString(helpStyle.Render("[enter] submit  [esc] cancel  [type] reason"))
	case compact:
		// Compact stands down in dialog/blocking states (see isCompact) so
		// the only reachable cases here are default and filter-active.
		b.WriteString(helpLineCompact(m.filterActive, m.filter != ""))
	case m.filterActive:
		b.WriteString(helpStyle.Render("[enter] apply  [esc] clear  [type] filter"))
	default:
		b.WriteString(helpStyle.Render("j/k nav  enter spawn  t new  c complete  d deactivate  b block  tab detail  / filter  r refresh  q quit"))
	}
	b.WriteString("\n")

	return b.String()
}

// renderCardList renders the per-section card list, scrolling to keep the
// cursor visible. Cards have variable height (active = 3 lines, backlog = 2;
// in compact mode: active = 2, backlog = 1) so we render to a flat line
// buffer first, find the cursor card's line range, then slice.
func (m Model) renderCardList(width, height int, compact bool) string {
	var (
		lines       []string
		cursorStart = -1
		cursorEnd   = -1
	)

	for i, item := range m.filtered {
		if item.isHeader {
			// Section header gets a leading blank for breathing room
			// (skipped at the very top of the list).
			if len(lines) > 0 {
				lines = append(lines, "")
			}
			lines = append(lines, renderSectionHeader(item.header))
			lines = append(lines, "")
			continue
		}

		if item.isPlaceholder {
			lines = append(lines, renderPlaceholder(item.placeholder))
			lines = append(lines, "")
			continue
		}

		selected := i == m.cursor
		var sub *status.File
		if s, ok := m.subStatuses[item.task.ID]; ok {
			sub = &s
		}
		card := renderCard(item.task, selected, width, sub, compact)

		if selected {
			cursorStart = len(lines)
			cursorEnd = len(lines) + len(card) - 1
		}
		lines = append(lines, card...)
		// Spacer between cards.
		lines = append(lines, "")
	}

	// Trim trailing blank.
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	// Scroll: keep cursor card fully visible.
	start := 0
	if cursorEnd >= 0 && len(lines) > height {
		switch {
		case cursorEnd-cursorStart+1 > height:
			start = cursorStart
		case cursorEnd >= height:
			start = cursorEnd - height + 1
		}
		if start < 0 {
			start = 0
		}
		if start > len(lines)-height {
			start = len(lines) - height
		}
	}
	end := start + height
	if end > len(lines) {
		end = len(lines)
	}

	// Pad to exactly `height` lines so the card list always fills its
	// allocated space, pinning the footer (status bar, help) to the bottom.
	result := make([]string, height)
	copy(result, lines[start:end])
	return strings.Join(result, "\n") + "\n"
}

// statusCounts returns a {status: count} map across all loaded tasks.
func (m Model) statusCounts() map[string]int {
	counts := map[string]int{}
	for _, t := range m.allTasks {
		counts[t.Status]++
	}
	return counts
}

func (m Model) detailViewRender() string {
	header := helpStyle.Render("[enter/esc] back  [↑↓] scroll  [q] quit")
	return m.viewport.View() + "\n" + header + "\n"
}

// renderStatusBar shows transient feedback (success / error / dispatching)
// or a quiet vault hint when idle. Per-status counts now live in the top
// bar, so the bottom bar stays free for the message of the moment.
func (m Model) renderStatusBar() string {
	if m.statusMsg != "" {
		if m.dispatching {
			return dispatchingStyle.Render(m.statusMsg)
		}
		if m.statusIsErr {
			return statusErrorStyle.Render(m.statusMsg)
		}
		return statusSuccessStyle.Render(m.statusMsg)
	}
	return statusBarStyle.Render(fmt.Sprintf("Vault: %s", m.vaultPath))
}
