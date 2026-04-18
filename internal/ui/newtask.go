package ui

import (
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// newTaskField identifies which control inside the form has focus.
type newTaskField int

const (
	fieldName newTaskField = iota
	fieldRepo
	fieldType
	fieldPrompt
)

// taskTypes is the ordered set shown in the type radio. Matches the mockup.
var taskTypes = []string{"bug", "feature", "ops", "sec"}

// newTaskForm is the modal form state for creating a backlog task.
// It owns text editing and field navigation so the parent Model only has to
// observe submitted/cancelled transitions.
type newTaskForm struct {
	name    string
	repo    string
	typeIdx int
	prompt  string

	focus newTaskField
}

func newNewTaskForm() newTaskForm {
	return newTaskForm{
		typeIdx: 1, // "feature" — matches mockup default
		focus:   fieldName,
	}
}

// taskType returns the currently selected type string.
func (f newTaskForm) taskType() string {
	return taskTypes[f.typeIdx]
}

// trimmed reports whether required fields (name, repo) are populated.
func (f newTaskForm) valid() bool {
	return strings.TrimSpace(f.name) != "" && strings.TrimSpace(f.repo) != ""
}

// handleKey applies a keypress to the form and returns the new form state
// plus submit/cancel signals. The caller clears m.creatingTask on cancel and
// dispatches a create command on submit.
func (f newTaskForm) handleKey(msg tea.KeyMsg) (newTaskForm, bool, bool) {
	if key.Matches(msg, keys.Back) {
		return f, false, true
	}

	// Tab cycles forward, shift+tab backward. Also allow down/up on non-text
	// controls so arrow navigation feels natural on the radio.
	if msg.Type == tea.KeyTab {
		f.focus = (f.focus + 1) % 4
		return f, false, false
	}
	if msg.Type == tea.KeyShiftTab {
		f.focus = (f.focus + 3) % 4
		return f, false, false
	}

	switch f.focus {
	case fieldName:
		f.name = applyTextEdit(f.name, msg)
		if msg.Type == tea.KeyEnter && f.valid() {
			return f, true, false
		}
	case fieldRepo:
		f.repo = applyTextEdit(f.repo, msg)
		if msg.Type == tea.KeyEnter && f.valid() {
			return f, true, false
		}
	case fieldType:
		switch msg.Type {
		case tea.KeyLeft:
			if f.typeIdx > 0 {
				f.typeIdx--
			}
		case tea.KeyRight:
			if f.typeIdx < len(taskTypes)-1 {
				f.typeIdx++
			}
		case tea.KeyEnter:
			if f.valid() {
				return f, true, false
			}
		}
	case fieldPrompt:
		// Enter inserts a newline; ctrl+d (or ctrl+s) submits so the prompt
		// can be multi-line without fighting for the enter key.
		if msg.Type == tea.KeyCtrlD || msg.Type == tea.KeyCtrlS {
			if f.valid() {
				return f, true, false
			}
			return f, false, false
		}
		if msg.Type == tea.KeyEnter {
			f.prompt += "\n"
			return f, false, false
		}
		f.prompt = applyTextEdit(f.prompt, msg)
	}

	return f, false, false
}

// applyTextEdit handles the narrow subset of key events we support in the
// text fields: printable runes append, backspace deletes the last rune.
// Cursor movement/line editing is intentionally omitted — this is a short
// form, not a full editor.
func applyTextEdit(s string, msg tea.KeyMsg) string {
	switch msg.Type {
	case tea.KeyBackspace:
		if len(s) == 0 {
			return s
		}
		// Trim one rune (handles multi-byte safely).
		r := []rune(s)
		return string(r[:len(r)-1])
	case tea.KeyRunes, tea.KeySpace:
		return s + string(msg.Runes)
	}
	return s
}

// view renders the form filling the given pane dimensions. The prompt box
// expands to consume whatever vertical space is left after the fixed rows
// (header, name, repo, type, footer), so a wider/taller pane gives the user
// more room for the prompt without the form feeling cramped.
func (f newTaskForm) view(paneWidth, paneHeight int) string {
	// Box border + padding reservations. Keep the padding generous — this
	// is the full-screen modal, not a bottom-of-pane overlay.
	const (
		borderW = 2
		padX    = 2
		borderH = 2
		padY    = 1
	)
	inner := paneWidth - borderW - padX*2
	if inner < 24 {
		inner = 24
	}

	// Fixed-row budget: header(1) + blank(1) + name(1) + blank(1) +
	// repo(1) + blank(1) + type(1) + blank(1) + prompt-label(1) +
	// blank(1) + footer(1) = 11 rows of chrome around the prompt lines.
	const chromeRows = 11
	promptLines := paneHeight - borderH - padY*2 - chromeRows
	if promptLines < 4 {
		promptLines = 4
	}

	var b strings.Builder

	headerFiller := inner - len("new task") - len("esc to close")
	if headerFiller < 1 {
		headerFiller = 1
	}
	header := lipgloss.JoinHorizontal(
		lipgloss.Left,
		newTaskTitleStyle.Render("new task"),
		lipgloss.NewStyle().Width(headerFiller).Render(""),
		newTaskHintStyle.Render("esc to close"),
	)
	b.WriteString(header)
	b.WriteString("\n\n")

	b.WriteString(f.renderTextField("name", f.name, fieldName, inner))
	b.WriteString("\n\n")
	b.WriteString(f.renderTextField("repo", f.repo, fieldRepo, inner))
	b.WriteString("\n\n")
	b.WriteString(f.renderTypeRow(inner))
	b.WriteString("\n\n")
	b.WriteString(f.renderPromptBlock(inner, promptLines))
	b.WriteString("\n")

	// Footer action hint.
	submitLabel := "↵ create"
	if f.focus == fieldPrompt {
		submitLabel = "ctrl+d create"
	}
	submitStyle := newTaskFieldFocusStyle
	if !f.valid() {
		submitStyle = newTaskLabelStyle
	}
	footer := lipgloss.JoinHorizontal(
		lipgloss.Left,
		newTaskHintStyle.Render("esc cancel   "),
		submitStyle.Render(" "+submitLabel+" "),
	)
	b.WriteString(footer)

	return newTaskBoxStyle.
		Width(paneWidth - borderW).
		Height(paneHeight - borderH).
		Render(b.String())
}

func (f newTaskForm) renderTextField(label, value string, field newTaskField, inner int) string {
	labelWidth := 7 // "name   ", "repo   ", "prompt "
	valueWidth := inner - labelWidth
	if valueWidth < 10 {
		valueWidth = 10
	}

	display := value
	if f.focus == field {
		display += "█"
	}
	// Pad the value to fill the field background.
	if lipgloss.Width(display) < valueWidth {
		display += strings.Repeat(" ", valueWidth-lipgloss.Width(display))
	}

	fieldStyle := newTaskFieldStyle
	if f.focus == field {
		fieldStyle = newTaskFieldFocusStyle
	}

	return lipgloss.JoinHorizontal(
		lipgloss.Left,
		newTaskLabelStyle.Width(labelWidth).Render(label),
		fieldStyle.Render(display),
	)
}

func (f newTaskForm) renderTypeRow(inner int) string {
	parts := []string{newTaskLabelStyle.Width(7).Render("type")}
	for i, t := range taskTypes {
		if i == f.typeIdx {
			parts = append(parts, newTaskRadioSelStyle.Render(t))
		} else {
			parts = append(parts, newTaskRadioStyle.Render(t))
		}
		if i < len(taskTypes)-1 {
			parts = append(parts, " ")
		}
	}
	row := lipgloss.JoinHorizontal(lipgloss.Left, parts...)
	// Dim the label cue if the radio isn't focused — subtle but helps
	// signal which control keys will affect.
	if f.focus != fieldType {
		return row
	}
	return row + newTaskHintStyle.Render("   ←/→")
}

func (f newTaskForm) renderPromptBlock(inner, maxLines int) string {
	// Label sits on its own row — the prompt box spans the full inner
	// width so there's room for a proper multi-line body.
	label := newTaskLabelStyle.Render("prompt")

	if maxLines < 4 {
		maxLines = 4
	}
	valueWidth := inner
	if valueWidth < 20 {
		valueWidth = 20
	}

	lines := strings.Split(f.prompt, "\n")
	// Keep the most recent window of lines so the cursor is visible when
	// the user types past the visible region.
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	for len(lines) < maxLines {
		lines = append(lines, "")
	}

	if f.focus == fieldPrompt {
		lines[len(lines)-1] = lines[len(lines)-1] + "█"
	}

	fieldStyle := newTaskFieldStyle
	if f.focus == fieldPrompt {
		fieldStyle = newTaskFieldFocusStyle
	}

	var rendered []string
	for _, ln := range lines {
		if lipgloss.Width(ln) < valueWidth {
			ln += strings.Repeat(" ", valueWidth-lipgloss.Width(ln))
		}
		rendered = append(rendered, fieldStyle.Render(ln))
	}
	box := lipgloss.JoinVertical(lipgloss.Left, rendered...)
	return lipgloss.JoinVertical(lipgloss.Left, label, box)
}
