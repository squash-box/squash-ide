package pane

import (
	"bytes"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// TestEncodeKey is the input round-trip acceptance check: one assertion per key
// the encoder must map to the bytes a terminal application expects on stdin.
func TestEncodeKey(t *testing.T) {
	cases := []struct {
		name string
		key  tea.KeyMsg
		want []byte
	}{
		{"rune-a", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")}, []byte("a")},
		{"rune-unicode", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("界")}, []byte("界")},
		{"space", tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}, []byte(" ")},
		{"alt-rune", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x"), Alt: true}, []byte{0x1b, 'x'}},
		{"enter-is-CR", tea.KeyMsg{Type: tea.KeyEnter}, []byte{'\r'}},
		{"tab", tea.KeyMsg{Type: tea.KeyTab}, []byte{'\t'}},
		{"backspace-is-DEL", tea.KeyMsg{Type: tea.KeyBackspace}, []byte{0x7f}},
		{"esc", tea.KeyMsg{Type: tea.KeyEsc}, []byte{0x1b}},
		{"up", tea.KeyMsg{Type: tea.KeyUp}, []byte("\x1b[A")},
		{"down", tea.KeyMsg{Type: tea.KeyDown}, []byte("\x1b[B")},
		{"right", tea.KeyMsg{Type: tea.KeyRight}, []byte("\x1b[C")},
		{"left", tea.KeyMsg{Type: tea.KeyLeft}, []byte("\x1b[D")},
		{"home", tea.KeyMsg{Type: tea.KeyHome}, []byte("\x1b[H")},
		{"end", tea.KeyMsg{Type: tea.KeyEnd}, []byte("\x1b[F")},
		{"pgup", tea.KeyMsg{Type: tea.KeyPgUp}, []byte("\x1b[5~")},
		{"pgdn", tea.KeyMsg{Type: tea.KeyPgDown}, []byte("\x1b[6~")},
		{"delete", tea.KeyMsg{Type: tea.KeyDelete}, []byte("\x1b[3~")},
		{"alt-up", tea.KeyMsg{Type: tea.KeyUp, Alt: true}, []byte{0x1b, 0x1b, '[', 'A'}},
		{"ctrl-c", tea.KeyMsg{Type: tea.KeyCtrlC}, []byte{0x03}},
		{"ctrl-d", tea.KeyMsg{Type: tea.KeyCtrlD}, []byte{0x04}},
		{
			"bracketed-paste",
			tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("hi"), Paste: true},
			[]byte("\x1b[200~hi\x1b[201~"),
		},
		{"unmapped-f1-dropped", tea.KeyMsg{Type: tea.KeyF1}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := EncodeKey(tc.key)
			if !bytes.Equal(got, tc.want) {
				t.Errorf("EncodeKey(%s) = %q, want %q", tc.key, got, tc.want)
			}
		})
	}
}

// TestEncodeMouse covers the SGR mouse encoding (the pure primitive T-040 will
// route). Coordinates are 1-based; release terminates with 'm'.
func TestEncodeMouse(t *testing.T) {
	cases := []struct {
		name string
		msg  tea.MouseMsg
		want []byte
	}{
		{
			"left-press-origin",
			tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonLeft, X: 0, Y: 0},
			[]byte("\x1b[<0;1;1M"),
		},
		{
			"right-press",
			tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonRight, X: 9, Y: 4},
			[]byte("\x1b[<2;10;5M"),
		},
		{
			"left-release",
			tea.MouseMsg{Action: tea.MouseActionRelease, Button: tea.MouseButtonLeft, X: 2, Y: 2},
			[]byte("\x1b[<0;3;3m"),
		},
		{
			"wheel-up",
			tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelUp, X: 0, Y: 0},
			[]byte("\x1b[<64;1;1M"),
		},
		{
			"ctrl-left-press",
			tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonLeft, Ctrl: true, X: 0, Y: 0},
			[]byte("\x1b[<16;1;1M"),
		},
		{
			"unmapped-none",
			tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonNone, X: 0, Y: 0},
			nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := EncodeMouse(tc.msg)
			if !bytes.Equal(got, tc.want) {
				t.Errorf("EncodeMouse(%+v) = %q, want %q", tc.msg, got, tc.want)
			}
		})
	}
}
