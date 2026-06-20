package pane

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// eventually polls cond until it holds or the deadline passes.
func eventually(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("condition not met within timeout: %s", msg)
}

// readN reads up to n bytes from f with a deadline, so a test never hangs.
func readN(t *testing.T, f *os.File, n int) []byte {
	t.Helper()
	_ = f.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, n)
	got := 0
	for got < n {
		m, err := f.Read(buf[got:])
		got += m
		if err != nil {
			break
		}
	}
	return buf[:got]
}

func TestPane_WriteEncodesKeysToMaster(t *testing.T) {
	fake := newFakePTY(t)
	m := NewManager(WithStarter(fake))
	p, _ := m.Spawn(SpawnSpec{Command: fakeCmd("claude")})
	rec := fake.records()[0]

	// Enter must reach the master as CR, not LF.
	if _, err := p.Write(EncodeKey(tea.KeyMsg{Type: tea.KeyEnter})); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got := readN(t, rec.testEnd, 1); !bytes.Equal(got, []byte{'\r'}) {
		t.Errorf("master received %q, want CR", got)
	}

	// A printable rune arrives as its UTF-8 bytes.
	if _, err := p.Write(EncodeKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})); err != nil {
		t.Fatalf("Write rune: %v", err)
	}
	if got := readN(t, rec.testEnd, 1); !bytes.Equal(got, []byte("x")) {
		t.Errorf("master received %q, want 'x'", got)
	}
}

func TestPane_ResizeIssuesSetsize(t *testing.T) {
	fake := newFakePTY(t)
	m := NewManager(WithStarter(fake), WithConstraints(Constraints{MinWidth: 10, Gutter: 1}))
	if _, err := m.Spawn(SpawnSpec{Command: fakeCmd("claude")}); err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	// region W=120,H=40,n=1,gutter=1 -> rect W = 119, H = 40.
	// interior cols = 119-2 = 117; interior rows = 40-2-1 = 37.
	m.Resize(Rect{W: 120, H: 40})

	sizes := fake.resizes()
	if len(sizes) == 0 {
		t.Fatal("Resize issued no setsize ioctl")
	}
	last := sizes[len(sizes)-1]
	if last.rows != 37 || last.cols != 117 {
		t.Errorf("setsize = %dx%d (rows x cols), want 37x117", last.rows, last.cols)
	}
}

func TestPane_RenderShowsChildOutputAndBadge(t *testing.T) {
	fake := newFakePTY(t)
	m := NewManager(WithStarter(fake), WithConstraints(Constraints{MinWidth: 10, Gutter: 1}))
	region := Rect{W: 50, H: 12}
	if _, err := m.Spawn(SpawnSpec{Command: fakeCmd("claude"), TaskID: "T-037"}); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	m.Resize(region)
	rec := fake.records()[0]

	if _, err := rec.testEnd.Write([]byte("HELLO")); err != nil {
		t.Fatalf("inject output: %v", err)
	}
	eventually(t, func() bool { return strings.Contains(m.Render(region), "HELLO") },
		"child output should appear in the rendered pane")

	out := m.Render(region)
	if !strings.Contains(out, "WORKING") {
		t.Errorf("rendered pane missing the WORKING badge:\n%s", out)
	}
	if !strings.Contains(out, "T-037") {
		t.Errorf("rendered pane missing the task id:\n%s", out)
	}
}

func TestPane_ChildExitMarksDeadAndKeepsPane(t *testing.T) {
	fake := newFakePTY(t)
	m := NewManager(WithStarter(fake))
	p, _ := m.Spawn(SpawnSpec{Command: fakeCmd("claude")})
	rec := fake.records()[0]

	// Simulate the child exiting: close the far end so the read loop sees EOF.
	_ = rec.testEnd.Close()

	eventually(t, p.IsDead, "pane should transition to dead on child exit")
	if p.State() != StateDead {
		t.Errorf("state = %q after child exit, want %q", p.State(), StateDead)
	}
	// Remain-on-exit: the pane is NOT removed from the manager.
	if got := m.Panes(); len(got) != 1 {
		t.Errorf("Panes() = %d after child exit, want 1 (remain-on-exit)", len(got))
	}
}

func TestPane_CloseMidOutputNoPanic(t *testing.T) {
	fake := newFakePTY(t)
	m := NewManager(WithStarter(fake))
	p, _ := m.Spawn(SpawnSpec{Command: fakeCmd("claude")})
	rec := fake.records()[0]

	// Flood the master from another goroutine, then close mid-stream. The race
	// between the read loop and Close is the primary -race hazard; this must not
	// panic and Close must return.
	stop := make(chan struct{})
	go func() {
		blob := bytes.Repeat([]byte("x"), 256)
		for {
			select {
			case <-stop:
				return
			default:
				if _, err := rec.testEnd.Write(blob); err != nil {
					return
				}
			}
		}
	}()

	if err := m.Close(p.ID()); err != nil {
		t.Fatalf("Close mid-output: %v", err)
	}
	close(stop)

	if got := m.Panes(); len(got) != 0 {
		t.Errorf("Panes() = %d after Close, want 0", len(got))
	}
}

func TestPane_DoubleCloseIsSafe(t *testing.T) {
	fake := newFakePTY(t)
	m := NewManager(WithStarter(fake))
	p, _ := m.Spawn(SpawnSpec{Command: fakeCmd("claude")})
	if err := p.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := p.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

// TestDebugLogging exercises the SQUASH_DEBUG gate: lifecycle lines are emitted
// at debug level only when the gate is on, and are routable to a buffer.
func TestDebugLogging(t *testing.T) {
	buf := captureLogs(t, true)

	fake := newFakePTY(t)
	m := NewManager(WithStarter(fake))
	p, _ := m.Spawn(SpawnSpec{Command: fakeCmd("claude"), TaskID: "T-037"})
	if err := m.Close(p.ID()); err != nil { // joins the read goroutine before we read buf
		t.Fatalf("Close: %v", err)
	}

	got := buf.String()
	for _, want := range []string{"manager: spawned", "pane pane-1: closed"} {
		if !strings.Contains(got, want) {
			t.Errorf("debug log missing %q; got:\n%s", want, got)
		}
	}
}

// captureLogs redirects package logging to a buffer and sets the debug gate,
// restoring both on cleanup. Tests run sequentially so the package vars are not
// contended across tests.
func captureLogs(t *testing.T, withDebug bool) *bytes.Buffer {
	t.Helper()
	buf := &bytes.Buffer{}
	logMu.Lock()
	prevOut, prevDebug := logOut, debug
	logOut, debug = buf, withDebug
	logMu.Unlock()
	t.Cleanup(func() {
		logMu.Lock()
		logOut, debug = prevOut, prevDebug
		logMu.Unlock()
	})
	return buf
}
