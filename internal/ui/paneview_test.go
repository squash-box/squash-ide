package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/squashbox/squash-ide/internal/config"
	"github.com/squashbox/squash-ide/internal/pane"
	"github.com/squashbox/squash-ide/internal/task"
)

// stubManager is a paneManager that records the calls the native UI makes,
// without forking a real PTY. renderOut controls what Render returns (""
// exercises the empty-state placeholder path); writes captures the bytes the
// input router forwarded; lastResize captures the region from Resize.
type stubManager struct {
	renderOut  string
	lastResize pane.Rect
	resizes    int
	writes     [][]byte
	repaint    chan struct{}
}

func newStubManager() *stubManager {
	return &stubManager{repaint: make(chan struct{}, 1)}
}

func (s *stubManager) Resize(region pane.Rect) {
	s.lastResize = region
	s.resizes++
}

func (s *stubManager) Render(region pane.Rect) string { return s.renderOut }

func (s *stubManager) WriteToFocused(b []byte) (int, error) {
	cp := append([]byte(nil), b...)
	s.writes = append(s.writes, cp)
	return len(b), nil
}

func (s *stubManager) Repaint() <-chan struct{} { return s.repaint }

// nativeModel builds a native-engine Model wired to a stub manager, pre-loaded
// with tasks and a default size, ready to drive through Update/View directly.
func nativeModel(t *testing.T, mgr paneManager) Model {
	t.Helper()
	cfg := config.Defaults()
	cfg.Engine = config.EngineNative
	cfg.Vault = "/fake/vault"
	m := New(cfg)
	m.manager = mgr // replace the real manager with the stub
	m.allTasks = testTasks()
	m.width = 200
	m.height = 50
	m.buildItems()
	m.applyFilter()
	m.clampCursor()
	return m
}

func TestNew_NativeBuildsManager(t *testing.T) {
	cfg := config.Defaults()
	cfg.Engine = config.EngineNative
	m := New(cfg)
	if !m.engineNative {
		t.Fatal("engineNative should be true for native engine")
	}
	if m.manager == nil {
		t.Fatal("native New should construct a pane manager")
	}
}

func TestNew_TmuxHasNoManager(t *testing.T) {
	m := New(config.Defaults()) // engine defaults to tmux
	if m.engineNative {
		t.Fatal("engineNative should be false for tmux engine")
	}
	if m.manager != nil {
		t.Fatal("tmux engine should not construct a pane manager")
	}
}

// View in native mode joins the list (left) with the manager's render (right).
func TestNativeView_JoinsListAndPaneRegion(t *testing.T) {
	mgr := newStubManager()
	mgr.renderOut = "PANE-REGION-SENTINEL"
	m := nativeModel(t, mgr)

	view := m.View()
	if !strings.Contains(view, "PANE-REGION-SENTINEL") {
		t.Error("native view should include the manager's render output")
	}
	// The list renders the ID with a "#" prefix (T-003 -> "#003"); asserting on
	// that form (plus the title) proves the task list is composed in.
	if !strings.Contains(view, "#003") || !strings.Contains(view, "Active task") {
		t.Error("native view should include the task list (#003 Active task)")
	}
}

// A WindowSizeMsg in native mode resizes the manager with W == width-TUI-gutter
// and never touches the tmux too-narrow/compact state.
func TestNativeWindowSize_ResizesManager(t *testing.T) {
	mgr := newStubManager()
	m := nativeModel(t, mgr)

	out, _ := m.Update(tea.WindowSizeMsg{Width: 200, Height: 50})
	um := out.(Model)

	wantW := 200 - um.tuiWidth() - paneGutter
	if mgr.lastResize.W != wantW {
		t.Errorf("Resize width = %d, want %d", mgr.lastResize.W, wantW)
	}
	if mgr.lastResize.H != 50 {
		t.Errorf("Resize height = %d, want 50", mgr.lastResize.H)
	}
	if mgr.lastResize.X != um.tuiWidth()+paneGutter {
		t.Errorf("Resize X = %d, want %d", mgr.lastResize.X, um.tuiWidth()+paneGutter)
	}
}

// Regression guard: native mode never engages the tmux tooNarrow/compact path,
// even at a width + active-task count that would trip it under tmux.
func TestNativeWindowSize_NoTmuxCoupling(t *testing.T) {
	mgr := newStubManager()
	m := nativeModel(t, mgr)
	// Two active tasks + a sub-300 width is exactly the compact/too-narrow
	// trigger in tmux mode.
	m.allTasks = []task.Task{
		{ID: "T-1", Title: "a", Status: "active"},
		{ID: "T-2", Title: "b", Status: "active"},
	}
	m.buildItems()

	out, _ := m.Update(tea.WindowSizeMsg{Width: 150, Height: 40})
	um := out.(Model)

	if um.tooNarrow {
		t.Error("native mode must not set tooNarrow (tmux-only state)")
	}
	if um.compact {
		t.Error("native mode must not set compact (tmux-only state)")
	}
	if um.isCompact() {
		t.Error("isCompact must be false in native mode")
	}
	if mgr.resizes == 0 {
		t.Error("native WindowSizeMsg should have resized the manager")
	}
}

// Zero panes (Render == "") falls back to the placeholder, no panic.
func TestNativeView_ZeroPanesPlaceholder(t *testing.T) {
	mgr := newStubManager()
	mgr.renderOut = "" // no panes
	m := nativeModel(t, mgr)

	view := m.View()
	if !strings.Contains(view, "Native pane region") {
		t.Error("native view with no panes should render the placeholder")
	}
}

// Focus toggle: ctrl+w hands focus to the pane region; subsequent keys forward
// to the focused child and are NOT interpreted as list commands. Toggling back
// restores list navigation.
func TestNativeFocusToggle_RoutesKeys(t *testing.T) {
	mgr := newStubManager()
	m := nativeModel(t, mgr)
	startCursor := m.cursor

	// ctrl+w → focus the pane region.
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlW})
	m = out.(Model)
	if !m.paneFocused {
		t.Fatal("ctrl+w should focus the pane region")
	}

	// 'j' now forwards to the child, not the list cursor.
	out, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	m = out.(Model)
	if m.cursor != startCursor {
		t.Error("cursor moved while pane-focused — key leaked into the list")
	}
	if len(mgr.writes) != 1 || string(mgr.writes[0]) != "j" {
		t.Errorf("expected forwarded 'j', got %q", mgr.writes)
	}

	// ctrl+w again → back to the list.
	out, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlW})
	m = out.(Model)
	if m.paneFocused {
		t.Fatal("ctrl+w should return focus to the list")
	}

	// 'j' moves the list cursor again (T-003 active is the first selectable,
	// so down should advance off it).
	out, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	m = out.(Model)
	if len(mgr.writes) != 1 {
		t.Error("key after toggling back should not forward to the pane")
	}
}

// ctrl+c quits even from a focused pane so the user is never trapped.
func TestNativeFocusToggle_CtrlCQuits(t *testing.T) {
	mgr := newStubManager()
	m := nativeModel(t, mgr)
	m.paneFocused = true

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil {
		t.Fatal("ctrl+c should return a quit command")
	}
	if msg := cmd(); msg == nil {
		t.Error("expected a tea.Quit message from ctrl+c")
	}
}

// A terminal narrower than TUIWidth + gutter + min pane renders the native
// too-narrow overlay rather than shelling tmux.
func TestNativeView_TooNarrow(t *testing.T) {
	mgr := newStubManager()
	mgr.renderOut = "PANE-REGION-SENTINEL"
	m := nativeModel(t, mgr)
	m.width = m.tuiWidth() + 5 // well below tui + gutter + nativeMinPaneWidth

	view := m.View()
	if !strings.Contains(view, "too narrow") {
		t.Errorf("expected too-narrow overlay, got: %q", view)
	}
	if strings.Contains(view, "PANE-REGION-SENTINEL") {
		t.Error("too-narrow overlay should not render the pane region")
	}
}

// paneOutputMsg re-arms the repaint listener and triggers a re-render.
func TestNativePaneOutputMsg_RearmsListener(t *testing.T) {
	mgr := newStubManager()
	m := nativeModel(t, mgr)
	_, cmd := m.Update(paneOutputMsg{})
	if cmd == nil {
		t.Fatal("paneOutputMsg should return a command to re-arm the listener")
	}
}
