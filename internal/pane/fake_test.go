package pane

import (
	"os"
	"os/exec"
	"sync"
	"syscall"
	"testing"
)

// fakePTY is a test ptyStarter. It never forks: each Start allocates a
// bidirectional unix socketpair and hands one end to the pane as its "master".
// The test holds the other end to inject child output (the pane reads it) and
// to observe the keystrokes the pane writes — exactly the record/replay role
// internal/testutil/fakerunner plays for the exec seam. It also records the
// commands started and the resize ioctls issued so lifecycle can be asserted.
type fakePTY struct {
	t *testing.T

	mu       sync.Mutex
	started  []*startRecord
	setsizes []winsize
	startErr error // when non-nil, Start fails (binary-not-found path)
}

type startRecord struct {
	cmd     *exec.Cmd
	testEnd *os.File // test writes child output here / reads pane keystrokes here
	paneEnd *os.File // handed to the pane as its master
	proc    *fakeProc
}

type winsize struct{ rows, cols uint16 }

func newFakePTY(t *testing.T) *fakePTY { return &fakePTY{t: t} }

// failNextStart makes the next Start return err, simulating a pty-start failure.
func (f *fakePTY) failNextStart(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.startErr = err
}

func (f *fakePTY) Start(cmd *exec.Cmd) (*os.File, Process, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.startErr != nil {
		err := f.startErr
		f.startErr = nil
		return nil, nil, err
	}
	paneEnd, testEnd := socketPair(f.t)
	// Killing the process must end the pane's read, the way killing a real child
	// EOFs the PTY master: closing the peer (test) end makes paneEnd's read
	// return EOF. fakeProc owns that close so Pane.Close()'s proc.Kill() unblocks
	// readLoop even when the socketpair fd is in blocking mode — there master.Close
	// alone would not wake a thread parked in read(2).
	rec := &startRecord{cmd: cmd, testEnd: testEnd, paneEnd: paneEnd, proc: &fakeProc{peer: testEnd}}
	f.started = append(f.started, rec)
	return paneEnd, rec.proc, nil
}

func (f *fakePTY) Setsize(_ *os.File, rows, cols uint16) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.setsizes = append(f.setsizes, winsize{rows, cols})
	return nil
}

// records returns a snapshot of the started commands.
func (f *fakePTY) records() []*startRecord {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*startRecord(nil), f.started...)
}

// startCount reports how many commands were started.
func (f *fakePTY) startCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.started)
}

// resizes returns a snapshot of the recorded window sizes.
func (f *fakePTY) resizes() []winsize {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]winsize(nil), f.setsizes...)
}

// fakeProc is a Process that records Kill/Wait without touching the OS. It
// holds the peer (test) end of the socketpair so Kill can close it, modelling a
// real child's death EOFing the PTY master and thereby unblocking readLoop.
type fakeProc struct {
	mu     sync.Mutex
	killed bool
	waited bool
	peer   *os.File
}

func (p *fakeProc) Kill() error {
	p.mu.Lock()
	p.killed = true
	peer := p.peer
	p.mu.Unlock()
	if peer != nil {
		_ = peer.Close() // EOFs the pane's read; double-close at cleanup is ignored
	}
	return nil
}

func (p *fakeProc) Wait() error {
	p.mu.Lock()
	p.waited = true
	p.mu.Unlock()
	return nil
}

func (p *fakeProc) Pid() int { return 4242 }

func (p *fakeProc) wasKilled() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.killed
}

// socketPair returns a connected, bidirectional pair of *os.File. Writing to
// one end is readable on the other (and vice-versa, on independent streams), so
// the pane can both read injected "child" output and have its keystrokes
// captured — without a real PTY device or a forked process. Both ends are
// closed on test cleanup.
func socketPair(t *testing.T) (a, b *os.File) {
	t.Helper()
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		t.Fatalf("socketpair: %v", err)
	}
	a = os.NewFile(uintptr(fds[0]), "pane-master")
	b = os.NewFile(uintptr(fds[1]), "test-end")
	t.Cleanup(func() {
		_ = a.Close()
		_ = b.Close()
	})
	return a, b
}

// fakeCmd builds a harmless *exec.Cmd carrying the given argv for assertions.
// It is never actually started (the fake PTY ignores it beyond recording).
func fakeCmd(argv ...string) *exec.Cmd {
	if len(argv) == 0 {
		argv = []string{"claude"}
	}
	return exec.Command(argv[0], argv[1:]...)
}
