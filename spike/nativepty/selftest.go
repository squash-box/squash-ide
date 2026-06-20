package main

import (
	"fmt"
	"image/color"
	"log"
	"os/exec"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/vt"
	"github.com/creack/pty"
)

// runSelfTest drives the PTY → emulator pipeline headlessly (no TTY, no Bubble
// Tea) so the load-bearing VT-fidelity claims can be checked in CI-like
// conditions and produce real evidence, not just "it compiles". It spawns short
// non-interactive children that emit known escape sequences, reads each to EOF,
// feeds the bytes through a fresh emulator, and asserts the resulting cell grid.
//
// It also exercises two of the exception paths headlessly: a child that exits
// (clean EOF on the master) and a binary that isn't on PATH (start error, no
// panic).
//
// Returns 0 if every check passes, 1 otherwise.
func runSelfTest(tr *log.Logger) int {
	t := &checks{tr: tr}

	// --- Phase A: primary-screen fidelity (cursor addressing, 256/truecolor,
	// wide glyphs) -----------------------------------------------------------
	//
	//   row 2 col 3 : "HELLO"            (CUP cursor addressing)
	//   row 4 col 1 : 256-colour green   (SGR 38;5;46)
	//   row 5 col 1 : truecolour crimson (SGR 38;2;220;20;60)
	//   row 6 col 1 : "界"               (East-Asian wide glyph, width 2)
	primary := `\033[2;3HHELLO` +
		`\033[4;1H\033[38;5;46mG256\033[0m` +
		`\033[5;1H\033[38;2;220;20;60mCRIM\033[0m` +
		`\033[6;1H\347\225\214X`
	out, err := drainPTY([]string{"sh", "-c", "printf '" + primary + "'"})
	if err != nil {
		t.fail("phaseA spawn", err.Error())
	} else {
		emu := vt.NewEmulator(80, 24)
		_, _ = emu.Write(out)

		t.eq("cursor addressing: 'HELLO' at row2col3", readRow(emu, 1, 2, 5), "HELLO")

		t.true("256-colour: green channel dominant at row4", greenDominant(cellFg(emu, 0, 3)))
		t.true("truecolour: red channel dominant at row5", redDominant(cellFg(emu, 0, 4)))

		wide := emu.CellAt(0, 5)
		t.true("wide glyph: '界' present at row6", wide != nil && wide.Content == "界")
		t.true("wide glyph: occupies width 2", wide != nil && wide.Width == 2)
	}

	// --- Phase B: alt-screen handling (load-bearing for vim/htop/claude) -----
	out, err = drainPTY([]string{"sh", "-c", `printf '\033[?1049hALT'`})
	if err != nil {
		t.fail("phaseB spawn", err.Error())
	} else {
		emu := vt.NewEmulator(80, 24)
		_, _ = emu.Write(out)
		t.true("alt-screen: IsAltScreen() true after DECSET 1049", emu.IsAltScreen())
		t.eq("alt-screen: content rendered on alt buffer", readRow(emu, 0, 0, 3), "ALT")
	}

	// --- Phase C: exception paths --------------------------------------------
	// Child that exits => clean EOF on the master, no hang/panic.
	if _, err := drainPTY([]string{"sh", "-c", "exit 0"}); err != nil {
		t.fail("child-exit path", "drain returned error: "+err.Error())
	} else {
		t.pass("child-exit path: PTY read reached EOF without hanging")
	}
	// Binary not on PATH => start error surfaces, not a nil-deref panic.
	if _, err := drainPTY([]string{"squash-ide-no-such-binary-xyz"}); err == nil {
		t.fail("binary-not-found path", "expected a start error, got nil")
	} else {
		t.pass("binary-not-found path: start error surfaced cleanly")
	}

	fmt.Printf("\nself-test: %d passed, %d failed\n", t.passed, t.failed)
	if t.failed > 0 {
		return 1
	}
	return 0
}

// drainPTY starts argv on a PTY, reads the master until the child closes it, and
// returns everything the child emitted. A closed PTY surfaces as EOF or EIO
// depending on platform; both end the read loop cleanly.
func drainPTY(argv []string) ([]byte, error) {
	cmd := exec.Command(argv[0], argv[1:]...)
	ptmx, err := pty.Start(cmd)
	if err != nil {
		return nil, err
	}
	defer func() { _ = ptmx.Close() }()

	var out []byte
	buf := make([]byte, 4096)
	for {
		n, rerr := ptmx.Read(buf)
		out = append(out, buf[:n]...)
		if rerr != nil {
			break
		}
	}
	_ = cmd.Wait()
	return out, nil
}

// readRow reads n cells from the emulator starting at (x,y) and joins their
// content into a string.
func readRow(emu *vt.Emulator, y, x, n int) string {
	s := ""
	for i := 0; i < n; i++ {
		c := emu.CellAt(x+i, y)
		if c == nil {
			break
		}
		s += c.Content
	}
	return s
}

func cellFg(emu *vt.Emulator, x, y int) color.Color {
	c := emu.CellAt(x, y)
	if c == nil {
		return nil
	}
	return c.Style.Fg
}

func rgb(c color.Color) (r, g, b uint32) {
	if c == nil {
		return 0, 0, 0
	}
	r, g, b, _ = c.RGBA()
	return r, g, b
}

func redDominant(c color.Color) bool   { r, g, b := rgb(c); return c != nil && r > g && r > b }
func greenDominant(c color.Color) bool { r, g, b := rgb(c); return c != nil && g > r && g > b }

// checks is a tiny pass/fail accumulator for the self-test.
type checks struct {
	tr             *log.Logger
	passed, failed int
}

func (t *checks) pass(name string) {
	t.passed++
	fmt.Printf("PASS %s\n", name)
}

func (t *checks) fail(name, detail string) {
	t.failed++
	fmt.Printf("FAIL %s — %s\n", name, detail)
}

func (t *checks) true(name string, ok bool) {
	if ok {
		t.pass(name)
	} else {
		t.fail(name, "assertion false")
	}
}

func (t *checks) eq(name, got, want string) {
	if got == want {
		t.pass(name)
	} else {
		t.fail(name, fmt.Sprintf("got %q, want %q", got, want))
	}
}

// uv is imported so the spike compiles against the same cell type the emulator
// returns; referenced here to keep the import even if assertions change.
var _ = uv.Cell{}
