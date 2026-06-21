package pane

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
)

// EncodeKey re-encodes a Bubble Tea key event into the raw byte sequence a
// terminal application expects on its stdin. This is the load-bearing half of
// the native engine's input path: Bubble Tea decodes the host terminal's input
// stream into tea.KeyMsg; here we reverse that decode and hand the bytes to the
// focused pane's PTY master so the child (claude/vim/…) sees a genuine
// keystroke.
//
// It is the hardened production form of the encoder proven in the T-036 spike.
// A key it does not map returns nil (the caller drops it and traces it under
// the debug gate). The documented gaps — function keys, shift/ctrl-modified
// navigation, xterm modern modifier encoding — are deferred to later tasks per
// the spike's findings.
func EncodeKey(k tea.KeyMsg) []byte {
	switch k.Type {
	// ---- text input -------------------------------------------------------
	case tea.KeyRunes, tea.KeySpace:
		if k.Paste {
			// Bracketed paste: wrap the pasted text so the child receives it as
			// one paste, not a storm of individual keystrokes. The child must
			// have enabled bracketed paste (DECSET 2004); claude does.
			out := append([]byte("\x1b[200~"), []byte(string(k.Runes))...)
			return append(out, "\x1b[201~"...)
		}
		var b []byte
		if k.Alt {
			b = append(b, 0x1b) // ESC prefix = Meta/Alt for printable runes
		}
		if len(k.Runes) > 0 {
			b = append(b, []byte(string(k.Runes))...)
		} else if k.Type == tea.KeySpace {
			b = append(b, ' ')
		}
		return b

	// ---- named control keys ----------------------------------------------
	case tea.KeyEnter:
		return []byte{'\r'} // CR, not LF — what a real terminal sends on Return
	case tea.KeyTab:
		return []byte{'\t'}
	case tea.KeyBackspace:
		return []byte{0x7f} // DEL; most apps treat this as backspace
	case tea.KeyEsc:
		return []byte{0x1b}

	// ---- cursor / navigation keys (CSI sequences) ------------------------
	case tea.KeyUp:
		return withAlt(k, "\x1b[A")
	case tea.KeyDown:
		return withAlt(k, "\x1b[B")
	case tea.KeyRight:
		return withAlt(k, "\x1b[C")
	case tea.KeyLeft:
		return withAlt(k, "\x1b[D")
	case tea.KeyHome:
		return withAlt(k, "\x1b[H")
	case tea.KeyEnd:
		return withAlt(k, "\x1b[F")
	case tea.KeyPgUp:
		return withAlt(k, "\x1b[5~")
	case tea.KeyPgDown:
		return withAlt(k, "\x1b[6~")
	case tea.KeyDelete:
		return withAlt(k, "\x1b[3~")

	default:
		// Bubble Tea sets a Ctrl-key's Type to the literal C0 control byte it
		// represents (KeyCtrlC == 3, KeyCtrlD == 4, …). Those are exactly the
		// bytes the child expects, so any KeyType in the C0 range emits
		// verbatim — one arm covers Ctrl-A..Ctrl-Z and the Ctrl-symbol variants
		// without enumerating them.
		if k.Type >= 0 && k.Type <= 31 {
			return []byte{byte(k.Type)}
		}
		return nil // unmapped (function keys, shift+nav, …) — documented gaps
	}
}

// withAlt prepends an ESC byte to a navigation sequence when Alt is held — the
// simple "ESC-prefix" Meta convention. xterm's modern modifier encoding
// (CSI 1 ; 3 A) is a documented gap, not implemented here.
func withAlt(k tea.KeyMsg, seq string) []byte {
	if k.Alt {
		return append([]byte{0x1b}, seq...)
	}
	return []byte(seq)
}

// EncodeMouse re-encodes a Bubble Tea mouse event into an SGR mouse report
// (CSI < Cb ; Cx ; Cy M/m), the modern, coordinate-unambiguous form modern
// terminals send when a program enables mouse reporting. The button byte
// carries the button index plus motion and modifier bits; coordinates are
// 1-based.
//
// This is the pure, tested primitive mouse routing builds on. The UI's
// click-to-focus handler (T-051) calls it to forward a press on the focused pane
// to its child PTY (Manager.WriteToFocused), the mouse dual of EncodeKey. It
// returns nil for an event it cannot map.
func EncodeMouse(m tea.MouseMsg) []byte {
	cb, ok := mouseButtonCode(m.Button)
	if !ok {
		return nil
	}
	if m.Action == tea.MouseActionMotion {
		cb += 32 // motion bit
	}
	if m.Shift {
		cb += 4
	}
	if m.Alt {
		cb += 8
	}
	if m.Ctrl {
		cb += 16
	}
	// SGR: press/motion terminate with 'M', release with 'm'. Coordinates are
	// 1-based, so add 1 to Bubble Tea's 0-based X/Y.
	final := byte('M')
	if m.Action == tea.MouseActionRelease {
		final = 'm'
	}
	return []byte(fmt.Sprintf("\x1b[<%d;%d;%d%c", cb, m.X+1, m.Y+1, final))
}

// mouseButtonCode maps a Bubble Tea button to its SGR base code, before motion
// and modifier bits are folded in. The wheel buttons live in the 64+ range per
// the SGR spec.
func mouseButtonCode(b tea.MouseButton) (int, bool) {
	switch b {
	case tea.MouseButtonLeft:
		return 0, true
	case tea.MouseButtonMiddle:
		return 1, true
	case tea.MouseButtonRight:
		return 2, true
	case tea.MouseButtonWheelUp:
		return 64, true
	case tea.MouseButtonWheelDown:
		return 65, true
	case tea.MouseButtonWheelLeft:
		return 66, true
	case tea.MouseButtonWheelRight:
		return 67, true
	default:
		return 0, false
	}
}
