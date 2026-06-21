package pane

import (
	"testing"
	"time"
)

// TestEmulatorQueryDoesNotDeadlock reproduces the spawn-time freeze: a child
// that emits a terminal-capability query (here a cursor-position report request,
// CSI 6 n — exactly what an interactive child like Claude Code sends at startup)
// makes the vt emulator write a reply into its internal io.Pipe. That pipe is
// synchronous and unbuffered, so the write blocks until someone Reads the reply
// out. readLoop performs emu.Write while holding p.mu, so a blocked reply write
// freezes the pane: Render (called from the UI's View) can never take p.mu, and
// the whole Bubble Tea event loop hangs (ctrl+c included).
//
// The fix is a reply pump that drains emu.Read and writes the bytes back to the
// child's master. With it, the query is answered, the pipe drains, and Render
// stays responsive.
func TestEmulatorQueryDoesNotDeadlock(t *testing.T) {
	fake := newFakePTY(t)
	m := NewManager(WithStarter(fake))
	m.Resize(Rect{W: 200, H: 40})
	spawnN(t, m, 1)

	rec := fake.records()[0]

	// The child asks for the cursor position (CSI 6 n).
	if _, err := rec.testEnd.Write([]byte("\x1b[6n")); err != nil {
		t.Fatalf("inject query: %v", err)
	}

	// The emulator's reply (a CPR, CSI row ; col R) must be forwarded back to the
	// child's master — observable on the test end of the socketpair. If the reply
	// is stuck in the emulator's pipe this read never returns data.
	_ = rec.testEnd.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 64)
	n, err := rec.testEnd.Read(buf)
	if err != nil || n == 0 {
		t.Fatalf("emulator reply was not forwarded to the child (deadlocked): n=%d err=%v", n, err)
	}

	// And Render must not block on p.mu (the symptom the user sees as a freeze).
	done := make(chan struct{})
	go func() {
		_ = m.Render(Rect{W: 200, H: 40})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Render blocked on p.mu — pane is deadlocked by the undrained reply pipe")
	}
}
