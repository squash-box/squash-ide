package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// PlaceholderModel is the bubbletea model rendered in the right-hand tmux
// pane when no tasks are active. It's a static "nothing here yet" screen
// with a dashed plus-circle, a short call-to-action, and a row of slot
// boxes indicating how many task panes could fit alongside the TUI given
// the current window width.
//
// The pane is killed by the spawner as soon as the first task is spawned,
// so this model has no task-state awareness — it just renders and resizes.
type PlaceholderModel struct {
	width        int
	height       int
	tuiWidth     int
	minPaneWidth int
}

// NewPlaceholder constructs the placeholder model. tuiWidth + minPaneWidth
// come from the same config values the spawner uses, so the slot count
// matches what the spawner will actually accept.
func NewPlaceholder(tuiWidth, minPaneWidth int) PlaceholderModel {
	return PlaceholderModel{
		tuiWidth:     tuiWidth,
		minPaneWidth: minPaneWidth,
	}
}

func (m PlaceholderModel) Init() tea.Cmd { return nil }

func (m PlaceholderModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	case tea.KeyMsg:
		// Only ctrl-c exits — we want the placeholder to stay put otherwise
		// so the spawner can kill it deterministically when the first task
		// launches.
		if msg.Type == tea.KeyCtrlC {
			return m, tea.Quit
		}
	}
	return m, nil
}

// placeholder color palette — kept local so the placeholder renders the
// same regardless of future TUI theme changes.
var (
	phMuted  = lipgloss.Color("240")
	phDim    = lipgloss.Color("244")
	phAccent = lipgloss.Color("111")
)

// dashedBorder is a rounded border stroked with dashed glyphs — reads as a
// dotted circle around the central "+" icon.
var dashedBorder = lipgloss.Border{
	Top:         "┄",
	Bottom:      "┄",
	Left:        "┊",
	Right:       "┊",
	TopLeft:     "╭",
	TopRight:    "╮",
	BottomLeft:  "╰",
	BottomRight: "╯",
}

func (m PlaceholderModel) View() string {
	if m.width <= 0 || m.height <= 0 {
		return ""
	}

	// Dashed plus-circle: rounded-dashed border around a "+" glyph, with
	// horizontal padding to make the shape read as a circle rather than a
	// tall rectangle.
	circle := lipgloss.NewStyle().
		Border(dashedBorder).
		BorderForeground(phMuted).
		Foreground(phDim).
		Padding(1, 4).
		Bold(true).
		Render("+")

	// Primary line.
	title := lipgloss.NewStyle().
		Foreground(phDim).
		Render("No active tasks")

	// Call-to-action with an inline keycap. JoinHorizontal with Center
	// alignment keeps the surrounding text on the keycap's middle row.
	keycap := lipgloss.NewStyle().
		Border(lipgloss.NormalBorder()).
		BorderForeground(phAccent).
		Foreground(phAccent).
		Padding(0, 1).
		Render("enter")
	prefix := lipgloss.NewStyle().Foreground(phMuted).Render("Select a task and press ")
	suffix := lipgloss.NewStyle().Foreground(phMuted).Render(" to activate")
	cta := lipgloss.JoinHorizontal(lipgloss.Center, prefix, keycap, suffix)

	// Slot counter: "tmux slots available: ▢ ▢ ▢". Slot count is how many
	// additional panes could fit at min width alongside the pinned TUI:
	//   (width - tuiWidth) / (minPaneWidth + 1)
	// — matching the arithmetic in tmux.Tile.
	slots := m.slotCount()
	slotRow := renderSlotRow(slots)

	body := lipgloss.JoinVertical(
		lipgloss.Center,
		circle,
		"",
		title,
		cta,
		"",
		"",
		slotRow,
	)

	// Center inside the pane.
	return lipgloss.Place(
		m.width, m.height,
		lipgloss.Center, lipgloss.Center,
		body,
	)
}

// slotCount returns how many additional task panes could fit in the window
// alongside the TUI, given the configured minimum pane width. Clamps to
// [0, 8] to avoid drawing a wall of boxes on very wide terminals.
func (m PlaceholderModel) slotCount() int {
	if m.minPaneWidth <= 0 || m.tuiWidth <= 0 {
		return 0
	}
	// Assume the placeholder currently occupies the right-side space; we
	// report slot capacity as if every right-side column were free.
	// m.width is THIS pane's width, not the window width, so re-derive the
	// total: placeholder-width + tui-width + 1 border.
	total := m.width + m.tuiWidth + 1
	avail := total - m.tuiWidth
	if avail <= 0 {
		return 0
	}
	n := avail / (m.minPaneWidth + 1)
	if n < 0 {
		n = 0
	}
	if n > 8 {
		n = 8
	}
	return n
}

func renderSlotRow(n int) string {
	label := lipgloss.NewStyle().Foreground(phMuted).Render("tmux slots available: ")
	if n == 0 {
		return label + lipgloss.NewStyle().Foreground(phMuted).Render("(window too narrow)")
	}
	box := lipgloss.NewStyle().Foreground(phDim).Render("▢")
	return label + strings.Repeat(box+" ", n-1) + box
}

// RunPlaceholder runs the placeholder bubbletea program on the current
// terminal. It blocks until the program exits (via ctrl-c or tmux killing
// the pane). Callers should only invoke this from the `placeholder`
// subcommand entry point.
func RunPlaceholder(tuiWidth, minPaneWidth int) error {
	m := NewPlaceholder(tuiWidth, minPaneWidth)
	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		return fmt.Errorf("placeholder program: %w", err)
	}
	return nil
}
