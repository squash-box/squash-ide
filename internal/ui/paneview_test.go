package ui

import (
	"bytes"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/squashbox/squash-ide/internal/config"
	"github.com/squashbox/squash-ide/internal/pane"
	"github.com/squashbox/squash-ide/internal/procstat"
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

	// T-055 resource-readout recording.
	pids     map[string]int         // returned by PIDsByTask
	statsSet map[string]statsRecord // taskID -> last stats set via SetStatsByTask

	// T-051 mouse hit-test scripting: when set, TaskAtPoint delegates to it so a
	// test can map a synthetic (x, y) to a chosen task id (or a miss). Nil => miss.
	taskAtPoint func(x, y int) (string, bool)

	// T-040 layout-control recording.
	strategy        pane.Strategy
	focusNextCount  int
	focusPrevCount  int
	collapseToggles int
	ticks           int

	// T-054 log-task tab recording. Spawn registers a child-exit channel per
	// task id so DoneByTask(id) returns non-nil, mirroring a real spawned pane;
	// a test closes it to simulate the child exiting.
	doneChans map[string]chan struct{}
}

func newStubManager() *stubManager {
	return &stubManager{
		repaint:   make(chan struct{}, 1),
		states:    map[string]string{},
		canSpawn:  true,
		doneChans: map[string]chan struct{}{},
	}
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
	// Register a child-exit channel so DoneByTask(id) returns non-nil — the
	// native /log-task tab watcher waits on it (T-054).
	if s.doneChans != nil && spec.TaskID != "" {
		if _, ok := s.doneChans[spec.TaskID]; !ok {
			s.doneChans[spec.TaskID] = make(chan struct{})
		}
	}
	return nil, nil // the Model ignores the returned pane
}

func (s *stubManager) CloseByTask(taskID string) error {
	s.closedTasks = append(s.closedTasks, taskID)
	return nil
}

func (s *stubManager) DoneByTask(taskID string) <-chan struct{} {
	if ch, ok := s.doneChans[taskID]; ok {
		return ch
	}
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

func (s *stubManager) TaskAtPoint(x, y int) (string, bool) {
	if s.taskAtPoint != nil {
		return s.taskAtPoint(x, y)
	}
	return "", false
}

func (s *stubManager) SetStateByTask(taskID, state string) { s.states[taskID] = state }

func (s *stubManager) CanSpawn() bool { return s.canSpawn }

// statsRecord captures one SetStatsByTask call for assertions.
type statsRecord struct {
	cpuPct   float64
	cpuValid bool
	memBytes uint64
	ok       bool
}

func (s *stubManager) PIDsByTask() map[string]int { return s.pids }

func (s *stubManager) SetStatsByTask(taskID string, cpuPct float64, cpuValid bool, memBytes uint64, ok bool) {
	if s.statsSet == nil {
		s.statsSet = map[string]statsRecord{}
	}
	s.statsSet[taskID] = statsRecord{cpuPct: cpuPct, cpuValid: cpuValid, memBytes: memBytes, ok: ok}
}

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

// A terminal narrower than CompactListWidth + gutter + min pane (61) renders the
// native too-narrow overlay rather than shelling tmux. Post-T-052 the floor is
// the *compact* list, so the width must sit below 61 (not merely below the
// full-list floor of 101 — that band now compacts instead of overlaying).
func TestNativeView_TooNarrow(t *testing.T) {
	mgr := newStubManager()
	mgr.renderOut = "PANE-REGION-SENTINEL"
	m := nativeModel(t, mgr)
	m.width = CompactListWidth + 5 // 25 — below the compact floor of 61

	view := m.View()
	if !strings.Contains(view, "too narrow") {
		t.Errorf("expected too-narrow overlay, got: %q", view)
	}
	if strings.Contains(view, "PANE-REGION-SENTINEL") {
		t.Error("too-narrow overlay should not render the pane region")
	}
}

// nativeListWidth scales the list between CompactListWidth (floor) and tuiWidth
// (ceiling), reserving paneGutter+nativeMinPaneWidth for the panes. This
// supersedes the binary T-052 collapse: the list no longer snaps from full to
// compact, it tracks the terminal across the band.
func TestNativeListWidth_TruthTable(t *testing.T) {
	mgr := newStubManager()
	cases := []struct {
		name  string
		width int
		want  int
	}{
		{"wide — parked at ceiling", 200, 60},
		{"boundary 101 — still ceiling", 101, 60},
		{"just inside band — one below ceiling", 100, 59},
		{"mid band", 90, 49},
		{"panes hit their floor", 81, 40},
		{"one narrower — list dips below 40", 80, 39},
		{"compact floor (61) — list at minimum", 61, 20},
		{"below floor — clamped to minimum", 60, 20},
		{"width zero — startup, report full width", 0, 60},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := nativeModel(t, mgr)
			m.width = tc.width
			if got := m.nativeListWidth(); got != tc.want {
				t.Errorf("nativeListWidth(width=%d) = %d, want %d", tc.width, got, tc.want)
			}
		})
	}
}

// In the responsive band the list takes its scaled width and the manager gets
// exactly the columns the list leaves behind — region X/W track nativeListWidth,
// never the old fixed CompactListWidth reservation.
func TestNativeView_ResponsiveRegion(t *testing.T) {
	mgr := newStubManager()
	mgr.renderOut = "PANE-REGION-SENTINEL"
	m := nativeModel(t, mgr)
	m.width = 90 // between the compact floor (61) and the full-list floor (101)

	listW := m.nativeListWidth()
	if listW != 49 {
		t.Fatalf("nativeListWidth() = %d at width=90, want 49", listW)
	}

	rr := m.rightRegion()
	if rr.X != listW+paneGutter {
		t.Errorf("rightRegion().X = %d, want %d", rr.X, listW+paneGutter)
	}
	if rr.W != 90-listW-paneGutter {
		t.Errorf("rightRegion().W = %d, want %d", rr.W, 90-listW-paneGutter)
	}

	// The view composes the list + pane region (sentinel present), not the overlay.
	view := m.View()
	if strings.Contains(view, "too narrow") {
		t.Error("responsive band should not render the too-narrow overlay")
	}
	if !strings.Contains(view, "PANE-REGION-SENTINEL") {
		t.Error("responsive band should render the manager's pane region")
	}
}

// Above width 80 the scaled list clears 40 cols, so it renders the full-width
// chrome (not the compact stub) — every line within the scaled width budget.
func TestNativeListViewRender_FullChromeInBand(t *testing.T) {
	mgr := newStubManager()
	m := nativeModel(t, mgr)
	m.width = 90
	listW := m.nativeListWidth() // 49 — above the 40-col compact-chrome threshold

	out := m.listViewRender()
	for i, line := range strings.Split(out, "\n") {
		if w := lipgloss.Width(line); w > listW {
			t.Errorf("native list line %d width %d exceeds scaled width %d: %q",
				i, w, listW, line)
		}
	}
}

// Once the scaled list drops below 40 cols (terminal in the lower band) the
// chrome switches to its compact layout: the 'sq' stub appears and every line
// fits the scaled width.
func TestNativeListViewRender_CompactChromeWhenNarrow(t *testing.T) {
	mgr := newStubManager()
	m := nativeModel(t, mgr)
	m.width = 70 // list = 70-1-40 = 29, below the compact-chrome threshold
	listW := m.nativeListWidth()
	if listW != 29 {
		t.Fatalf("nativeListWidth() = %d at width=70, want 29", listW)
	}

	out := m.listViewRender()
	if !strings.Contains(out, "sq") {
		t.Errorf("expected compact top bar stub 'sq' in narrow native list: %q", out)
	}
	for i, line := range strings.Split(out, "\n") {
		if w := lipgloss.Width(line); w > listW {
			t.Errorf("narrow native list line %d width %d exceeds %d: %q",
				i, w, listW, line)
		}
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

// --- T-055: per-pane CPU/mem header readout ---

// Native Init batches the 15s resource tick when pane_stats is enabled, and a
// resourceTickMsg fans usage out to SetStatsByTask and re-arms.
func TestNativeResourceTick_FansOutAndRearms(t *testing.T) {
	mgr := newStubManager()
	m := nativeModel(t, mgr) // config.Defaults() -> PaneStats true

	if m.statsCollector == nil {
		t.Fatal("native model with pane_stats=true should build a stats collector")
	}

	msg := resourceTickMsg{usage: map[string]procstat.Usage{
		"T-003": {CPUPercent: 3.2, CPUValid: true, RSSBytes: 145 << 20, OK: true},
		"T-009": {OK: false},
	}}
	out, cmd := m.Update(msg)
	_ = out.(Model)

	rec, ok := mgr.statsSet["T-003"]
	if !ok || !rec.ok || !rec.cpuValid || rec.cpuPct != 3.2 || rec.memBytes != 145<<20 {
		t.Errorf("SetStatsByTask(T-003) = %+v, want cpu 3.2 valid mem 145M ok", rec)
	}
	failed := mgr.statsSet["T-009"]
	if failed.ok {
		t.Errorf("a failed sample should set ok=false for T-009, got %+v", failed)
	}
	if cmd == nil {
		t.Error("resourceTickMsg should re-arm the resource tick")
	}
}

// With pane_stats disabled, no collector is built, tickResources returns nil,
// and a resourceTickMsg (should one arrive) drives no stats and no re-arm.
func TestNativeResourceTick_DisabledNoCollector(t *testing.T) {
	cfg := config.Defaults()
	cfg.Engine = config.EngineNative
	cfg.Vault = "/fake/vault"
	cfg.PaneStats = false
	m := New(cfg)
	mgr := newStubManager()
	m.manager = mgr
	m.allTasks = testTasks()
	m.width = 200
	m.height = 50
	m.buildItems()

	if m.statsCollector != nil {
		t.Fatal("pane_stats=false must not build a stats collector")
	}
	if m.tickResources() != nil {
		t.Error("pane_stats=false: tickResources should return nil (never armed)")
	}
}

// tmux mode never builds a stats collector (native-only feature).
func TestTmuxModeNoStatsCollector(t *testing.T) {
	m := New(config.Defaults()) // tmux engine
	if m.statsCollector != nil {
		t.Error("tmux engine must not build a stats collector")
	}
	if m.tickResources() != nil {
		t.Error("tmux engine tickResources should be nil")
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

// --- T-051: click a native pane to focus it ---

// hitAt scripts the stub's TaskAtPoint to return (id, true) for any coordinate;
// "" scripts a miss (click on the list / gutter / tab strip).
func hitAt(mgr *stubManager, id string) {
	if id == "" {
		mgr.taskAtPoint = func(int, int) (string, bool) { return "", false }
		return
	}
	mgr.taskAtPoint = func(int, int) (string, bool) { return id, true }
}

// press drives a left-button mouse press at (x, y) through the model.
func press(t *testing.T, m Model, x, y int) Model {
	t.Helper()
	out, _ := m.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonLeft, X: x, Y: y})
	return out.(Model)
}

// Click on an unfocused pane while the list owns focus: it acquires focus on the
// hit task and consumes the press (no forward to the child).
func TestMouse_ClickFocusesPane(t *testing.T) {
	mgr := newStubManager()
	m := nativeModel(t, mgr)
	hitAt(mgr, "T-003")

	m = press(t, m, 120, 5)

	if !m.paneFocused {
		t.Error("clicking a pane should hand the UI focus to the pane region")
	}
	if mgr.focusedTask != "T-003" {
		t.Errorf("FocusByTask = %q, want T-003", mgr.focusedTask)
	}
	if len(mgr.writes) != 0 {
		t.Errorf("focus-acquire press must not be forwarded to the child, got %d writes", len(mgr.writes))
	}
}

// A press on the already-focused pane forwards to the child via EncodeMouse,
// leaving focus unchanged. Asserts the exact SGR bytes for the press coordinates.
func TestMouse_ForwardsToFocusedChild(t *testing.T) {
	mgr := newStubManager()
	mgr.focusedTask = "T-003"
	m := nativeModel(t, mgr)
	m.paneFocused = true
	hitAt(mgr, "T-003")

	m = press(t, m, 120, 5)

	if !m.paneFocused || mgr.focusedTask != "T-003" {
		t.Errorf("focus should be unchanged: paneFocused=%v focused=%q", m.paneFocused, mgr.focusedTask)
	}
	if len(mgr.writes) != 1 {
		t.Fatalf("expected one forwarded write, got %d", len(mgr.writes))
	}
	// SGR: left press (Cb 0), coords 1-based → X 120 -> 121, Y 5 -> 6, terminator M.
	want := []byte("\x1b[<0;121;6M")
	if !bytes.Equal(mgr.writes[0], want) {
		t.Errorf("forwarded bytes = %q, want %q", mgr.writes[0], want)
	}
}

// A click that hits no pane (the list, gutter, or tab strip) is a no-op: focus is
// unchanged and nothing is forwarded.
func TestMouse_MissIsNoOp(t *testing.T) {
	mgr := newStubManager()
	m := nativeModel(t, mgr)
	hitAt(mgr, "") // miss

	m = press(t, m, 1, 1)

	if m.paneFocused {
		t.Error("a click that hits no pane must not change focus")
	}
	if mgr.focusedTask != "" || len(mgr.writes) != 0 {
		t.Errorf("a miss must not focus or forward: focused=%q writes=%d", mgr.focusedTask, len(mgr.writes))
	}
}

// Clicking switches focus between two panes: focusing A then clicking B calls
// FocusByTask(B) and leaves the pane region focused.
func TestMouse_SwitchesFocusBetweenPanes(t *testing.T) {
	mgr := newStubManager()
	mgr.focusedTask = "T-001"
	m := nativeModel(t, mgr)
	m.paneFocused = true
	hitAt(mgr, "T-003") // click lands on the other pane

	m = press(t, m, 120, 5)

	if !m.paneFocused {
		t.Error("switching focus by click should keep the pane region focused")
	}
	if mgr.focusedTask != "T-003" {
		t.Errorf("FocusByTask = %q, want T-003", mgr.focusedTask)
	}
	if len(mgr.writes) != 0 {
		t.Errorf("a focus switch must not forward the press, got %d writes", len(mgr.writes))
	}
}

// A click re-arms focus-follows-input for a pane the user had dismissed with
// ctrl+w — a deliberate gesture overrides the standing-prompt suppression (T-048).
func TestMouse_ClickReArmsDismissedFocus(t *testing.T) {
	mgr := newStubManager()
	m := nativeModel(t, mgr)
	m.dismissedFocus = map[string]bool{"T-003": true}
	hitAt(mgr, "T-003")

	m = press(t, m, 120, 5)

	if m.dismissedFocus["T-003"] {
		t.Error("clicking a dismissed pane should clear its dismissedFocus entry")
	}
}

// A click is ignored while a modal/dialog/detail view owns the screen — the pane
// region isn't drawn, so a stray press must not move focus behind the overlay.
func TestMouse_IgnoredWhileModal(t *testing.T) {
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
			hitAt(mgr, "T-003") // would hit a pane if the handler got that far

			m = press(t, m, 120, 5)

			if m.paneFocused {
				t.Error("a click must not focus a pane while a modal owns the screen")
			}
			if mgr.focusedTask != "" || len(mgr.writes) != 0 {
				t.Errorf("modal click must be a no-op: focused=%q writes=%d", mgr.focusedTask, len(mgr.writes))
			}
		})
	}
}

// Plain motion and release are ignored (only presses route) so a moving pointer
// never thrashes focus or floods the child.
func TestMouse_MotionAndReleaseIgnored(t *testing.T) {
	mgr := newStubManager()
	mgr.focusedTask = "T-003"
	m := nativeModel(t, mgr)
	m.paneFocused = true
	hitAt(mgr, "T-003")

	for _, action := range []tea.MouseAction{tea.MouseActionMotion, tea.MouseActionRelease} {
		out, _ := m.Update(tea.MouseMsg{Action: action, Button: tea.MouseButtonLeft, X: 120, Y: 5})
		m = out.(Model)
	}
	if len(mgr.writes) != 0 {
		t.Errorf("motion/release must not forward to the child, got %d writes", len(mgr.writes))
	}
}

// Exception: FocusByTask returning ErrUnknownPane (pane closed between render and
// click) is swallowed — focus stays on the list, no panic.
func TestMouse_FocusByTaskErrSwallowed(t *testing.T) {
	mgr := newStubManager()
	mgr.focusErr = pane.ErrUnknownPane
	m := nativeModel(t, mgr)
	hitAt(mgr, "T-003")

	m = press(t, m, 120, 5)

	if m.paneFocused {
		t.Error("a FocusByTask error must leave focus on the list")
	}
	if len(mgr.writes) != 0 {
		t.Errorf("no forward on a focus error, got %d writes", len(mgr.writes))
	}
}

// Exception: an unmappable event (EncodeMouse returns nil) on the focused pane
// forwards nothing — no write, no panic.
func TestMouse_UnmappableEventNotForwarded(t *testing.T) {
	mgr := newStubManager()
	mgr.focusedTask = "T-003"
	m := nativeModel(t, mgr)
	m.paneFocused = true
	hitAt(mgr, "T-003")

	// MouseButtonNone is unmappable — EncodeMouse returns nil.
	out, _ := m.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonNone, X: 120, Y: 5})
	m = out.(Model)

	if len(mgr.writes) != 0 {
		t.Errorf("an unmappable event must not be forwarded, got %d writes", len(mgr.writes))
	}
}

// Regression: tmux mode never handles mouse events (the program is built without
// mouse support), so a MouseMsg is a no-op and never touches a (nil) manager.
func TestMouse_TmuxModeNoOp(t *testing.T) {
	m := New(config.Defaults()) // tmux engine — manager is nil
	out, _ := m.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonLeft, X: 10, Y: 10})
	if _, ok := out.(Model); !ok {
		t.Fatal("tmux MouseMsg should return the Model unchanged")
	}
}
