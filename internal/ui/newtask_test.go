package ui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func runeKey(r rune) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}
}

func TestNewTaskForm_TabCyclesFields(t *testing.T) {
	f := newNewTaskForm()
	if f.focus != fieldName {
		t.Fatalf("initial focus = %d, want fieldName", f.focus)
	}
	order := []newTaskField{fieldRepo, fieldType, fieldPrompt, fieldName}
	for i, want := range order {
		f, _, _ = f.handleKey(tea.KeyMsg{Type: tea.KeyTab})
		if f.focus != want {
			t.Errorf("after %d tabs: focus = %d, want %d", i+1, f.focus, want)
		}
	}
}

func TestNewTaskForm_ShiftTabCyclesBackward(t *testing.T) {
	f := newNewTaskForm()
	f, _, _ = f.handleKey(tea.KeyMsg{Type: tea.KeyShiftTab})
	if f.focus != fieldPrompt {
		t.Errorf("shift+tab from name: focus = %d, want fieldPrompt", f.focus)
	}
}

func TestNewTaskForm_TypeRadioNavigation(t *testing.T) {
	f := newNewTaskForm()
	// Default is "feature" (idx=1).
	if f.taskType() != "feature" {
		t.Errorf("default type = %q, want feature", f.taskType())
	}
	// Move focus to the type field.
	for f.focus != fieldType {
		f, _, _ = f.handleKey(tea.KeyMsg{Type: tea.KeyTab})
	}
	f, _, _ = f.handleKey(tea.KeyMsg{Type: tea.KeyLeft})
	if f.taskType() != "bug" {
		t.Errorf("after left: %q, want bug", f.taskType())
	}
	// Left at index 0 should clamp.
	f, _, _ = f.handleKey(tea.KeyMsg{Type: tea.KeyLeft})
	if f.taskType() != "bug" {
		t.Errorf("after second left: %q, want bug (clamped)", f.taskType())
	}
	// Right repeatedly to the end.
	for i := 0; i < 10; i++ {
		f, _, _ = f.handleKey(tea.KeyMsg{Type: tea.KeyRight})
	}
	if f.taskType() != "sec" {
		t.Errorf("after rights: %q, want sec", f.taskType())
	}
}

func TestNewTaskForm_EscCancels(t *testing.T) {
	f := newNewTaskForm()
	_, submitted, cancelled := f.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	if submitted {
		t.Error("esc should not submit")
	}
	if !cancelled {
		t.Error("esc should cancel")
	}
}

func TestNewTaskForm_EnterSubmitsWhenValid(t *testing.T) {
	f := newNewTaskForm()
	// Type "abc" into name.
	for _, r := range "abc" {
		f, _, _ = f.handleKey(runeKey(r))
	}
	// Tab to repo.
	f, _, _ = f.handleKey(tea.KeyMsg{Type: tea.KeyTab})
	for _, r := range "proj" {
		f, _, _ = f.handleKey(runeKey(r))
	}
	// Enter on repo should submit now that both required fields are set.
	_, submitted, cancelled := f.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	if !submitted {
		t.Error("enter should submit when name and repo are set")
	}
	if cancelled {
		t.Error("enter should not cancel")
	}
}

func TestNewTaskForm_EnterDoesNothingWhenInvalid(t *testing.T) {
	f := newNewTaskForm()
	_, submitted, _ := f.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	if submitted {
		t.Error("enter should not submit with empty name/repo")
	}
}

func TestNewTaskForm_EnterInPromptAddsNewline(t *testing.T) {
	f := newNewTaskForm()
	// Type into name + repo so it's otherwise valid.
	for _, r := range "a" {
		f, _, _ = f.handleKey(runeKey(r))
	}
	f, _, _ = f.handleKey(tea.KeyMsg{Type: tea.KeyTab})
	for _, r := range "p" {
		f, _, _ = f.handleKey(runeKey(r))
	}
	// Tab to prompt (via type).
	f, _, _ = f.handleKey(tea.KeyMsg{Type: tea.KeyTab}) // → type
	f, _, _ = f.handleKey(tea.KeyMsg{Type: tea.KeyTab}) // → prompt
	// Type "hi", then enter, then "there".
	for _, r := range "hi" {
		f, _, _ = f.handleKey(runeKey(r))
	}
	f, submitted, _ := f.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	if submitted {
		t.Error("enter in prompt should not submit")
	}
	for _, r := range "there" {
		f, _, _ = f.handleKey(runeKey(r))
	}
	if f.prompt != "hi\nthere" {
		t.Errorf("prompt = %q, want hi\\nthere", f.prompt)
	}
	// ctrl+d submits.
	_, submitted, _ = f.handleKey(tea.KeyMsg{Type: tea.KeyCtrlD})
	if !submitted {
		t.Error("ctrl+d in prompt should submit when valid")
	}
}

func TestNewTaskForm_Backspace(t *testing.T) {
	f := newNewTaskForm()
	for _, r := range "hello" {
		f, _, _ = f.handleKey(runeKey(r))
	}
	f, _, _ = f.handleKey(tea.KeyMsg{Type: tea.KeyBackspace})
	if f.name != "hell" {
		t.Errorf("name after backspace = %q, want hell", f.name)
	}
	// Backspace on empty is a no-op.
	f = newNewTaskForm()
	f, _, _ = f.handleKey(tea.KeyMsg{Type: tea.KeyBackspace})
	if f.name != "" {
		t.Errorf("backspace on empty produced %q", f.name)
	}
}

func TestNewTaskForm_ViewRenders(t *testing.T) {
	f := newNewTaskForm()
	got := f.view(60, 30)
	if got == "" {
		t.Error("view returned empty string")
	}
}
