package ui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/squashbox/squash-ide/internal/pane"
)

// paneManager is the subset of *pane.Manager the native engine UI depends on.
// It is a consumer-side interface (defined here, not in the pane package) so
// the UI's tests can inject a stub that records Resize/Write calls without
// forking a real PTY — the pane package's PTY seam (ptyStarter) is
// package-private and cannot be faked from outside. *pane.Manager satisfies
// this interface in production.
type paneManager interface {
	// Resize records the available right-region and re-tiles the panes.
	Resize(region pane.Rect)
	// Render lays the panes out across region and returns the composed string
	// ("" when there are no panes or the layout is rejected).
	Render(region pane.Rect) string
	// WriteToFocused forwards raw input bytes to the focused pane's child.
	WriteToFocused(b []byte) (int, error)
	// Repaint is the coalescing output signal the UI selects on to know a
	// re-render is due.
	Repaint() <-chan struct{}
}

// paneGutter is the blank-column separator between the task list and the
// native pane region. One column keeps the two columns visually distinct,
// mirroring tmux's one-border-between-panes look.
const paneGutter = 1

// nativeMinPaneWidth is the floor (in cols) the right region must clear for the
// native engine to attempt a render. Below TUIWidth+gutter+this we show a
// "too narrow" overlay instead of a squeezed, illegible pane — the native
// analogue of the tmux tooNarrow view.
const nativeMinPaneWidth = 40

// rightRegion computes the bounding box the pane manager renders into: the
// columns to the right of the fixed-width task list, less the gutter. Height
// is the full terminal height. Mirrors the test-plan contract
// (W == width - TUIWidth - gutter).
func (m Model) rightRegion() pane.Rect {
	tui := m.tuiWidth()
	x := tui + paneGutter
	w := m.width - x
	if w < 0 {
		w = 0
	}
	h := m.height
	if h < 0 {
		h = 0
	}
	return pane.Rect{X: x, Y: 0, W: w, H: h}
}

// tuiWidth returns the configured left-list width, falling back to 60 when
// unset — the same default the tmux path uses.
func (m Model) tuiWidth() int {
	w := m.cfg.Tmux.TUIWidth
	if w <= 0 {
		w = 60
	}
	return w
}

// nativeView composes the native-engine screen: the task list on the left and
// the pane manager's render (or an empty-state placeholder) on the right,
// joined horizontally. When the terminal cannot fit the list plus a usable
// pane it shows a "too narrow" overlay instead — the native analogue of the
// tmux tooNarrow view, with no tmux shell-out.
func (m Model) nativeView() string {
	tui := m.tuiWidth()
	needed := tui + paneGutter + nativeMinPaneWidth
	if m.width > 0 && m.width < needed {
		return m.nativeTooNarrowView(needed)
	}

	// Detail view still takes over the whole screen, as in tmux mode.
	if m.view == detailView {
		return m.detailViewRender()
	}

	left := m.listViewRender()

	region := m.rightRegion()
	right := m.manager.Render(region)
	if right == "" {
		right = m.renderPanePlaceholder(region)
	}

	gap := lipgloss.NewStyle().Width(paneGutter).Render("")
	return lipgloss.JoinHorizontal(lipgloss.Top, left, gap, right)
}

// nativeTooNarrowView renders the full-screen "terminal too narrow" overlay for
// native mode. Unlike the tmux tooNarrow path it never shells out — it is a
// pure render keyed on m.width.
func (m Model) nativeTooNarrowView(needed int) string {
	msg := fmt.Sprintf(
		"Terminal too narrow\n\nNeeded: %d cols\n(native engine)\n\nWiden the terminal to use\nthe native pane region",
		needed,
	)
	styled := lipgloss.NewStyle().
		Foreground(lipgloss.Color("204")).
		Bold(true).
		Align(lipgloss.Center).
		Render(msg)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, styled)
}

// renderPanePlaceholder fills the right region with a muted empty-state shown
// while no panes are running (the whole of T-038 — spawning into the manager
// lands in T-039). It reuses the placeholder palette so it reads as part of
// the same UI without claiming tmux slots.
func (m Model) renderPanePlaceholder(region pane.Rect) string {
	if region.W <= 0 || region.H <= 0 {
		return ""
	}
	circle := lipgloss.NewStyle().
		Border(dashedBorder).
		BorderForeground(phMuted).
		Foreground(phDim).
		Padding(1, 4).
		Bold(true).
		Render("+")
	title := lipgloss.NewStyle().Foreground(phDim).Render("Native pane region")
	hint := lipgloss.NewStyle().Foreground(phMuted).Render("No panes yet — spawn lands in T-039")
	body := lipgloss.JoinVertical(lipgloss.Center, circle, "", title, hint)
	return lipgloss.Place(region.W, region.H, lipgloss.Center, lipgloss.Center, body)
}

// waitForPaneOutput blocks on the manager's coalescing repaint channel and
// turns the next signal into a paneOutputMsg. Bubble Tea is pull-based, so this
// self-rescheduling command (re-issued from the paneOutputMsg handler) is how a
// pane's background output triggers a re-render — the same pattern as
// tickStatus. Native mode only.
func (m Model) waitForPaneOutput() tea.Cmd {
	repaint := m.manager.Repaint()
	return func() tea.Msg {
		<-repaint
		return paneOutputMsg{}
	}
}
