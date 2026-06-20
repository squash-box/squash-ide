package ui

import "github.com/charmbracelet/bubbles/key"

type keyMap struct {
	Up          key.Binding
	Down        key.Binding
	Enter       key.Binding
	Detail      key.Binding
	Back        key.Binding
	Filter      key.Binding
	Refresh     key.Binding
	Quit        key.Binding
	Confirm     key.Binding
	Deny        key.Binding
	Complete    key.Binding
	Block       key.Binding
	Deactivate  key.Binding
	NewTask     key.Binding
	PaneFocus   key.Binding
	CycleLayout key.Binding
	NextTab     key.Binding
	PrevTab     key.Binding
	Collapse    key.Binding
}

var keys = keyMap{
	Up: key.NewBinding(
		key.WithKeys("up", "k"),
		key.WithHelp("↑/k", "up"),
	),
	Down: key.NewBinding(
		key.WithKeys("down", "j"),
		key.WithHelp("↓/j", "down"),
	),
	Enter: key.NewBinding(
		key.WithKeys("enter"),
		key.WithHelp("enter", "spawn/detail"),
	),
	Detail: key.NewBinding(
		key.WithKeys("tab"),
		key.WithHelp("tab", "detail"),
	),
	Back: key.NewBinding(
		key.WithKeys("esc"),
		key.WithHelp("esc", "back"),
	),
	Filter: key.NewBinding(
		key.WithKeys("/"),
		key.WithHelp("/", "filter"),
	),
	Refresh: key.NewBinding(
		key.WithKeys("r"),
		key.WithHelp("r", "refresh"),
	),
	Quit: key.NewBinding(
		key.WithKeys("q", "ctrl+c"),
		key.WithHelp("q", "quit"),
	),
	Confirm: key.NewBinding(
		key.WithKeys("y", "Y"),
	),
	Deny: key.NewBinding(
		key.WithKeys("n", "N"),
	),
	Complete: key.NewBinding(
		key.WithKeys("c"),
		key.WithHelp("c", "complete"),
	),
	Block: key.NewBinding(
		key.WithKeys("b"),
		key.WithHelp("b", "block"),
	),
	Deactivate: key.NewBinding(
		key.WithKeys("d"),
		key.WithHelp("d", "deactivate"),
	),
	NewTask: key.NewBinding(
		key.WithKeys("t"),
		key.WithHelp("t", "new task"),
	),
	// PaneFocus toggles focus between the task list and the native pane region
	// (native engine only). ctrl+w reads as "window focus" and is reserved —
	// it is intercepted, never forwarded to a focused pane's child.
	PaneFocus: key.NewBinding(
		key.WithKeys("ctrl+w"),
		key.WithHelp("ctrl+w", "focus pane"),
	),
	// Native-engine layout controls (T-040). They act in list mode only, so they
	// can be plain keys without colliding with a focused pane's child input.
	CycleLayout: key.NewBinding(
		key.WithKeys("L"),
		key.WithHelp("L", "cycle layout"),
	),
	NextTab: key.NewBinding(
		key.WithKeys("]"),
		key.WithHelp("]", "next tab"),
	),
	PrevTab: key.NewBinding(
		key.WithKeys("["),
		key.WithHelp("[", "prev tab"),
	),
	Collapse: key.NewBinding(
		key.WithKeys("z"),
		key.WithHelp("z", "collapse pane"),
	),
}
