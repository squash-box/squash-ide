package pane

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
)

// ErrUnknownPane is returned (wrapped) by Focus/Close when no pane has the given
// id. Callers match it with errors.Is to treat an unknown id as a no-op rather
// than a fatal error.
var ErrUnknownPane = errors.New("unknown pane")

// SpawnSpec describes a child to launch in a new pane. Command is the prepared
// command (the caller builds it via config.BuildExec in T-038); the metadata
// fields populate the pane's painted border, replacing tmux's @squash-* pane
// options.
type SpawnSpec struct {
	Command *exec.Cmd
	TaskID  string
	Title   string
	Project string
}

// Manager owns the ordered set of panes, which one has focus, and the layout
// Strategy that turns the available region into per-pane geometry. It is the
// native replacement for the tmux-side layout authority (internal/tmux + the
// tmux calls in spawner.runTmux).
type Manager struct {
	mu          sync.Mutex
	panes       []*Pane
	focusID     string
	strategy    Strategy
	constraints Constraints
	region      Rect
	starter     ptyStarter
	nextID      int

	// modal is a single floating pane composited *over* the tiled region (the
	// /log-task popover, T-050). It is deliberately kept out of m.panes so no
	// layout Strategy ever tiles it — a popover floats, it isn't a column. Only
	// one may be open at a time; SpawnModal rejects a second.
	modal *Pane

	// collapsed marks panes shown as a thin strip rather than a full box
	// (T-040). Keyed by pane id; toggling reflows the siblings.
	collapsed map[string]bool

	// blinkOn is the badge-animation phase, flipped by Tick. It rides the UI's
	// status tick rather than a timer of its own (the [[T-024]]/T-040 idiom), so
	// the input_required badge pulses without a new goroutine.
	blinkOn bool

	// repaint is a coalescing repaint signal: the UI selects on it (T-038) and
	// re-renders. A buffered-size-1 channel with non-blocking send collapses a
	// burst of pane output into a single pending repaint.
	repaint chan struct{}
}

// Option configures a Manager.
type Option func(*Manager)

// WithStrategy overrides the layout strategy (default FlexColumns).
func WithStrategy(s Strategy) Option { return func(m *Manager) { m.strategy = s } }

// WithConstraints overrides the layout constraints (default MinWidth 20,
// Gutter 1).
func WithConstraints(c Constraints) Option { return func(m *Manager) { m.constraints = c } }

// WithStarter overrides the PTY seam. Tests inject a fake starter so Spawn
// never forks a real process — the internal/exec.Runner pattern.
func WithStarter(s ptyStarter) Option { return func(m *Manager) { m.starter = s } }

// NewManager builds a Manager with sensible defaults, applying any options.
func NewManager(opts ...Option) *Manager {
	m := &Manager{
		strategy:    FlexColumns{},
		constraints: Constraints{MinWidth: 20, MinHeight: 5, Gutter: 1},
		collapsed:   map[string]bool{},
		starter:     DefaultStarter,
		repaint:     make(chan struct{}, 1),
	}
	for _, o := range opts {
		o(m)
	}
	return m
}

// Repaint returns the coalescing repaint channel. The UI ranges/selects on it
// to know when a pane produced output or changed state and a re-render is due.
func (m *Manager) Repaint() <-chan struct{} { return m.repaint }

func (m *Manager) requestRepaint() {
	select {
	case m.repaint <- struct{}{}:
	default: // a repaint is already pending; coalesce
	}
}

// Spawn launches spec.Command in a new pane. It mirrors spawner.runTmux's
// ordering: create the pane, then re-tile and, if the layout rejects it, tear
// the just-created pane down (kill-on-reject — spawner.go:173-176) so no
// half-created pane is left behind. Focus is deliberately left where it was —
// the new pane does not steal focus ([[T-031]]).
func (m *Manager) Spawn(spec SpawnSpec) (*Pane, error) {
	if spec.Command == nil {
		return nil, fmt.Errorf("pane: Spawn requires a Command")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	region := m.region

	master, proc, err := m.starter.Start(spec.Command)
	if err != nil {
		return nil, fmt.Errorf("pane: starting %q on a pty: %w", cmdName(spec.Command), err)
	}

	m.nextID++
	id := fmt.Sprintf("pane-%d", m.nextID)
	setsize := func(rows, cols uint16) error { return m.starter.Setsize(master, rows, cols) }
	p := newPane(id, spec.TaskID, spec.Title, spec.Project, master, proc, 80, 24, setsize, m.requestRepaint)
	m.panes = append(m.panes, p)

	// Verify the layout admits n panes once we know the region. Before the UI
	// has reported a size (region zero), admit unconditionally and let the
	// first Resize tile. Under a reflowing strategy (Responsive) this never
	// rejects — it degrades columns→stack→tabs; under layout: columns it still
	// hard-rejects, the contrast the acceptance demo shows.
	if region.W > 0 && region.H > 0 {
		rects, _, lerr := computeRects(m.strategy, region, m.panes, m.collapsed, m.constraints)
		if lerr != nil {
			m.panes = m.panes[:len(m.panes)-1] // un-append
			_ = p.Close()                      // kill + reap the just-created child
			return nil, fmt.Errorf("pane: layout rejected new pane: %w", lerr)
		}
		m.applyLayoutLocked(rects)
	}

	debugf("manager: spawned %s (task %s) — %d pane(s)", id, spec.TaskID, len(m.panes))
	m.requestRepaint()
	return p, nil
}

// Close removes the pane with id, terminating its child and reaping it. If the
// closed pane held focus, focus shifts to the nearest remaining pane. An
// unknown id returns ErrUnknownPane. The child-exit path does NOT remove a
// pane (it goes StateDead and stays — remain-on-exit); Close is the explicit
// teardown.
func (m *Manager) Close(id string) error {
	m.mu.Lock()
	idx := m.indexOfLocked(id)
	if idx < 0 {
		m.mu.Unlock()
		return fmt.Errorf("pane: close %s: %w", id, ErrUnknownPane)
	}
	p := m.panes[idx]
	m.panes = append(m.panes[:idx], m.panes[idx+1:]...)
	delete(m.collapsed, id) // a closed pane carries no collapse state
	if m.focusID == id {
		m.focusID = ""
		if len(m.panes) > 0 {
			ni := idx
			if ni >= len(m.panes) {
				ni = len(m.panes) - 1
			}
			m.focusID = m.panes[ni].id
		}
	}
	region, n := m.region, len(m.panes)
	m.mu.Unlock()

	err := p.Close() // blocks on the read goroutine; done outside the lock

	m.mu.Lock()
	if n > 0 && region.W > 0 && region.H > 0 {
		if rects, _, lerr := computeRects(m.strategy, region, m.panes, m.collapsed, m.constraints); lerr == nil {
			m.applyLayoutLocked(rects)
		} else {
			warnf("manager: re-tile after close failed (swallowed): %v", lerr)
		}
	}
	m.mu.Unlock()

	debugf("manager: closed %s — %d pane(s) remain", id, n)
	m.requestRepaint()
	return err
}

// Focus makes the pane with id the focused pane. An unknown id is a no-op that
// returns ErrUnknownPane, leaving focus unchanged.
func (m *Manager) Focus(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.indexOfLocked(id) < 0 {
		return fmt.Errorf("pane: focus %s: %w", id, ErrUnknownPane)
	}
	if m.focusID != id {
		m.focusID = id
		m.requestRepaint()
	}
	return nil
}

// CloseByTask closes the pane running taskID. It is the native analogue of the
// tmux teardown's FindPaneByTask → KillPane: the TUI keys lifecycle off the task
// id, not the internal pane id. An unknown task id is a no-op returning
// ErrUnknownPane, so a complete/deactivate of a task with no live pane (spawned
// in a prior, now-dead TUI session) tears the rest of the dispatch flow down
// cleanly. Delegates to Close so the focus-shift and re-tile logic is shared.
func (m *Manager) CloseByTask(taskID string) error {
	m.mu.Lock()
	idx := m.indexOfTaskLocked(taskID)
	if idx < 0 {
		m.mu.Unlock()
		return fmt.Errorf("pane: close task %s: %w", taskID, ErrUnknownPane)
	}
	id := m.panes[idx].id
	m.mu.Unlock()
	return m.Close(id)
}

// FocusByTask focuses the pane running taskID. Unknown task id is a no-op
// returning ErrUnknownPane (the notify-click focus path swallows it so a click
// for a task whose pane has gone away never crashes). Delegates to Focus.
func (m *Manager) FocusByTask(taskID string) error {
	m.mu.Lock()
	idx := m.indexOfTaskLocked(taskID)
	if idx < 0 {
		m.mu.Unlock()
		return fmt.Errorf("pane: focus task %s: %w", taskID, ErrUnknownPane)
	}
	id := m.panes[idx].id
	m.mu.Unlock()
	return m.Focus(id)
}

// SetStateByTask updates the lifecycle state of the pane running taskID, driving
// its painted border badge. It is how the TUI's status-file poll (the same
// working|idle|input_required|testing pipeline the tmux border consumed) reaches
// the native pane. Unknown task id is a silent no-op; a dead pane ignores the
// write (Pane.SetState freezes once dead), preserving the [[T-035]] remain-on-
// exit badge.
//
// Badge-only: this NEVER moves focus (T-048). Focus-follows-input is owned
// entirely by the UI, which gates it on user intent (a modal is open, or the
// user dismissed the pane with ctrl+w) — state the manager has no view of. The
// manager renders the input_required badge; the UI alone decides whether to
// surface the pane. Pane.SetState fires its own repaint on a state change, so
// the badge updates without a manager-level repaint here.
func (m *Manager) SetStateByTask(taskID, state string) {
	m.mu.Lock()
	idx := m.indexOfTaskLocked(taskID)
	if idx < 0 {
		m.mu.Unlock()
		return
	}
	p := m.panes[idx]
	m.mu.Unlock()

	p.SetState(state)
}

// SetStatsByTask updates the CPU/memory readout of the pane running taskID
// (T-055), driving the right-floated header stats. It mirrors SetStateByTask:
// locate the pane under m.mu, release, then mutate the pane (Pane.SetStats
// fires its own repaint on a change). Unknown task id is a silent no-op; a dead
// pane ignores the write (Pane.SetStats freezes once dead), so an exited pane's
// last reading stays frozen with its EXITED badge.
func (m *Manager) SetStatsByTask(taskID string, cpuPct float64, cpuValid bool, memBytes uint64, ok bool) {
	m.mu.Lock()
	idx := m.indexOfTaskLocked(taskID)
	if idx < 0 {
		m.mu.Unlock()
		return
	}
	p := m.panes[idx]
	m.mu.Unlock()

	p.SetStats(cpuPct, cpuValid, memBytes, ok)
}

// PIDsByTask maps each live, task-bound pane's task id to its child PID, for the
// UI's resource sampler (T-055). It skips dead panes (their child has exited —
// no group to sample) and any pane whose Process reports a non-positive PID
// (proc nil / not started). The modal popover is excluded by construction: it
// lives in m.modal, not m.panes (T-050).
func (m *Manager) PIDsByTask() map[string]int {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]int, len(m.panes))
	for _, p := range m.panes {
		if p.taskID == "" || p.IsDead() {
			continue
		}
		pid := p.proc.Pid()
		if pid <= 0 {
			continue
		}
		out[p.taskID] = pid
	}
	return out
}

// CanSpawn reports whether the current region admits one more pane under the
// active Strategy/Constraints. The TUI calls it as a pre-flight before touching
// the vault, the native analogue of dispatch's tmux width check — so a spawn
// that wouldn't fit is rejected without orphaning an active task. Before the UI
// has reported a size (region zero) it admits, mirroring Spawn's "admit until
// the first Resize tiles" rule.
func (m *Manager) CanSpawn() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	region := m.region
	if region.W <= 0 || region.H <= 0 {
		return true
	}
	n := len(m.panes) + 1
	// The prospective pane spawns expanded; existing collapsed panes stay strips.
	nCollapsed := countCollapsed(m.panes, m.collapsed)
	concrete, axis := resolveStrategy(m.strategy, region, n, m.constraints, nCollapsed)
	if axis == axisTabbed {
		_, err := concrete.Arrange(region, n, m.constraints)
		return err == nil
	}
	// axisMainStack only resolves when nCollapsed == 0, so reduceRegion is a
	// no-op there and this fits the whole region — the same call columns/rows use.
	_, err := concrete.Arrange(reduceRegion(region, nCollapsed, axis, m.constraints), n-nCollapsed, m.constraints)
	return err == nil
}

// HasPaneForTask reports whether a live (manager-tracked) pane exists for
// taskID. A pane that has gone StateDead but not been Closed still counts —
// it is still in the set.
func (m *Manager) HasPaneForTask(taskID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.indexOfTaskLocked(taskID) >= 0
}

// WriteToFocused forwards raw input bytes (typically from EncodeKey) to the
// focused pane's child PTY. It returns (0, nil) when no pane is focused, so the
// native UI's input router (T-038) can forward a keystroke unconditionally
// without first nil-checking Focused(). A write to a closed pane surfaces the
// pane's own error.
func (m *Manager) WriteToFocused(b []byte) (int, error) {
	p := m.Focused()
	if p == nil {
		return 0, nil
	}
	return p.Write(b)
}

// Focused returns the focused pane, or nil if none is focused.
func (m *Manager) Focused() *Pane {
	m.mu.Lock()
	defer m.mu.Unlock()
	idx := m.indexOfLocked(m.focusID)
	if idx < 0 {
		return nil
	}
	return m.panes[idx]
}

// FocusedTaskID returns the task id of the focused pane, or "" if none is
// focused. It is the consumer-facing projection of Focused() the UI uses to
// record which pane the user dismissed with ctrl+w (T-048), without coupling the
// UI's paneManager interface to the concrete *Pane type.
func (m *Manager) FocusedTaskID() string {
	if p := m.Focused(); p != nil {
		return p.TaskID()
	}
	return ""
}

// TaskAtPoint maps an absolute screen cell (x, y) to the task id of the pane
// rendered there. It recomputes the live geometry from the retained region via
// the pure computeRects rather than caching a rects slice on the Manager —
// avoiding a second source of truth and the Render/Resize-vs-cache staleness
// window, at the cost of one extra layout pass per call (negligible against a
// human click rate). It returns ("", false) when no addressable pane covers the
// point: no panes, an un-sized region, a layout the region can't admit, a click
// in the gutter / tab strip, or a placeholder pane with no task id.
//
// It is the geometry seam click-to-focus is built on (T-051): the UI turns a
// tea.MouseMsg's absolute coords into a *task id*, then drives FocusByTask — the
// task-id-keyed focus API the rest of the UI already uses (T-039), so the UI
// never handles internal pane ids. tea.MouseMsg.X/Y and Rect are both in 0-based
// absolute screen cells, so the contains-test needs no offset translation.
func (m *Manager) TaskAtPoint(x, y int) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.panes) == 0 || m.region.W <= 0 || m.region.H <= 0 {
		return "", false
	}
	rects, axis, err := computeRects(m.strategy, m.region, m.panes, m.collapsed, m.constraints)
	if err != nil {
		// Mirror Render/Resize's swallow-and-degrade ([[T-031]] best-effort idiom):
		// a stray click while the region is mid-shrink must never crash the TUI.
		warnf("manager: TaskAtPoint layout rejected (swallowed): %v", err)
		return "", false
	}

	// Tabbed: every rect is the identical content area, so a positional scan would
	// always pick index 0. Only the active tab is visible — a hit in the content
	// area resolves to it (composeTabs' tabsActiveIndex: the focused pane, or the
	// first when focus is elsewhere); a click in the tab strip above the content is
	// not a pane click (tab-strip click-to-select is out of scope, T-051).
	if axis == axisTabbed {
		r := rects[0]
		if x >= r.X && x < r.X+r.W && y >= r.Y && y < r.Y+r.H {
			if id := m.panes[tabsActiveIndex(m.panes, m.focusID)].taskID; id != "" {
				return id, true
			}
		}
		return "", false
	}

	// Linear (columns / rows): the first rect whose half-open bounds contain the
	// point. Half-open (x < r.X+r.W) puts a cell exactly on a rect's far edge in
	// the next pane / the gutter, never double-counted.
	for i, r := range rects {
		if i >= len(m.panes) {
			break
		}
		if x >= r.X && x < r.X+r.W && y >= r.Y && y < r.Y+r.H {
			if id := m.panes[i].taskID; id != "" {
				return id, true
			}
			return "", false
		}
	}
	return "", false
}

// SetStrategy swaps the active layout strategy and re-tiles. It is the runtime
// half of the T-037 Open/Closed seam: the cycle-layout keybinding switches
// columns→stack→tabs→responsive without the Manager knowing the concrete types.
// A nil strategy is ignored.
func (m *Manager) SetStrategy(s Strategy) {
	if s == nil {
		return
	}
	m.mu.Lock()
	m.strategy = s
	m.retileLocked()
	m.mu.Unlock()
	debugf("manager: layout strategy set")
	m.requestRepaint()
}

// ToggleCollapse flips whether the pane with id renders as a thin strip, then
// re-tiles so siblings reflow. Unknown id is a no-op.
func (m *Manager) ToggleCollapse(id string) {
	m.mu.Lock()
	if m.indexOfLocked(id) < 0 {
		m.mu.Unlock()
		return
	}
	now := !m.collapsed[id]
	if now {
		m.collapsed[id] = true
	} else {
		delete(m.collapsed, id)
	}
	m.retileLocked()
	m.mu.Unlock()
	debugf("manager: collapse %s -> %v", id, now)
	m.requestRepaint()
}

// ToggleCollapseFocused collapses/expands the focused pane, or the first pane
// when focus is on the list. A no-op when there are no panes — the UI binds the
// collapse key to it so the user needn't know internal pane ids.
func (m *Manager) ToggleCollapseFocused() {
	m.mu.Lock()
	id := m.focusID
	if m.indexOfLocked(id) < 0 {
		if len(m.panes) == 0 {
			m.mu.Unlock()
			return
		}
		id = m.panes[0].id
	}
	m.mu.Unlock()
	m.ToggleCollapse(id)
}

// FocusNext moves focus to the next pane in order (wrapping). It is also the
// next-tab action under the Tabs layout, where the focused pane is the active
// tab. With no panes it is a no-op; with none focused it focuses the first.
func (m *Manager) FocusNext() { m.focusStep(+1) }

// FocusPrev moves focus to the previous pane in order (wrapping); the prev-tab
// action under Tabs.
func (m *Manager) FocusPrev() { m.focusStep(-1) }

func (m *Manager) focusStep(delta int) {
	m.mu.Lock()
	n := len(m.panes)
	if n == 0 {
		m.mu.Unlock()
		return
	}
	idx := m.indexOfLocked(m.focusID)
	if idx < 0 {
		idx = 0 // nothing focused yet — start at the first pane
	} else {
		idx = ((idx+delta)%n + n) % n
	}
	m.focusID = m.panes[idx].id
	id := m.focusID
	m.mu.Unlock()
	debugf("manager: focus -> %s", id)
	m.requestRepaint()
}

// Tick advances the badge-animation phase and requests a repaint. The UI calls
// it on its status tick so the input_required badge pulses without a dedicated
// timer. Safe at any time (it touches no pane), so a tick that lands after a
// pane closed is harmless.
func (m *Manager) Tick() {
	m.mu.Lock()
	m.blinkOn = !m.blinkOn
	m.mu.Unlock()
	m.requestRepaint()
}

// retileLocked recomputes geometry under the active strategy and collapse state
// and resizes the panes. Best-effort: a rejection is warn-logged and the prior
// geometry retained ([[T-031]] idiom). Caller holds mu.
func (m *Manager) retileLocked() {
	if len(m.panes) == 0 || m.region.W <= 0 || m.region.H <= 0 {
		return
	}
	rects, _, err := computeRects(m.strategy, m.region, m.panes, m.collapsed, m.constraints)
	if err != nil {
		warnf("manager: re-tile rejected (swallowed): %v", err)
		return
	}
	m.applyLayoutLocked(rects)
}

// Resize records the new available region and re-tiles every pane. A layout
// that no longer fits is warn-logged and the previous geometry retained, rather
// than crashing the TUI ([[T-031]] best-effort idiom).
func (m *Manager) Resize(region Rect) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.region = region
	n := len(m.panes)
	if n == 0 {
		return
	}
	rects, _, err := computeRects(m.strategy, region, m.panes, m.collapsed, m.constraints)
	if err != nil {
		warnf("manager: resize layout rejected (swallowed): %v", err)
		return
	}
	m.applyLayoutLocked(rects)
	debugf("manager: resized to %dx%d — %d pane(s)", region.W, region.H, n)
}

// Render lays the panes out across region under the active strategy and stitches
// their rendered boxes together along the chosen axis: columns (left to right),
// rows (top to bottom), or a tab strip + one pane. Collapsed panes render as
// strips. A layout failure yields an empty string rather than a panic.
func (m *Manager) Render(region Rect) string {
	m.mu.Lock()
	panes := append([]*Pane(nil), m.panes...)
	focusID := m.focusID
	gutter := m.constraints.Gutter
	strategy := m.strategy
	constraints := m.constraints
	collapsed := make(map[string]bool, len(m.collapsed))
	for k, v := range m.collapsed {
		collapsed[k] = v
	}
	blink := m.blinkOn
	m.mu.Unlock()

	if len(panes) == 0 {
		return ""
	}
	rects, axis, err := computeRects(strategy, region, panes, collapsed, constraints)
	if err != nil {
		warnf("manager: render layout rejected (swallowed): %v", err)
		return ""
	}
	if axis == axisTabbed {
		return composeTabs(region, panes, rects, focusID, blink)
	}
	if axis == axisMainStack {
		return composeMainStack(panes, rects, focusID, gutter, blink)
	}
	return composeLinear(axis, panes, rects, collapsed, focusID, gutter, blink)
}

// Panes returns a snapshot copy of the pane set, in order.
func (m *Manager) Panes() []*Pane {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]*Pane(nil), m.panes...)
}

// --- modal popover (T-050) --------------------------------------------------
//
// A modal is a single PTY pane composited *over* the tiled region rather than
// laid out within it (the /log-task popover). It reuses the same starter seam,
// newPane constructor, and repaint channel as Spawn, but is stored in m.modal
// and never appended to m.panes, so no Strategy ever tiles it.

// SpawnModal starts spec.Command in the floating modal slot, sized to the
// popover box's interior so the child reflows to the box. It errors if a modal
// is already open (single-slot only), leaving the existing one intact. Focus is
// untouched — the UI routes keys to the modal via WriteToModal while the popover
// flag is set, independent of pane focus.
func (m *Manager) SpawnModal(spec SpawnSpec, box Rect) (*Pane, error) {
	if spec.Command == nil {
		return nil, fmt.Errorf("pane: SpawnModal requires a Command")
	}

	m.mu.Lock()
	if m.modal != nil {
		m.mu.Unlock()
		return nil, fmt.Errorf("pane: a modal is already open")
	}
	master, proc, err := m.starter.Start(spec.Command)
	if err != nil {
		m.mu.Unlock()
		return nil, fmt.Errorf("pane: starting modal %q on a pty: %w", cmdName(spec.Command), err)
	}
	m.nextID++
	id := fmt.Sprintf("modal-%d", m.nextID)
	setsize := func(rows, cols uint16) error { return m.starter.Setsize(master, rows, cols) }
	cols, rows := interiorCols(box.W), interiorRows(box.H)
	p := newPane(id, spec.TaskID, spec.Title, spec.Project, master, proc, cols, rows, setsize, m.requestRepaint)
	m.modal = p
	m.mu.Unlock()

	// Size the child to the popover interior so claude lays out to the box.
	p.Resize(rows, cols)
	debugf("manager: spawned modal %s (task %s) at %dx%d", id, spec.TaskID, box.W, box.H)
	m.requestRepaint()
	return p, nil
}

// ModalPane returns the open modal pane, or nil. The UI needs the concrete pane
// to render its box for the overlay composite.
func (m *Manager) ModalPane() *Pane {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.modal
}

// HasModal reports whether a modal popover is currently open.
func (m *Manager) HasModal() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.modal != nil
}

// WriteToModal forwards raw input bytes (from EncodeKey) to the modal's child
// PTY. It is a no-op (0, nil) when no modal is open, mirroring WriteToFocused so
// the UI's popover key router can forward unconditionally.
func (m *Manager) WriteToModal(b []byte) (int, error) {
	m.mu.Lock()
	p := m.modal
	m.mu.Unlock()
	if p == nil {
		return 0, nil
	}
	return p.Write(b)
}

// ModalDone returns the modal child's exit channel (closed when claude exits),
// or a nil channel when no modal is open — a receive on which blocks forever, so
// the UI only calls this after a successful SpawnModal.
func (m *Manager) ModalDone() <-chan struct{} {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.modal == nil {
		return nil
	}
	return m.modal.Done()
}

// ResizeModal best-effort resizes the modal child to a new popover box's
// interior (e.g. on a terminal resize). A no-op when no modal is open.
func (m *Manager) ResizeModal(box Rect) {
	m.mu.Lock()
	p := m.modal
	m.mu.Unlock()
	if p == nil {
		return
	}
	p.Resize(interiorRows(box.H), interiorCols(box.W))
}

// CloseModal terminates the modal child, reaps it, and clears the slot. It is an
// idempotent no-op when no modal is open, so a force-close after the child has
// already auto-exited is safe (double-close).
func (m *Manager) CloseModal() error {
	m.mu.Lock()
	p := m.modal
	m.modal = nil
	m.mu.Unlock()
	if p == nil {
		return nil
	}
	err := p.Close()
	debugf("manager: closed modal %s", p.ID())
	m.requestRepaint()
	return err
}

// applyLayoutLocked resizes each pane to its laid-out rect. Collapsed panes are
// skipped — they render as a fixed strip that ignores the emulator grid, so
// reflowing their child every toggle would be churn for no visible gain. Caller
// holds mu.
func (m *Manager) applyLayoutLocked(rects []Rect) {
	for i, p := range m.panes {
		if i >= len(rects) {
			break
		}
		if m.collapsed[p.id] {
			continue
		}
		p.Resize(interiorRows(rects[i].H), interiorCols(rects[i].W))
	}
}

// indexOfLocked returns the slice index of the pane with id, or -1. Caller
// holds mu.
func (m *Manager) indexOfLocked(id string) int {
	if id == "" {
		return -1
	}
	for i, p := range m.panes {
		if p.id == id {
			return i
		}
	}
	return -1
}

// indexOfTaskLocked returns the slice index of the pane running taskID, or -1.
// Caller holds mu. An empty taskID never matches (a placeholder/taskless pane is
// not addressable by task).
func (m *Manager) indexOfTaskLocked(taskID string) int {
	if taskID == "" {
		return -1
	}
	for i, p := range m.panes {
		if p.taskID == taskID {
			return i
		}
	}
	return -1
}

// cmdName renders a command for an error message.
func cmdName(cmd *exec.Cmd) string {
	if cmd == nil {
		return "<nil>"
	}
	if len(cmd.Args) > 0 {
		return strings.Join(cmd.Args, " ")
	}
	return cmd.Path
}
