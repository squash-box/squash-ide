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

// fullChromeMinWidth is the narrowest list width that still renders the full
// chrome (full top bar, expanded cards, long help). Below it the list switches
// to the compact layout. It sits well under tuiWidth so the responsive list
// keeps its full appearance across most of its range — the compact shape only
// engages near the CompactListWidth floor, where expanded card titles get too
// cramped (~10 cols) to read. Overflow above this (e.g. a multi-count top bar)
// is truncated by listViewRender's clamp rather than forcing compact early.
const fullChromeMinWidth = 30

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
	// Compact mode is a tmux-pane-width behaviour: it shrinks the TUI pane so
	// tmux can give the recovered columns to spawned panes. The native engine
	// composes the list and pane region itself at full terminal width, so
	// compact never applies — and in native mode m.width is the whole terminal,
	// which would otherwise trip the <300-col trigger spuriously.
	if m.engineNative {
		return false
	}
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

// nativeListWidth computes the responsive width of the native task list. The
// list scales linearly with the terminal between CompactListWidth (its floor)
// and tuiWidth (its ceiling), always reserving paneGutter+nativeMinPaneWidth
// for the pane region so spawned windows keep a usable minimum. Wide terminals
// park the list at its ceiling and hand every extra column to the panes; narrow
// ones shrink it toward the compact floor. This supersedes the binary T-052
// collapse, which snapped straight from full to CompactListWidth.
//
// It is the single source of truth for the native list's rendered width:
// rightRegion derives the pane region from it and listViewRender renders the
// list at it, so the reserved region and the rendered list can never disagree.
func (m Model) nativeListWidth() int {
	full := m.tuiWidth()
	if !m.engineNative || m.width <= 0 {
		return full
	}
	// A manual collapse (ctrl+b, T-056) forces the list to its compact floor so
	// the panes get the freed columns, overriding the responsive width. Guarded so
	// a degenerate tuiWidth <= CompactListWidth never *widens* the list.
	if m.listCollapsed && full > CompactListWidth {
		return CompactListWidth
	}
	w := m.width - paneGutter - nativeMinPaneWidth
	if w > full {
		w = full
	}
	if w < CompactListWidth {
		w = CompactListWidth
	}
	return w
}

// nativeListCompact reports whether the native list renders below its full
// (tuiWidth) ceiling — i.e. the terminal is narrow enough that the responsive
// list has ceded columns to the panes. It is a thin predicate over
// nativeListWidth; note the *chrome* switches to its compact layout at a lower
// width than this (see listViewRender), so a true result here does not by
// itself mean condensed cards.
func (m Model) nativeListCompact() bool {
	if !m.engineNative {
		return false
	}
	return m.nativeListWidth() < m.tuiWidth()
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
