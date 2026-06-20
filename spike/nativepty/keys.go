package main

import (
	tea "github.com/charmbracelet/bubbletea"
)

// encodeKey re-encodes a Bubble Tea key event back into the raw byte sequence a
// terminal application expects on its stdin. This is the load-bearing half of
// the "input round-trip" the spike exists to prove: Bubble Tea decodes the
// terminal's input stream into tea.KeyMsg; here we reverse that decode and write
// the bytes to the PTY master so the child process (claude/vim/htop) sees a
// genuine keystroke.
//
// It is deliberately NOT complete — completeness is T-037's job. The covered set
// is the one the spike's go/no-go observations exercise; everything else returns
// nil and is logged as "unmapped, dropped" under -debug. Known gaps are listed
// in README.md.
func encodeKey(k tea.KeyMsg) []byte {
	switch k.Type {
	// ---- text input -------------------------------------------------------
	case tea.KeyRunes, tea.KeySpace:
		if k.Paste {
			// Bracketed paste: wrap the pasted text so the child receives it as
			// one paste, not a storm of individual keystrokes. The child must
			// have enabled bracketed paste (DECSET 2004) for this to matter;
			// claude does.
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
		// represents (KeyCtrlC == 3, KeyCtrlD == 4, ...). Those are exactly the
		// bytes the child expects, so for any positive KeyType in the C0 range
		// we can emit it verbatim. This single arm covers Ctrl-A..Ctrl-Z and the
		// Ctrl-symbol variants without enumerating them.
		if k.Type >= 0 && k.Type <= 31 {
			return []byte{byte(k.Type)}
		}
		return nil // unmapped (function keys, shift+nav, etc.) — see README gaps
	}
}

// withAlt prepends an ESC byte to a navigation sequence when Alt is held. This is
// the simple "ESC-prefix" Meta convention; xterm's modern modifier encoding
// (CSI 1 ; 3 A) is a documented gap, not implemented in the spike.
func withAlt(k tea.KeyMsg, seq string) []byte {
	if k.Alt {
		return append([]byte{0x1b}, seq...)
	}
	return []byte(seq)
}
