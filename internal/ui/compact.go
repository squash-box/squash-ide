package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/squashbox/squash-ide/internal/task"
	"github.com/squashbox/squash-ide/internal/tmux"
)

// Compact mode thresholds. When the terminal is narrow AND more than one
// task is active, the TUI pane is squeezed to CompactListWidth so the
// spawned panes can absorb the recovered columns.
const (
	CompactTriggerWidth    = 300
	CompactMinActiveSpawns = 2
	CompactListWidth       = 20
)

// isCompact reports whether compact mode should be active. Returns false
// while any modal dialog is open — dialogs take over the pane and need
// the normal width to render legibly, so compact stands down until the
// flow completes.
//
// The width arm keys on m.windowWidth (the tmux *window* / outer terminal
// column count, refreshed by refreshWindowWidth) rather than m.width.
// Inside tmux, m.width is the pane width, which gets pinned to
// CompactListWidth once compact engages — keying on it would leave the
// predicate stuck and prevent release when the user widens the terminal.
// Mirrors checkTooNarrow's choice to read tmux.WindowWidth as the source
// of truth. Outside tmux, m.windowWidth stays 0 and the predicate falls
// back to m.width, which is the terminal width directly.
func (m Model) isCompact() bool {
	w := m.windowWidth
	if w <= 0 {
		w = m.width
	}
	if w <= 0 || w >= CompactTriggerWidth {
		return false
	}
	if m.confirming != nil || m.completing != nil || m.deactivating != nil ||
		m.blocking != nil || m.creatingTask != nil {
		return false
	}
	return activeTaskCount(m.allTasks) >= CompactMinActiveSpawns
}

// refreshWindowWidth queries tmux for the outer window column count and
// caches it on the model. Best-effort: errors and zero readings leave the
// previous value in place so a transient tmux failure doesn't flip the
// predicate. Mirrors the swallow-on-cosmetic-failure pattern used for
// tmux.ResizePane below.
func (m *Model) refreshWindowWidth(pane string) {
	if pane == "" {
		return
	}
	ww, err := tmux.WindowWidth(pane)
	if err != nil || ww <= 0 {
		return
	}
	m.windowWidth = ww
}

func activeTaskCount(tasks []task.Task) int {
	n := 0
	for _, t := range tasks {
		if t.Status == "active" {
			n++
		}
	}
	return n
}

// renderTopBarCompact renders a narrow-format top bar: short app stub +
// condensed counts ("3a 2b 1x"). Budgeted to `width` columns (expected 20).
func renderTopBarCompact(width int, counts map[string]int) string {
	left := appTitleStyle.Render("sq")

	var parts []string
	if c := counts["active"]; c > 0 {
		parts = append(parts, fmt.Sprintf("%da", c))
	}
	if c := counts["backlog"]; c > 0 {
		parts = append(parts, fmt.Sprintf("%db", c))
	}
	if c := counts["blocked"]; c > 0 {
		parts = append(parts, fmt.Sprintf("%dx", c))
	}
	right := countsStyle.Render(strings.Join(parts, " "))

	gap := width - lipgloss.Width(left) - lipgloss.Width(right) - 2
	if gap < 1 {
		gap = 1
	}
	return " " + left + strings.Repeat(" ", gap) + right + " "
}

// helpLineCompact returns a ≤20-col help hint for the list view. Variants
// match the filter states; modal states are not reachable in compact mode
// (isCompact stands down while any dialog is open).
func helpLineCompact(filterActive, filterSet bool) string {
	switch {
	case filterActive:
		return helpStyle.Render("↵ ok  esc")
	case filterSet:
		return helpStyle.Render("/ edit  esc")
	default:
		return helpStyle.Render("j/k ↵ c d b q")
	}
}

// checkCompactPane mirrors checkTooNarrow's transition-only state machine
// for the compact-mode pane width. When the predicate flips, shell out to
// tmux to resize the TUI pane; when it stays the same, do nothing.
//
// Errors from tmux.ResizePane are swallowed on purpose — a failed resize
// is a cosmetic overflow (the renderer still produces valid output at
// 20 cols), not a crash condition. Same pattern as model.go's
// SetPaneBorderFormat call.
func (m *Model) checkCompactPane(pane string) {
	if pane == "" {
		return
	}
	m.refreshWindowWidth(pane)
	want := m.isCompact()
	if want == m.compact {
		return
	}
	m.compact = want
	width := m.cfg.Tmux.TUIWidth
	if width <= 0 {
		width = 60
	}
	if want {
		width = CompactListWidth
	}
	_ = tmux.ResizePane(pane, width)
}
