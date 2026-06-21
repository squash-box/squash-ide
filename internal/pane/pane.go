// Package pane is the native window-management core: it owns the PTYs of
// spawned task processes and renders them, the job tmux does today.
//
// Ownership model — who owns what, and why it is a deliberate inversion of the
// tmux design (internal/tmux):
//
//   - A Pane wraps one creack/pty master running a child (claude), a
//     charmbracelet/x/vt Emulator that turns the child's raw byte stream into a
//     cell grid, and the per-pane metadata (task id, title, project, state)
//     tmux used to hold as pane options. A background read goroutine pumps the
//     master into the emulator; the Pane is rendered as a lipgloss-bordered box
//     with a painted status badge — the border tmux drew via
//     pane-border-format, now ours.
//   - A Manager owns the ordered set of Panes, which one has focus, and a
//     pluggable layout Strategy that computes per-pane geometry. Manager.Spawn
//     mirrors the lifecycle ordering of spawner.runTmux (create → tag →
//     remain-on-exit → re-tile/reject → keep focus off the new pane) without
//     shelling out to tmux.
//
// Two seams keep the package testable and crash-safe, copied from patterns the
// codebase already trusts: the layout math is a pure function behind a Strategy
// interface (the internal/tmux/layout.go precedent), and every PTY/process
// side-effect goes through ptyStarter (the internal/exec.Runner precedent) so
// tests inject a fake PTY and never fork. Transient render/resize failures are
// swallowed and warn-logged per the [[T-031]] best-effort UX-side-effect idiom,
// so a glitch can never crash the TUI.
//
// The package is wired into the UI in T-038; until then it is unreferenced.
package pane

import (
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/vt"
)

// Pane states. These reuse the exact strings internal/status writes and
// spawner.TaskBorderFormatWithState / ui.activeBadge render, so the native
// engine's badge agrees with the task-list badge (the [[T-023]] invariant) and
// the status-file pipeline is untouched. StateDead is local to the native
// engine: a child that has exited but whose final output we keep on screen
// (the [[T-035]] remain-on-exit analogue).
const (
	StateWorking       = "working"
	StateIdle          = "idle"
	StateInputRequired = "input_required"
	StateTesting       = "testing"
	StateDead          = "dead"
)

// Box chrome dimensions, in cells. A pane's bounding box spends one column on
// each side for the lipgloss border, one row top and bottom likewise, and one
// interior row on the status header. The emulator grid fills what remains.
const (
	borderCols = 2 // left + right border
	borderRows = 2 // top + bottom border
	headerRows = 1 // status badge/title line inside the border
)

// Pane owns one child process's PTY, its VT emulator, and its metadata.
type Pane struct {
	id      string
	taskID  string
	title   string
	project string

	master *os.File
	proc   Process

	// mu guards every field below it: the emulator is written by the read
	// goroutine and read by Render/Resize on another goroutine, and state/dead/
	// closed are read and written from both. This is the package's primary race
	// hazard, asserted under -race.
	mu     sync.Mutex
	emu    *vt.Emulator
	rows   int
	cols   int
	state  string
	dead   bool
	closed bool

	setsize   func(rows, cols uint16) error // window-size ioctl via the manager's seam
	notify    func()                        // repaint hook, invoked on output and state change
	done      chan struct{}                 // closed when the read goroutine exits
	replyDone chan struct{}                 // closed when the reply-pump goroutine exits

	// replyW is the write end of the emulator's reply pipe (emu.InputPipe). The
	// emulator writes query replies into it and replyPump drains the read end;
	// closing it EOFs that read so the pump exits at teardown. We close this
	// directly rather than emu.Close() because emu.Close mutates an unsynchronised
	// `closed` flag that emu.Read also reads — a data race the pump would trip.
	replyW io.Closer
}

// newPane builds a Pane around an already-started master/process and launches
// its read goroutine. cols/rows size the initial emulator grid; they are
// resized to the laid-out geometry on the first Manager.Resize. setsize issues
// the window-size ioctl through the manager's ptyStarter so tests exercise the
// fake seam instead of a real ioctl.
func newPane(id, taskID, title, project string, master *os.File, proc Process, cols, rows int, setsize func(rows, cols uint16) error, notify func()) *Pane {
	if cols < 1 {
		cols = 1
	}
	if rows < 1 {
		rows = 1
	}
	if notify == nil {
		notify = func() {}
	}
	if setsize == nil {
		setsize = func(uint16, uint16) error { return nil }
	}
	emu := vt.NewEmulator(cols, rows)
	p := &Pane{
		id:        id,
		taskID:    taskID,
		title:     title,
		project:   project,
		master:    master,
		proc:      proc,
		emu:       emu,
		cols:      cols,
		rows:      rows,
		state:     StateWorking,
		setsize:   setsize,
		notify:    notify,
		done:      make(chan struct{}),
		replyDone: make(chan struct{}),
	}
	// The emulator's reply pipe write end (an *io.PipeWriter). Closing it at
	// teardown EOFs replyPump's Read without touching emu's racy closed flag.
	if c, ok := emu.InputPipe().(io.Closer); ok {
		p.replyW = c
	}
	go p.readLoop()
	go p.replyPump()
	return p
}

// ID returns the pane's stable identifier.
func (p *Pane) ID() string { return p.id }

// TaskID returns the task this pane is running, if any.
func (p *Pane) TaskID() string { return p.taskID }

// Title returns the pane's human title.
func (p *Pane) Title() string { return p.title }

// Project returns the pane's project label.
func (p *Pane) Project() string { return p.project }

// State returns the pane's current lifecycle state (one of the State* values).
func (p *Pane) State() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.state
}

// IsDead reports whether the child has exited (or the pane was closed).
func (p *Pane) IsDead() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.dead
}

// SetState updates the pane's lifecycle state and requests a repaint. A dead
// pane's state is frozen — a late status write must not resurrect an exited
// child's badge.
func (p *Pane) SetState(state string) {
	p.mu.Lock()
	if p.dead {
		p.mu.Unlock()
		return
	}
	changed := p.state != state
	p.state = state
	p.mu.Unlock()
	if changed {
		debugf("pane %s: state -> %s", p.id, state)
		p.notify()
	}
}

// Write sends raw bytes (typically from EncodeKey) to the child's PTY master.
// It returns an error on a closed pane rather than panicking on a nil/closed
// file.
func (p *Pane) Write(b []byte) (int, error) {
	p.mu.Lock()
	closed := p.closed
	p.mu.Unlock()
	if closed {
		return 0, fmt.Errorf("pane %s: write to closed pane", p.id)
	}
	return p.master.Write(b)
}

// Resize sizes the emulator grid and issues the window-size ioctl so the child
// reflows (SIGWINCH). rows/cols are the emulator (interior) dimensions, not the
// bounding box — Manager.Resize derives them from the laid-out rect. The ioctl
// is best-effort: a transient failure is warn-logged and swallowed so a resize
// glitch never crashes the TUI ([[T-031]] idiom).
func (p *Pane) Resize(rows, cols int) {
	if rows < 1 {
		rows = 1
	}
	if cols < 1 {
		cols = 1
	}
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.rows, p.cols = rows, cols
	p.emu.Resize(cols, rows)
	setsize := p.setsize
	p.mu.Unlock()

	if err := setsize(uint16(rows), uint16(cols)); err != nil {
		warnf("pane %s: resize ioctl failed (swallowed): %v", p.id, err)
	}
	debugf("pane %s: resize -> %d rows x %d cols", p.id, rows, cols)
}

// Render draws the pane as a lipgloss-bordered box of exactly width×height
// cells: a status header (the painted equivalent of tmux's pane-border-format)
// above the emulator grid. focused selects the border color. blink is the
// animation phase (T-040): an input_required pane's badge pulses on it, riding
// the Manager's tick rather than a timer of its own. width/height are the
// bounding box from the layout Strategy.
func (p *Pane) Render(width, height int, focused, blink bool) string {
	cols := interiorCols(width)
	rows := interiorRows(height)

	p.mu.Lock()
	grid := p.emu.Render()
	state := p.state
	p.mu.Unlock()

	header := p.headerLine(state, cols, blink)
	content := lipgloss.JoinVertical(lipgloss.Left, header, grid)

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor(state, focused, blink)).
		Width(cols).
		Height(rows + headerRows).
		Render(content)
}

// RenderStrip draws a collapsed pane as a thin strip — a tall sliver under the
// columns axis, a short bar under the rows axis — showing just its state badge
// and task id, not its emulator grid. Its sibling panes reflow into the space it
// gives up (the Manager carves the strip out before arranging the rest). The
// border keeps the pane's state colour so a collapsed input_required pane still
// reads at a glance.
func (p *Pane) RenderStrip(width, height int, axis layoutAxis, focused, blink bool) string {
	cols := interiorCols(width)
	rows := interiorRows(height) + headerRows
	if rows < 1 {
		rows = 1
	}

	p.mu.Lock()
	state := p.state
	p.mu.Unlock()

	label := p.taskID
	if label == "" {
		label = p.id
	}

	var content string
	if axis == axisRows {
		// Short, wide bar: badge glyph + id + title on one line.
		line := stateGlyph(state) + " " + label
		if p.title != "" {
			line += " — " + p.title
		}
		content = lipgloss.NewStyle().MaxWidth(cols).Render(line)
	} else {
		// Tall, narrow sliver: the glyph then the id stacked one rune per row.
		lines := []string{stateGlyph(state)}
		for _, r := range label {
			if len(lines) >= rows {
				break
			}
			lines = append(lines, string(r))
		}
		content = lipgloss.JoinVertical(lipgloss.Center, lines...)
	}

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor(state, focused, blink)).
		Foreground(stateColor(state)).
		Width(cols).
		Height(rows).
		Render(content)
}

// headerLine renders the status badge + task id + title, the painted lipgloss
// analogue of spawner.TaskBorderFormatWithState. width is the interior width to
// fit within; blink animates the input_required badge.
func (p *Pane) headerLine(state string, width int, blink bool) string {
	title := p.title
	if len(title) > 30 {
		title = title[:27] + "..."
	}
	badge := stateBadge(state, blink)
	line := badge
	if p.taskID != "" {
		line += " " + headerMetaStyle.Render(p.taskID)
	}
	if title != "" {
		line += " " + headerTitleStyle.Render(title)
	}
	return lipgloss.NewStyle().MaxWidth(width).Render(line)
}

// readLoop pumps the PTY master into the emulator until EOF/error, then marks
// the pane dead (remain-on-exit: we keep the final frame until explicit Close).
// It owns p.done, closing it on exit so Close can wait the goroutine out before
// anything else touches the emulator — the close-mid-output race guard.
func (p *Pane) readLoop() {
	defer close(p.done)
	buf := make([]byte, 4096)
	for {
		n, err := p.master.Read(buf)
		if n > 0 {
			p.mu.Lock()
			_, _ = p.emu.Write(buf[:n])
			p.mu.Unlock()
			p.notify()
		}
		if err != nil {
			p.markDead()
			debugf("pane %s: child exited (read end: %v)", p.id, err)
			p.notify()
			return
		}
	}
}

// replyPump drains the emulator's reply stream and writes it back to the child's
// PTY master. A vt emulator answers terminal-capability queries — Device
// Attributes (CSI c / CSI > c), cursor-position / Device Status reports (CSI 6 n),
// OSC color queries — by writing the reply into an internal, *synchronous*
// io.Pipe (vt's emulator.go). That write BLOCKS until the reply is read out, and
// readLoop feeds the emulator under p.mu: an undrained reply therefore freezes
// readLoop mid-write while holding p.mu, which deadlocks Render (and so the whole
// Bubble Tea event loop — keyboard input and ctrl+c included). An interactive
// child like Claude Code emits these queries at startup, so the freeze hit the
// instant a task was spawned. Draining here both unblocks the writer and
// correctly delivers the answer to the child. It exits when Pane.Close closes the
// reply pipe (p.replyW), EOFing the reply stream.
func (p *Pane) replyPump() {
	defer close(p.replyDone)
	buf := make([]byte, 256)
	for {
		n, err := p.emu.Read(buf)
		if n > 0 {
			_, _ = p.master.Write(buf[:n]) // best-effort; a closed master just errors
		}
		if err != nil {
			return
		}
	}
}

// markDead freezes the pane in StateDead. Idempotent.
func (p *Pane) markDead() {
	p.mu.Lock()
	p.dead = true
	p.state = StateDead
	p.mu.Unlock()
}

// Close terminates the child, closes the master, and waits for the read
// goroutine to drain so no goroutine touches the emulator afterwards. Safe to
// call more than once, and safe to call after the child has already exited.
func (p *Pane) Close() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	p.dead = true
	p.state = StateDead
	proc, master := p.proc, p.master
	p.mu.Unlock()

	if proc != nil {
		_ = proc.Kill() // best-effort; already-exited returns an error we ignore
	}
	if master != nil {
		_ = master.Close() // unblocks the read goroutine
	}
	if p.replyW != nil {
		_ = p.replyW.Close() // EOFs the reply stream so replyPump exits
	}
	<-p.done      // wait for readLoop to stop before returning
	<-p.replyDone // and the reply pump, so no goroutine touches the emulator after
	if proc != nil {
		_ = proc.Wait() // reap so we don't leak a zombie
	}
	debugf("pane %s: closed", p.id)
	return nil
}

// interiorCols / interiorRows convert a bounding-box dimension to the emulator
// grid dimension, reserving the border (and, for rows, the header line). They
// floor at 1 so a degenerate box never produces a zero-size grid.
func interiorCols(boxW int) int {
	c := boxW - borderCols
	if c < 1 {
		return 1
	}
	return c
}

func interiorRows(boxH int) int {
	r := boxH - borderRows - headerRows
	if r < 1 {
		return 1
	}
	return r
}

// --- badge styling ----------------------------------------------------------
//
// Colors and glyphs are copied verbatim from internal/ui/styles.go and
// spawner.TaskBorderFormatWithState so the native pane border and the task-list
// badge stay in visual agreement.
var (
	colorWorking    = lipgloss.Color("78")  // green
	colorIdle       = lipgloss.Color("214") // amber
	colorNeeds      = lipgloss.Color("204") // pink
	colorNeedsBlink = lipgloss.Color("231") // near-white, the pink badge's pulse
	colorTesting    = lipgloss.Color("39")  // cyan
	colorDead       = lipgloss.Color("240") // grey
	colorFocus      = lipgloss.Color("69")  // bright blue, focused border
	colorBadgeFg    = lipgloss.Color("235") // dark text on light badge bg

	badgeBase = lipgloss.NewStyle().Foreground(colorBadgeFg).Bold(true).Padding(0, 1)

	badgeWorkingStyle = badgeBase.Background(colorWorking)
	badgeIdleStyle    = badgeBase.Background(colorIdle)
	badgeNeedsStyle   = badgeBase.Background(colorNeeds)
	badgeTestingStyle = badgeBase.Background(colorTesting)
	badgeDeadStyle    = badgeBase.Background(colorDead)

	// badgeNeedsBlinkStyle is the "on" phase of the input_required pulse: the
	// pink badge brightens to near-white so the eye is drawn to the pane that
	// needs the user. The "off" phase is the normal pink badgeNeedsStyle, so the
	// badge alternates on the Manager's tick — richer than tmux's static
	// pane-border-format string ([[T-024]]).
	badgeNeedsBlinkStyle = badgeBase.Background(colorNeedsBlink).Foreground(colorNeeds)

	headerMetaStyle  = lipgloss.NewStyle().Bold(true)
	headerTitleStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("243"))

	// Tab-strip chips (Tabs layout). The active tab and a pulsing input_required
	// tab stand out from the dim inactive ones.
	tabInactiveStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("243")).Padding(0, 1)
	tabActiveStyle   = lipgloss.NewStyle().Foreground(colorBadgeFg).Background(colorFocus).Bold(true).Padding(0, 1)
	tabAlertStyle    = lipgloss.NewStyle().Foreground(colorBadgeFg).Background(colorNeeds).Bold(true).Padding(0, 1)
)

// stateBadge renders the colored status badge for a state, matching
// ui.activeBadge's glyphs exactly. For input_required the badge pulses with the
// blink phase (T-040): the "on" phase brightens the pink so it animates rather
// than sitting static like the tmux border.
func stateBadge(state string, blink bool) string {
	switch state {
	case StateIdle:
		return badgeIdleStyle.Render("○ IDLE")
	case StateInputRequired:
		if blink {
			return badgeNeedsBlinkStyle.Render("⚠ INPUT REQUIRED")
		}
		return badgeNeedsStyle.Render("⚠ INPUT REQUIRED")
	case StateTesting:
		return badgeTestingStyle.Render("⧖ TESTING")
	case StateDead:
		return badgeDeadStyle.Render("✗ EXITED")
	default:
		return badgeWorkingStyle.Render("● WORKING")
	}
}

// stateGlyph is the bare status glyph (no background), used in the tab strip and
// collapsed strips where a full badge won't fit. It matches stateBadge's glyphs.
func stateGlyph(state string) string {
	switch state {
	case StateIdle:
		return "○"
	case StateInputRequired:
		return "⚠"
	case StateTesting:
		return "⧖"
	case StateDead:
		return "✗"
	default:
		return "●"
	}
}

// stateColor is the foreground colour for a state, used to tint collapsed strips.
func stateColor(state string) lipgloss.Color {
	switch state {
	case StateIdle:
		return colorIdle
	case StateInputRequired:
		return colorNeeds
	case StateTesting:
		return colorTesting
	case StateDead:
		return colorDead
	default:
		return colorWorking
	}
}

// borderColor picks a box border colour: blue when focused, else the state
// colour for an input_required pane (so it stands out even unfocused, pulsing
// with blink), else dim grey.
func borderColor(state string, focused, blink bool) lipgloss.Color {
	switch {
	case focused:
		return colorFocus
	case state == StateInputRequired:
		if blink {
			return colorNeedsBlink
		}
		return colorNeeds
	default:
		return lipgloss.Color("240")
	}
}
