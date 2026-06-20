package pane

import (
	"os"
	"os/exec"

	"github.com/creack/pty"
)

// Process is the slice of an OS process the pane lifecycle needs: terminate it
// and reap it. It is an interface so manager tests inject a fake process and
// assert lifecycle (start/kill/wait) without forking a real binary — the same
// reason internal/exec.Runner exists.
type Process interface {
	// Kill terminates the process. Safe to call on an already-exited process
	// (it returns an error the caller swallows).
	Kill() error
	// Wait reaps the process, returning its exit error (nil on exit 0).
	Wait() error
	// Pid reports the process id, for logging.
	Pid() int
}

// ptyStarter is the seam between the pane manager and the OS: it allocates a
// PTY, starts a command on it, and resizes it. The default implementation wraps
// creack/pty; tests swap in a fake that returns an in-memory pipe and a fake
// Process, mirroring how internal/exec.Default is swapped via SetRunner.
type ptyStarter interface {
	// Start launches cmd attached to a freshly allocated PTY and returns the
	// master side plus a handle to the started process.
	Start(cmd *exec.Cmd) (master *os.File, proc Process, err error)
	// Setsize issues the window-size ioctl on master so the child reflows
	// (SIGWINCH).
	Setsize(master *os.File, rows, cols uint16) error
}

// DefaultStarter is the production ptyStarter. Like internal/exec.Default it is
// a package var so it could be swapped wholesale; the Manager also accepts a
// per-instance starter via WithStarter for isolated tests.
var DefaultStarter ptyStarter = creackStarter{}

// creackStarter is the real PTY seam, backed by github.com/creack/pty — the
// canonical Go choice the T-036 spike validated.
type creackStarter struct{}

func (creackStarter) Start(cmd *exec.Cmd) (*os.File, Process, error) {
	master, err := pty.Start(cmd)
	if err != nil {
		// A missing binary (exec.ErrNotFound), a PTY-allocation failure, etc.
		// surface here; the caller wraps with context.
		return nil, nil, err
	}
	return master, cmdProcess{cmd}, nil
}

func (creackStarter) Setsize(master *os.File, rows, cols uint16) error {
	return pty.Setsize(master, &pty.Winsize{Rows: rows, Cols: cols})
}

// cmdProcess adapts an *exec.Cmd to the Process interface.
type cmdProcess struct{ cmd *exec.Cmd }

func (p cmdProcess) Kill() error {
	if p.cmd.Process == nil {
		return nil
	}
	return p.cmd.Process.Kill()
}

func (p cmdProcess) Wait() error { return p.cmd.Wait() }

func (p cmdProcess) Pid() int {
	if p.cmd.Process == nil {
		return 0
	}
	return p.cmd.Process.Pid
}
