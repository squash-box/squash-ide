// Command nativepty is a THROWAWAY spike for T-036. It spawns a command on a
// PTY we own, emulates its terminal output into a cell grid with
// charmbracelet/x/vt, and renders that grid inside a lipgloss-bordered Bubble
// Tea panel beside a stub "task list" box — mimicking squash-ide's real
// two-column layout. Keystrokes are re-encoded and written back to the PTY so
// the focused pane is genuinely interactive.
//
// It exists to answer two go/no-go questions before T-037 commits to the native
// window-management design:
//  1. VT fidelity     — does the emulator faithfully render a full-screen TUI?
//  2. Input round-trip — can decoded keys be re-encoded into the bytes the child
//     expects, such that typing works?
//
// This is not production code. It must not touch internal/ or cmd/. See
// README.md for how to run it and the written finding.
//
// Usage:
//
//	go run ./spike/nativepty [flags] [command [args...]]   (default command: claude)
//	go run ./spike/nativepty vim /etc/hosts
//	go run ./spike/nativepty -debug claude 2>trace.log      (redirect trace off the alt-screen)
//	go run ./spike/nativepty -selftest                      (headless VT-pipeline check, no TTY)
//
// Detach key: ctrl+\ force-quits the spike. Everything else (including Ctrl-C,
// Ctrl-D) is forwarded to the child so interrupt/EOF can be tested.
package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/vt"
	"github.com/creack/pty"
)

const (
	stubWidth = 22 // stub "task list" content width, mimics the pinned TUI column
	gap       = 1  // columns between the two boxes
)

func main() {
	debug := flag.Bool("debug", false, "verbose trace to stderr: 'pty<' bytes in, 'key>' bytes out, 'resize' events. Redirect with 2>trace.log so it doesn't corrupt the alt-screen.")
	selftest := flag.Bool("selftest", false, "run the headless VT-pipeline self-test (no TTY required) and exit. See README → 'Headless self-test'.")
	mouse := flag.Bool("mouse", false, "capture the mouse to enable the click-to-focus probe. Off by default so the child gets a clean terminal.")
	flag.Parse()

	tr := newTracer(*debug)

	if *selftest {
		os.Exit(runSelfTest(tr))
	}

	argv := flag.Args()
	if len(argv) == 0 {
		argv = []string{"claude"}
	}

	if err := run(argv, tr, *mouse); err != nil {
		// Binary-not-found, PTY-allocation failure, etc. land here: a clear
		// message + non-zero exit, never a nil-deref panic.
		fmt.Fprintf(os.Stderr, "nativepty: %v\n", err)
		os.Exit(1)
	}
}

// newTracer returns a logger that writes to stderr when -debug is set, and
// silently discards otherwise.
func newTracer(enabled bool) *log.Logger {
	if !enabled {
		return log.New(io.Discard, "", 0)
	}
	return log.New(os.Stderr, "", log.Ltime|log.Lmicroseconds)
}

func run(argv []string, tr *log.Logger, captureMouse bool) error {
	cmd := exec.Command(argv[0], argv[1:]...)
	ptmx, err := pty.Start(cmd)
	if err != nil {
		// exec.ErrNotFound for a missing binary surfaces here; wrap it so the
		// operator sees what we tried to run.
		return fmt.Errorf("could not start %q on a pty: %w", strings.Join(argv, " "), err)
	}
	defer func() { _ = ptmx.Close() }()

	m := &model{
		argv: argv,
		ptmx: ptmx,
		cmd:  cmd,
		emu:  vt.NewEmulator(80, 24), // resized to the real pane on first WindowSizeMsg
		tr:   tr,
	}

	opts := []tea.ProgramOption{tea.WithAltScreen()}
	if captureMouse {
		opts = append(opts, tea.WithMouseCellMotion())
	}
	p := tea.NewProgram(m, opts...)

	// Pump PTY-master output into the emulator on a goroutine, nudging the
	// program to repaint on each chunk. p.Send is the idiomatic way for an
	// external goroutine to inject messages.
	go m.readLoop(p)

	_, err = p.Run()

	// Best-effort teardown: kill the child if it's still alive and reap it so we
	// don't leak a process. (Spike-grade — production lifecycle is T-037.)
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}
	return err
}

// ---- messages -------------------------------------------------------------

type ptyOutputMsg struct{}            // new bytes arrived from the child
type ptyClosedMsg struct{ err error } // child closed the PTY (exit / error)

// ---- model ----------------------------------------------------------------

type model struct {
	argv []string
	ptmx *os.File
	cmd  *exec.Cmd
	tr   *log.Logger

	// emu is written by readLoop (a separate goroutine) and read by View/Update
	// on the Bubble Tea goroutine, so every access is guarded by mu.
	mu  sync.Mutex
	emu *vt.Emulator

	width, height int    // host terminal size
	paneW, paneH  int    // emulator grid size (right pane interior)
	focusPane     string // "pty" | "tui" — cosmetic + click-to-focus probe
	exited        bool
	exitNote      string
	lastKey       string
}

func (m *model) Init() tea.Cmd { return nil }

// readLoop reads the PTY master until EOF/error, feeding every chunk into the
// emulator and signalling the program to repaint.
func (m *model) readLoop(p *tea.Program) {
	buf := make([]byte, 4096)
	for {
		n, err := m.ptmx.Read(buf)
		if n > 0 {
			m.mu.Lock()
			_, _ = m.emu.Write(buf[:n])
			m.mu.Unlock()
			m.tr.Printf("pty< %4d bytes %s", n, sample(buf[:n]))
			p.Send(ptyOutputMsg{})
		}
		if err != nil {
			m.tr.Printf("pty< EOF/err: %v", err)
			p.Send(ptyClosedMsg{err: err})
			return
		}
	}
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.resize()
		return m, nil

	case tea.KeyMsg:
		// ctrl+\ is the spike's reserved detach key. Everything else — crucially
		// Ctrl-C and Ctrl-D — is forwarded so interrupt/EOF round-trip can be
		// tested against the child.
		if msg.Type == tea.KeyCtrlBackslash {
			return m, tea.Quit
		}
		if m.exited {
			// Child is gone; any key dismisses the prototype.
			return m, tea.Quit
		}
		m.lastKey = msg.String()
		b := encodeKey(msg)
		if len(b) == 0 {
			m.tr.Printf("key> %-12s -> (unmapped, dropped)", msg.String())
			return m, nil
		}
		m.tr.Printf("key> %-12s -> %q", msg.String(), b)
		if _, err := m.ptmx.Write(b); err != nil {
			m.tr.Printf("key> write error: %v", err)
		}
		return m, nil

	case tea.MouseMsg:
		// Basic click-to-focus probe only (the task scopes out real mouse
		// routing into the child). Left-press in the left box focuses the stub,
		// elsewhere focuses the PTY pane.
		if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
			if msg.X < stubWidth+2 {
				m.focusPane = "tui"
			} else {
				m.focusPane = "pty"
			}
			m.tr.Printf("mouse click (%d,%d) -> focus %s", msg.X, msg.Y, m.focusPane)
		}
		return m, nil

	case ptyOutputMsg:
		return m, nil // repaint happens in View; nothing to mutate

	case ptyClosedMsg:
		m.exited = true
		m.exitNote = describeExit(m.cmd, msg.err)
		m.tr.Printf("child exited: %s", m.exitNote)
		return m, nil
	}
	return m, nil
}

// resize recomputes the pane geometry, resizes the emulator grid, and issues
// TIOCSWINSZ to the child so it reflows (SIGWINCH).
func (m *model) resize() {
	m.paneW = m.width - (stubWidth + 2) - gap - 2 // minus left box, gap, right border
	m.paneH = m.height - 2 /*right border*/ - 1   /*status line*/
	if m.paneW < 1 {
		m.paneW = 1
	}
	if m.paneH < 1 {
		m.paneH = 1
	}
	m.mu.Lock()
	m.emu.Resize(m.paneW, m.paneH)
	m.mu.Unlock()
	if err := pty.Setsize(m.ptmx, &pty.Winsize{Rows: uint16(m.paneH), Cols: uint16(m.paneW)}); err != nil {
		m.tr.Printf("resize: pty.Setsize error: %v", err)
	}
	m.tr.Printf("resize: window %dx%d -> pane grid %dx%d", m.width, m.height, m.paneW, m.paneH)
}

func (m *model) View() string {
	if m.width == 0 {
		return "starting…" // before the first WindowSizeMsg
	}

	stub := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor(m.focusPane == "tui")).
		Width(stubWidth).
		Height(m.paneH).
		Render(stubContent(m.argv))

	m.mu.Lock()
	grid := m.emu.Render()
	m.mu.Unlock()
	if m.exited {
		grid = grid + "\n\n  ── " + m.exitNote + " ── (press any key to quit)"
	}

	pane := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor(m.focusPane != "tui")).
		Width(m.paneW).
		Height(m.paneH).
		Render(grid)

	row := lipgloss.JoinHorizontal(lipgloss.Top, stub, strings.Repeat(" ", gap), pane)
	return lipgloss.JoinVertical(lipgloss.Left, row, m.statusLine())
}

func (m *model) statusLine() string {
	st := fmt.Sprintf(" spike:nativepty │ %s │ grid %dx%d │ focus:%s │ last:%s │ ctrl+\\ quit",
		strings.Join(m.argv, " "), m.paneW, m.paneH, m.focusPane, m.lastKey)
	return lipgloss.NewStyle().Faint(true).MaxWidth(m.width).Render(st)
}

// ---- small helpers --------------------------------------------------------

func borderColor(focused bool) lipgloss.Color {
	if focused {
		return lipgloss.Color("69") // bright blue when focused
	}
	return lipgloss.Color("240") // dim grey otherwise
}

func stubContent(argv []string) string {
	return strings.Join([]string{
		"squash-ide",
		"(stub task list)",
		"",
		"> " + argv[0],
		"  T-037",
		"  T-038",
	}, "\n")
}

// sample renders a short, printable preview of a byte slice for the trace log.
func sample(b []byte) string {
	const max = 60
	s := b
	truncated := ""
	if len(s) > max {
		s = s[:max]
		truncated = "…"
	}
	return fmt.Sprintf("%q%s", s, truncated)
}

func describeExit(cmd *exec.Cmd, err error) string {
	if err != nil && err != io.EOF {
		return fmt.Sprintf("process error: %v", err)
	}
	if cmd.ProcessState != nil {
		return fmt.Sprintf("process exited (%s)", cmd.ProcessState.String())
	}
	// PTY hit EOF before Wait recorded the state; reap to find out.
	if cmd.Process != nil {
		_ = cmd.Wait()
		if cmd.ProcessState != nil {
			return fmt.Sprintf("process exited (%s)", cmd.ProcessState.String())
		}
	}
	return "process exited"
}
