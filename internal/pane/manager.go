package pane

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"

	"github.com/charmbracelet/lipgloss"
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
		constraints: Constraints{MinWidth: 20, Gutter: 1},
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
	n := len(m.panes) + 1

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
	// first Resize tile.
	if region.W > 0 && region.H > 0 {
		rects, lerr := m.strategy.Arrange(region, n, m.constraints)
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
		if rects, lerr := m.strategy.Arrange(region, n, m.constraints); lerr == nil {
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
	_, err := m.strategy.Arrange(region, len(m.panes)+1, m.constraints)
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
	rects, err := m.strategy.Arrange(region, n, m.constraints)
	if err != nil {
		warnf("manager: resize layout rejected (swallowed): %v", err)
		return
	}
	m.applyLayoutLocked(rects)
	debugf("manager: resized to %dx%d — %d pane(s)", region.W, region.H, n)
}

// Render lays the panes out across region and concatenates their rendered
// boxes left to right (with the gutter between them). A layout failure yields
// an empty string rather than a panic.
func (m *Manager) Render(region Rect) string {
	m.mu.Lock()
	panes := append([]*Pane(nil), m.panes...)
	focusID := m.focusID
	gutter := m.constraints.Gutter
	strategy := m.strategy
	constraints := m.constraints
	m.mu.Unlock()

	if len(panes) == 0 {
		return ""
	}
	rects, err := strategy.Arrange(region, len(panes), constraints)
	if err != nil {
		warnf("manager: render layout rejected (swallowed): %v", err)
		return ""
	}

	parts := make([]string, 0, len(panes)*2)
	gap := strings.Repeat(" ", gutter)
	for i, p := range panes {
		if gutter > 0 {
			parts = append(parts, gap) // leading separator before each pane
		}
		parts = append(parts, p.Render(rects[i].W, rects[i].H, p.id == focusID))
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, parts...)
}

// Panes returns a snapshot copy of the pane set, in order.
func (m *Manager) Panes() []*Pane {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]*Pane(nil), m.panes...)
}

// applyLayoutLocked resizes each pane to its laid-out rect. Caller holds mu.
func (m *Manager) applyLayoutLocked(rects []Rect) {
	for i, p := range m.panes {
		if i >= len(rects) {
			break
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
