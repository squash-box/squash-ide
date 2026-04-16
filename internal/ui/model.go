package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/squashbox/squash-ide/internal/config"
	"github.com/squashbox/squash-ide/internal/dispatch"
	"github.com/squashbox/squash-ide/internal/task"
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

	err error

	// Dispatch / cleanup state
	confirming   *task.Task // non-nil when spawn confirmation dialog is showing
	completing   *task.Task // non-nil when complete confirmation dialog is showing
	deactivating *task.Task // non-nil when deactivate confirmation dialog is showing
	blocking     *task.Task // non-nil when block-reason input is active
	blockReason  string     // current text buffer for the block reason
	dispatching  bool       // true while an async op is in progress
	statusMsg   string     // transient message for status bar
	statusIsErr bool       // whether statusMsg is an error
}

// New creates a new Model from the resolved config.
func New(cfg config.Config) Model {
	return Model{
		cfg:       cfg,
		vaultPath: cfg.Vault,
	}
}

// Init loads tasks from the vault.
func (m Model) Init() tea.Cmd {
	return m.loadTasks
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

func (m Model) loadTasks() tea.Msg {
	tasks, err := vault.ReadAll(m.vaultPath)
	return tasksLoadedMsg{tasks: tasks, err: err}
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
		return m, nil

	case tasksLoadedMsg:
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.allTasks = msg.tasks
		m.buildItems()
		m.applyFilter()
		m.clampCursor()
		return m, nil

	case dispatchDoneMsg:
		m.dispatching = false
		m.statusMsg = fmt.Sprintf("spawned %s", msg.taskID)
		m.statusIsErr = false
		return m, m.loadTasks

	case completeDoneMsg:
		m.dispatching = false
		m.statusMsg = fmt.Sprintf("completed %s", msg.taskID)
		m.statusIsErr = false
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
		return m, m.loadTasks

	case dispatchErrMsg:
		m.dispatching = false
		m.statusMsg = msg.err.Error()
		m.statusIsErr = true
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
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
func (m Model) View() string {
	if m.err != nil {
		return fmt.Sprintf("\n  Error: %v\n\n  Press q to quit.\n", m.err)
	}

	if m.view == detailView {
		return m.detailViewRender()
	}
	return m.listViewRender()
}

func (m Model) listViewRender() string {
	// The task list is always rendered at a fixed width (cfg.Tmux.TUIWidth,
	// default 60). In tmux mode the pane is pinned to that width anyway, but
	// clamping here also keeps the layout consistent in no-tmux mode and
	// during the brief window before tmux finishes reshaping.
	width := m.width
	maxWidth := m.cfg.Tmux.TUIWidth
	if maxWidth <= 0 {
		maxWidth = 60
	}
	if width > maxWidth {
		width = maxWidth
	}
	if width < 40 {
		width = 40
	}

	var b strings.Builder

	// Top bar: app name + per-status counts.
	counts := m.statusCounts()
	b.WriteString(renderTopBar(width, "squash-ide", "", counts))
	b.WriteString("\n")
	b.WriteString(renderDivider(width))
	b.WriteString("\n")

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
	chrome := 4
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

	b.WriteString(m.renderCardList(width, listHeight))

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
	case m.filterActive:
		b.WriteString(helpStyle.Render("[enter] apply  [esc] clear  [type] filter"))
	default:
		b.WriteString(helpStyle.Render("j/k nav  enter spawn  c complete  d deactivate  b block  tab detail  / filter  r refresh  q quit"))
	}
	b.WriteString("\n")

	return b.String()
}

// renderCardList renders the per-section card list, scrolling to keep the
// cursor visible. Cards have variable height (active = 3 lines, backlog = 2)
// so we render to a flat line buffer first, find the cursor card's line
// range, then slice.
func (m Model) renderCardList(width, height int) string {
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
		card := renderCard(item.task, selected, width)

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

	return strings.Join(lines[start:end], "\n") + "\n"
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
