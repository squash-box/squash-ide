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

	// T-039 lifecycle surface — the native replacement for dispatch's tmux
	// side effects, keyed on task id rather than internal pane id.

	// Spawn launches spec.Command in a new pane (Enter-to-spawn).
	Spawn(spec pane.SpawnSpec) (*pane.Pane, error)
	// CloseByTask tears down the pane running taskID (complete / deactivate).
	CloseByTask(taskID string) error
	// FocusByTask focuses the pane running taskID (notify-click focus).
	FocusByTask(taskID string) error
	// FocusedTaskID returns the task id of the focused pane, or "" if none is
	// focused — used to record which pane the user dismissed with ctrl+w (T-048).
	FocusedTaskID() string
	// SetStateByTask drives the pane's border badge from the status pipeline.
	SetStateByTask(taskID, state string)
	// CanSpawn reports whether the region admits one more pane (spawn pre-flight).
	CanSpawn() bool

	// T-040 responsive-layout surface — runtime layout controls and the badge
	// animation tick.

	// SetStrategy swaps the active layout strategy (cycle-layout keybinding).
	SetStrategy(s pane.Strategy)
	// FocusNext / FocusPrev cycle the focused pane — also next/prev tab under Tabs.
	FocusNext()
	FocusPrev()
	// ToggleCollapseFocused collapses/expands the focused (or first) pane.
	ToggleCollapseFocused()
	// Tick advances the input_required badge-blink phase.
	Tick()

	// T-050 modal popover surface — a single floating pane composited over the
	// tiled region for the native /log-task session.

	// SpawnModal starts spec.Command in the floating modal slot sized to box.
	SpawnModal(spec pane.SpawnSpec, box pane.Rect) (*pane.Pane, error)
	// ModalPane returns the open modal pane (nil if none) for the overlay render.
	ModalPane() *pane.Pane
	// HasModal reports whether a modal popover is open.
	HasModal() bool
	// WriteToModal forwards raw input bytes to the modal's child (no-op if none).
	WriteToModal(b []byte) (int, error)
	// ModalDone returns the modal child's exit channel (nil if no modal).
	ModalDone() <-chan struct{}
	// ResizeModal best-effort resizes the modal child to a new box geometry.
	ResizeModal(box pane.Rect)
	// CloseModal terminates + clears the modal (idempotent no-op if none).
	CloseModal() error
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
	if m.nativeListCompact() {
		// The list renders at CompactListWidth, so the manager gets the columns
		// the list actually leaves behind — m.width - 20 - gutter — instead of
		// the full-width reservation. This is the load-bearing T-052 fix.
		tui = CompactListWidth
	}
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
	// The floor is the compact list, not the full list: a terminal that can't
	// fit a full-width list still renders if it can fit a compact (20-col) list
	// plus a usable pane region. rightRegion/listViewRender collapse the list
	// to match. The overlay only fires below the compact floor (61 cols).
	needed := CompactListWidth + paneGutter + nativeMinPaneWidth
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
	joined := lipgloss.JoinHorizontal(lipgloss.Top, left, gap, right)

	// T-050: the /log-task popover is composited *over* the joined view so the
	// spawned panes stay visible behind it — unlike the takeover the tmux engine
	// still uses. Only engages while a modal is open.
	if m.manager.HasModal() {
		if mp := m.manager.ModalPane(); mp != nil {
			box := m.popoverBox()
			over := mp.Render(box.W, box.H, true, false)
			joined = placeOverlay(box.X, box.Y, over, joined)
		}
	}
	return joined
}

// maxPopoverWidth / maxPopoverHeight cap the /log-task popover so it never grows
// to the full terminal on a large screen — it should read as a centered modal
// with the spawned panes framing it. Below the cap it tracks the terminal at
// ~90%/80% (see popoverBox).
const (
	maxPopoverWidth  = 100
	maxPopoverHeight = 40
)

// popoverBox computes the centered bounding box (border included) for the
// /log-task popover: ~90% of the terminal width and ~80% of its height, each
// capped, and never larger than the terminal. The same box drives both the PTY
// child's interior size (SpawnModal/ResizeModal) and the overlay position, so
// they always agree.
func (m Model) popoverBox() pane.Rect {
	w := m.width * 9 / 10
	if w > maxPopoverWidth {
		w = maxPopoverWidth
	}
	if w > m.width {
		w = m.width
	}
	if w < 1 {
		w = 1
	}
	h := m.height * 8 / 10
	if h > maxPopoverHeight {
		h = maxPopoverHeight
	}
	if h > m.height {
		h = m.height
	}
	if h < 1 {
		h = 1
	}
	x := (m.width - w) / 2
	if x < 0 {
		x = 0
	}
	y := (m.height - h) / 2
	if y < 0 {
		y = 0
	}
	return pane.Rect{X: x, Y: y, W: w, H: h}
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
