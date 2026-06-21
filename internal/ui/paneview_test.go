package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/squashbox/squash-ide/internal/config"
	"github.com/squashbox/squash-ide/internal/pane"
	"github.com/squashbox/squash-ide/internal/status"
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

	// T-039 lifecycle recording.
	spawned     []pane.SpawnSpec
	spawnErr    error            // when set, Spawn returns it (exercises the rollback path)
	spawnErrFor map[string]error // per-task Spawn error (T-049 partial-failure respawn)
	closedTasks []string
	focusedTask string
	focusErr    error             // when set, FocusByTask returns it (pane died between tick and focus)
	states      map[string]string // taskID -> last state set
	canSpawn    bool

	// T-040 layout-control recording.
	strategy        pane.Strategy
	focusNextCount  int
	focusPrevCount  int
	collapseToggles int
	ticks           int
}

func newStubManager() *stubManager {
	return &stubManager{repaint: make(chan struct{}, 1), states: map[string]string{}, canSpawn: true}
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

func (s *stubManager) Spawn(spec pane.SpawnSpec) (*pane.Pane, error) {
	s.spawned = append(s.spawned, spec)
	if err, ok := s.spawnErrFor[spec.TaskID]; ok {
		return nil, err
	}
	if s.spawnErr != nil {
		return nil, s.spawnErr
	}
	return nil, nil // the Model ignores the returned pane
}

func (s *stubManager) CloseByTask(taskID string) error {
	s.closedTasks = append(s.closedTasks, taskID)
	return nil
}

func (s *stubManager) FocusByTask(taskID string) error {
	if s.focusErr != nil {
		return s.focusErr
	}
	s.focusedTask = taskID
	return nil
}

func (s *stubManager) FocusedTaskID() string { return s.focusedTask }

func (s *stubManager) SetStateByTask(taskID, state string) { s.states[taskID] = state }

func (s *stubManager) CanSpawn() bool { return s.canSpawn }

func (s *stubManager) SetStrategy(st pane.Strategy) { s.strategy = st }
func (s *stubManager) FocusNext()                   { s.focusNextCount++ }
func (s *stubManager) FocusPrev()                   { s.focusPrevCount++ }
func (s *stubManager) ToggleCollapseFocused()       { s.collapseToggles++ }
func (s *stubManager) Tick()                        { s.ticks++ }

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

// 'L' cycles the layout and swaps the manager's strategy. Default is responsive
// (config default), so the first 'L' advances to columns.
func TestNativeCycleLayout(t *testing.T) {
	mgr := newStubManager()
	m := nativeModel(t, mgr)
	if m.layoutName != config.LayoutResponsive {
		t.Fatalf("initial layout = %q, want responsive", m.layoutName)
	}

	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("L")})
	m = out.(Model)

	if m.layoutName != config.LayoutColumns {
		t.Errorf("after L, layout = %q, want columns", m.layoutName)
	}
	if mgr.strategy == nil {
		t.Error("L should have called SetStrategy on the manager")
	}
}

// '[' and ']' drive prev/next-tab (FocusPrev/FocusNext); 'z' toggles collapse.
func TestNativeTabAndCollapseKeys(t *testing.T) {
	mgr := newStubManager()
	m := nativeModel(t, mgr)

	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("]")})
	m = out.(Model)
	out, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("[")})
	m = out.(Model)
	out, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("z")})
	m = out.(Model)

	if mgr.focusNextCount != 1 {
		t.Errorf("] -> FocusNext count = %d, want 1", mgr.focusNextCount)
	}
	if mgr.focusPrevCount != 1 {
		t.Errorf("[ -> FocusPrev count = %d, want 1", mgr.focusPrevCount)
	}
	if mgr.collapseToggles != 1 {
		t.Errorf("z -> ToggleCollapseFocused count = %d, want 1", mgr.collapseToggles)
	}
}

// The native status tick advances the badge-blink phase via manager.Tick.
func TestNativeStatusTick_AdvancesBlink(t *testing.T) {
	mgr := newStubManager()
	m := nativeModel(t, mgr)

	out, _ := m.Update(statusTickMsg{statuses: map[string]status.File{}})
	_ = out.(Model)

	if mgr.ticks == 0 {
		t.Error("native status tick should call manager.Tick for the badge blink")
	}
}

// Focus-follows-input: a status tick that reports input_required for an active
// task surfaces its pane (focus + the UI's pane-focus owner). With it disabled,
// the pane is not surfaced.
func TestNativeFocusFollowsInput(t *testing.T) {
	t.Run("enabled", func(t *testing.T) {
		mgr := newStubManager()
		m := nativeModel(t, mgr) // config.Defaults() -> FocusFollowsInput true

		out, _ := m.Update(statusTickMsg{statuses: map[string]status.File{
			"T-003": {TaskID: "T-003", State: pane.StateInputRequired},
		}})
		m = out.(Model)

		if mgr.focusedTask != "T-003" {
			t.Errorf("focus-follows-input should focus T-003, got %q", mgr.focusedTask)
		}
		if !m.paneFocused {
			t.Error("focus-follows-input should hand the UI focus to the pane region")
		}
	})

	t.Run("disabled", func(t *testing.T) {
		mgr := newStubManager()
		cfg := config.Defaults()
		cfg.Engine = config.EngineNative
		cfg.Vault = "/fake/vault"
		cfg.FocusFollowsInput = false
		m := New(cfg)
		m.manager = mgr
		m.allTasks = testTasks()
		m.width = 200
		m.height = 50
		m.buildItems()
		m.applyFilter()

		out, _ := m.Update(statusTickMsg{statuses: map[string]status.File{
			"T-003": {TaskID: "T-003", State: pane.StateInputRequired},
		}})
		m = out.(Model)

		if mgr.focusedTask != "" {
			t.Errorf("ffi disabled: should not focus a pane, got %q", mgr.focusedTask)
		}
		if m.paneFocused {
			t.Error("ffi disabled: should not steal the UI focus")
		}
		// The badge is still updated from the status pipeline.
		if mgr.states["T-003"] != pane.StateInputRequired {
			t.Errorf("badge state = %q, want input_required", mgr.states["T-003"])
		}
	})
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

// --- T-048: focus-follows-input respects user intent ---

// tickStatusInput drives one status tick reporting `state` for taskID through
// the model (an empty state sends a tick with no entry for the task, exercising
// the synthesised-idle path). Returns the updated Model.
func tickStatusInput(t *testing.T, m Model, taskID, state string) Model {
	t.Helper()
	statuses := map[string]status.File{}
	if state != "" {
		statuses[taskID] = status.File{TaskID: taskID, State: state}
	}
	out, _ := m.Update(statusTickMsg{statuses: statuses})
	return out.(Model)
}

// ctrlW drives a ctrl+w keypress through the model.
func ctrlW(t *testing.T, m Model) Model {
	t.Helper()
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlW})
	return out.(Model)
}

// Core regression (T-048): once the user presses ctrl+w to return to the list,
// the same standing prompt must not yank focus back — not on a continuous pause,
// and not across a transient status gap that synthesises a fresh edge.
func TestFocusFollowsInput_DismissedPaneStaysOnList(t *testing.T) {
	mgr := newStubManager()
	m := nativeModel(t, mgr)

	// First prompt surfaces the pane (preserves the T-040 happy path).
	m = tickStatusInput(t, m, "T-003", pane.StateInputRequired)
	if !m.paneFocused || mgr.focusedTask != "T-003" {
		t.Fatalf("first input_required should surface T-003: paneFocused=%v focused=%q", m.paneFocused, mgr.focusedTask)
	}

	// User deliberately returns to the list.
	m = ctrlW(t, m)
	if m.paneFocused {
		t.Fatal("ctrl+w should return focus to the list")
	}
	if !m.dismissedFocus["T-003"] {
		t.Fatal("ctrl+w should record T-003 as dismissed")
	}
	mgr.focusedTask = "" // observe any subsequent FocusByTask

	// Continuous pause across a tick: edge gate alone holds focus on the list.
	m = tickStatusInput(t, m, "T-003", pane.StateInputRequired)
	// Status flap: entry briefly absent (synthesised idle) then input_required
	// again — a fresh working->input_required edge that dismissal must suppress.
	m = tickStatusInput(t, m, "", "")
	m = tickStatusInput(t, m, "T-003", pane.StateInputRequired)

	if m.paneFocused {
		t.Error("dismissed pane must not re-grab the UI focus across ticks")
	}
	if mgr.focusedTask != "" {
		t.Errorf("dismissed pane must not be re-focused, got %q", mgr.focusedTask)
	}
	if !m.dismissedFocus["T-003"] {
		t.Error("a synthesised-idle flap must not clear the dismissal")
	}
}

// Re-arm (T-048): dismissal suppresses only the standing prompt the user left.
// Once the pane makes genuine progress out of input_required, the next, truly
// new prompt surfaces again.
func TestFocusFollowsInput_ReArmsAfterProgress(t *testing.T) {
	mgr := newStubManager()
	m := nativeModel(t, mgr)

	m = tickStatusInput(t, m, "T-003", pane.StateInputRequired)
	m = ctrlW(t, m)
	if !m.dismissedFocus["T-003"] {
		t.Fatal("precondition: T-003 should be dismissed after ctrl+w")
	}
	mgr.focusedTask = ""

	// Genuine progress (a real new report) clears the dismissal.
	m = tickStatusInput(t, m, "T-003", pane.StateWorking)
	if m.dismissedFocus["T-003"] {
		t.Error("progress out of input_required should re-arm (clear dismissal)")
	}
	if m.paneFocused {
		t.Error("a working report should not surface the pane")
	}

	// The next, genuinely new prompt surfaces again on its edge.
	m = tickStatusInput(t, m, "T-003", pane.StateInputRequired)
	if !m.paneFocused || mgr.focusedTask != "T-003" {
		t.Errorf("re-armed prompt should surface: paneFocused=%v focused=%q", m.paneFocused, mgr.focusedTask)
	}
}

// Modal guard (T-048): a background pane entering input_required must not pull
// focus out from under a user mid-interaction. The badge still updates so the
// pane visibly shows it needs input.
func TestFocusFollowsInput_SuppressedWhileModal(t *testing.T) {
	form := newNewTaskForm()
	cases := []struct {
		name  string
		setup func(*Model)
	}{
		{"new-task form", func(m *Model) { m.creatingTask = &form }},
		{"spawn confirm", func(m *Model) { m.confirming = &task.Task{ID: "T-001"} }},
		{"filter active", func(m *Model) { m.filterActive = true }},
		{"detail view", func(m *Model) { m.view = detailView }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mgr := newStubManager()
			m := nativeModel(t, mgr)
			tc.setup(&m)

			m = tickStatusInput(t, m, "T-003", pane.StateInputRequired)

			if m.paneFocused {
				t.Error("focus-follows-input must not steal focus while a modal owns the keyboard")
			}
			if mgr.focusedTask != "" {
				t.Errorf("no FocusByTask expected while a modal is open, got %q", mgr.focusedTask)
			}
			if mgr.states["T-003"] != pane.StateInputRequired {
				t.Errorf("badge state = %q, want input_required (badge must still update)", mgr.states["T-003"])
			}
		})
	}
}

// Exception (T-048): ctrl+w with no focused pane records no dismissal and does
// not panic; pane focus still clears.
func TestFocusDismiss_NoFocusedPaneNoPanic(t *testing.T) {
	mgr := newStubManager() // focusedTask "" => FocusedTaskID() == ""
	m := nativeModel(t, mgr)
	m.paneFocused = true

	m = ctrlW(t, m)
	if m.paneFocused {
		t.Error("ctrl+w should still clear pane focus")
	}
	if len(m.dismissedFocus) != 0 {
		t.Errorf("no dismissal expected with no focused pane, got %v", m.dismissedFocus)
	}
}

// Exception (T-048): if the pane died between the tick and the focus call,
// FocusByTask returns ErrUnknownPane and the UI must leave focus unchanged.
func TestFocusFollowsInput_FocusByTaskErrLeavesFocus(t *testing.T) {
	mgr := newStubManager()
	mgr.focusErr = pane.ErrUnknownPane
	m := nativeModel(t, mgr)

	m = tickStatusInput(t, m, "T-003", pane.StateInputRequired)
	if m.paneFocused {
		t.Error("a FocusByTask error must leave the UI focus on the list")
	}
}
