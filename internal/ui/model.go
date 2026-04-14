package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
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

// displayItem is either a status header or a task row in the list.
type displayItem struct {
	isHeader bool
	header   string
	task     task.Task
}

// Model is the top-level Bubble Tea model.
type Model struct {
	vaultPath string

	allTasks []task.Task    // all loaded tasks
	items    []displayItem  // grouped display items (headers + tasks)
	filtered []displayItem  // items after filter applied
	cursor   int            // index into filtered

	filter       string
	filterActive bool

	view     view
	viewport viewport.Model

	width  int
	height int

	err error

	// Dispatch state
	confirming  *task.Task // non-nil when confirmation dialog is showing
	dispatching bool       // true while dispatch is in progress
	statusMsg   string     // transient message for status bar
	statusIsErr bool       // whether statusMsg is an error
}

// New creates a new Model for the given vault path.
func New(vaultPath string) Model {
	return Model{
		vaultPath: vaultPath,
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

func (m Model) loadTasks() tea.Msg {
	tasks, err := vault.ReadAll(m.vaultPath)
	return tasksLoadedMsg{tasks: tasks, err: err}
}

func (m Model) runDispatch(t task.Task) tea.Cmd {
	vaultPath := m.vaultPath
	return func() tea.Msg {
		res, err := dispatch.Run(vaultPath, t)
		if err != nil {
			return dispatchErrMsg{err: err}
		}
		return dispatchDoneMsg{taskID: t.ID, branch: res.Branch}
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
	// Confirmation dialog
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

// moveCursor moves the cursor by delta, skipping headers.
func (m *Model) moveCursor(delta int) {
	if len(m.filtered) == 0 {
		return
	}
	next := m.cursor + delta
	// Skip headers
	for next >= 0 && next < len(m.filtered) && m.filtered[next].isHeader {
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
	// If cursor is on a header, move to next task
	if m.filtered[m.cursor].isHeader {
		m.moveCursor(1)
	}
}

func (m *Model) selectedItem() *displayItem {
	if m.cursor >= 0 && m.cursor < len(m.filtered) {
		return &m.filtered[m.cursor]
	}
	return nil
}

// buildItems groups tasks by status into display items with headers.
func (m *Model) buildItems() {
	m.items = nil
	statusOrder := []string{"backlog", "active", "blocked"}
	grouped := map[string][]task.Task{}
	for _, t := range m.allTasks {
		grouped[t.Status] = append(grouped[t.Status], t)
	}
	for _, status := range statusOrder {
		tasks := grouped[status]
		if len(tasks) == 0 {
			continue
		}
		m.items = append(m.items, displayItem{isHeader: true, header: status})
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
	if item == nil || item.isHeader {
		return
	}
	t := item.task
	header := detailTitleStyle.Render(fmt.Sprintf("%s — %s", t.ID, t.Title))
	meta := fmt.Sprintf("  Type: %s  Project: %s  Status: %s  Priority: %s",
		t.Type, t.Project, t.Status, t.Priority)
	body := detailBodyStyle.Render(t.Body)
	content := header + "\n" + meta + "\n\n" + body

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
	var b strings.Builder

	// Title
	b.WriteString(titleStyle.Render("squash-ide"))
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

	// List items
	listHeight := m.height - 4 // reserve for title, status bar, help, filter
	if listHeight < 1 {
		listHeight = 10
	}

	// Calculate scroll offset to keep cursor visible
	start := 0
	if m.cursor >= listHeight {
		start = m.cursor - listHeight + 1
	}

	rendered := 0
	for i := start; i < len(m.filtered) && rendered < listHeight; i++ {
		item := m.filtered[i]
		if item.isHeader {
			b.WriteString(statusHeaderStyle.Render(fmt.Sprintf("─── %s ───", item.header)))
			b.WriteString("\n")
		} else {
			line := formatTaskLine(item.task)
			if i == m.cursor {
				b.WriteString(selectedStyle.Render("► " + line))
			} else {
				b.WriteString(normalStyle.Render("  " + line))
			}
			b.WriteString("\n")
		}
		rendered++
	}

	// Confirmation dialog overlay
	if m.confirming != nil {
		b.WriteString(confirmBoxStyle.Render(
			fmt.Sprintf("Spawn %s? [y/N]", m.confirming.ID)))
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

	// Status bar
	b.WriteString(m.renderStatusBar())
	b.WriteString("\n")

	// Help
	if m.confirming != nil {
		b.WriteString(helpStyle.Render("[y/enter] confirm  [n/esc] cancel"))
	} else if m.filterActive {
		b.WriteString(helpStyle.Render("[enter] apply  [esc] clear  [type] filter"))
	} else {
		b.WriteString(helpStyle.Render("[↑↓/jk] navigate  [enter] spawn  [tab] detail  [/] filter  [r] refresh  [q] quit"))
	}
	b.WriteString("\n")

	return b.String()
}

func (m Model) detailViewRender() string {
	header := helpStyle.Render("[enter/esc] back  [↑↓] scroll  [q] quit")
	return m.viewport.View() + "\n" + header + "\n"
}

func (m Model) renderStatusBar() string {
	// Show transient status message if present
	if m.statusMsg != "" {
		if m.dispatching {
			return dispatchingStyle.Render(m.statusMsg)
		}
		if m.statusIsErr {
			return statusErrorStyle.Render(m.statusMsg)
		}
		return statusSuccessStyle.Render(m.statusMsg)
	}

	counts := map[string]int{}
	for _, t := range m.allTasks {
		counts[t.Status]++
	}
	parts := []string{fmt.Sprintf("Vault: %s", m.vaultPath)}
	for _, s := range []string{"backlog", "active", "blocked"} {
		if c := counts[s]; c > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", c, s))
		}
	}
	return statusBarStyle.Render(strings.Join(parts, " • "))
}

func formatTaskLine(t task.Task) string {
	id := t.ID
	typ := typeStyle.Render(fmt.Sprintf("[%s]", t.Type))
	title := t.Title
	proj := projectStyle.Render(t.Project)
	return lipgloss.JoinHorizontal(lipgloss.Top, id, " ", typ, " ", title, " ", proj)
}
