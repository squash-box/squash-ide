package ui

import (
	"strings"
	"testing"

	"github.com/squashbox/squash-ide/internal/tmux"
)

// countToggleZooms returns the number of "tmux resize-pane -Z -t <pane>"
// calls captured by the recorder. ToggleZoom is the only command in the
// codebase that issues -Z, so this is sufficient to count zoom transitions.
func countToggleZooms(r *tmuxRecorder, pane string) int {
	needle := "resize-pane -Z -t " + pane
	n := 0
	for _, c := range r.calls {
		if strings.Contains(c, needle) {
			n++
		}
	}
	return n
}

func TestCheckFormZoom_OpenEmitsOneToggle(t *testing.T) {
	r := tmuxFixture(t, "%1")

	m := compactModel(400, 1) // wide, not compact, not tooNarrow
	f := newNewTaskForm()
	m.creatingTask = &f

	m.checkFormZoom()

	if !m.formZoomed {
		t.Error("expected formZoomed=true after opening form")
	}
	if n := countToggleZooms(r, "%1"); n != 1 {
		t.Errorf("expected 1 ToggleZoom call on form open, got %d (calls: %v)", n, r.calls)
	}
}

func TestCheckFormZoom_CloseEmitsOneToggle(t *testing.T) {
	r := tmuxFixture(t, "%1")

	m := compactModel(400, 1)
	m.formZoomed = true // simulate prior open
	m.creatingTask = nil

	m.checkFormZoom()

	if m.formZoomed {
		t.Error("expected formZoomed=false after closing form")
	}
	if n := countToggleZooms(r, "%1"); n != 1 {
		t.Errorf("expected 1 ToggleZoom call on form close, got %d (calls: %v)", n, r.calls)
	}
}

func TestCheckFormZoom_Idempotent(t *testing.T) {
	r := tmuxFixture(t, "%1")

	m := compactModel(400, 1)
	f := newNewTaskForm()
	m.creatingTask = &f

	m.checkFormZoom()
	m.checkFormZoom()
	m.checkFormZoom()

	if n := countToggleZooms(r, "%1"); n != 1 {
		t.Errorf("expected 1 ToggleZoom across 3 calls, got %d (calls: %v)", n, r.calls)
	}
}

// TestCheckFormZoom_TooNarrowDefersOpen — when checkTooNarrow already holds
// the pane zoomed (m.tooNarrow=true), opening the form must NOT emit a
// second toggle (which would un-zoom). formZoomed stays false so the
// eventual close also emits no toggle, leaving tooNarrow's zoom intact.
func TestCheckFormZoom_TooNarrowDefersOpen(t *testing.T) {
	r := tmuxFixture(t, "%1")

	m := compactModel(400, 1)
	m.tooNarrow = true
	f := newNewTaskForm()
	m.creatingTask = &f

	m.checkFormZoom()

	if m.formZoomed {
		t.Error("expected formZoomed=false when tooNarrow holds zoom")
	}
	if n := countToggleZooms(r, "%1"); n != 0 {
		t.Errorf("expected 0 ToggleZoom calls when deferring to tooNarrow, got %d (calls: %v)",
			n, r.calls)
	}
}

// TestCheckFormZoom_TooNarrowDefersClose — when tooNarrow holds zoom and
// the form closes (creatingTask=nil) but formZoomed was never set true,
// no toggle is emitted. Guards against an unbalanced un-zoom that would
// release tooNarrow's full-screen overlay.
func TestCheckFormZoom_TooNarrowDefersClose(t *testing.T) {
	r := tmuxFixture(t, "%1")

	m := compactModel(400, 1)
	m.tooNarrow = true
	m.formZoomed = false
	m.creatingTask = nil

	m.checkFormZoom()

	if m.formZoomed {
		t.Error("formZoomed should remain false")
	}
	if n := countToggleZooms(r, "%1"); n != 0 {
		t.Errorf("expected 0 ToggleZoom calls, got %d (calls: %v)", n, r.calls)
	}
}

func TestCheckFormZoom_NoOpOutsideTmux(t *testing.T) {
	t.Setenv("TMUX", "")
	t.Setenv("TMUX_PANE", "")
	r := &tmuxRecorder{}
	prev := tmux.SetRunOutFn(r.fn)
	t.Cleanup(func() { tmux.SetRunOutFn(prev) })

	m := compactModel(400, 1)
	f := newNewTaskForm()
	m.creatingTask = &f

	m.checkFormZoom()

	if m.formZoomed {
		t.Error("formZoomed should remain false outside tmux")
	}
	if len(r.calls) != 0 {
		t.Errorf("expected 0 tmux calls outside tmux session, got: %v", r.calls)
	}
}

// TestCheckFormZoom_OpenViaKeystroke drives the user-facing flow: pressing
// `t` in the list view sets creatingTask and the handler-tail re-check
// fires checkFormZoom, which zooms the pane.
func TestCheckFormZoom_OpenViaKeystroke(t *testing.T) {
	r := tmuxFixture(t, "%1")
	r.windowWidth = 400 // non-compact width

	m := compactModel(400, 1)

	updated, _ := m.Update(keyMsg("t"))
	u := updated.(Model)

	if u.creatingTask == nil {
		t.Fatal("expected creatingTask to be set after pressing t")
	}
	if !u.formZoomed {
		t.Error("expected formZoomed=true after pressing t")
	}
	if n := countToggleZooms(r, "%1"); n != 1 {
		t.Errorf("expected 1 ToggleZoom on form open via keystroke, got %d (calls: %v)",
			n, r.calls)
	}
}

// TestCheckFormZoom_CancelViaEscUnzooms drives cancel: form is open and
// zoomed, Esc closes it, the explicit checkFormZoom call in the cancel
// branch un-zooms before returning.
func TestCheckFormZoom_CancelViaEscUnzooms(t *testing.T) {
	r := tmuxFixture(t, "%1")
	r.windowWidth = 400

	m := compactModel(400, 1)
	f := newNewTaskForm()
	m.creatingTask = &f
	m.formZoomed = true

	updated, _ := m.Update(escKeyMsg())
	u := updated.(Model)

	if u.creatingTask != nil {
		t.Error("expected creatingTask to be cleared on Esc")
	}
	if u.formZoomed {
		t.Error("expected formZoomed=false after Esc")
	}
	if n := countToggleZooms(r, "%1"); n != 1 {
		t.Errorf("expected 1 ToggleZoom on Esc cancel, got %d (calls: %v)", n, r.calls)
	}
}

// TestCheckFormZoom_CompactReleaseAndZoom — the integration that closes
// out the bug. Compact engaged (narrow window + 2 active), pressing `t`
// must (a) release compact (resize back to TUIWidth=60) AND (b) zoom the
// pane. Both transitions fire from the handler-tail.
func TestCheckFormZoom_CompactReleaseAndZoom(t *testing.T) {
	r := tmuxFixture(t, "%1")
	r.windowWidth = 200 // compact-engaging width

	m := compactModel(200, 3)
	m.compact = true
	if !m.isCompact() {
		t.Fatal("precondition: should be compact at width=200 with 3 active")
	}

	updated, _ := m.Update(keyMsg("t"))
	u := updated.(Model)

	if u.creatingTask == nil {
		t.Fatal("expected form to open")
	}
	if u.compact {
		t.Error("expected compact to release while form is open")
	}
	if !u.formZoomed {
		t.Error("expected formZoomed=true after pressing t in compact")
	}
	// One resize back to TUIWidth=60 (compact release) + one ToggleZoom.
	if n := countResizes(r, "%1", 60); n != 1 {
		t.Errorf("expected 1 resize-pane to 60 (compact release), got %d (calls: %v)",
			n, r.calls)
	}
	if n := countToggleZooms(r, "%1"); n != 1 {
		t.Errorf("expected 1 ToggleZoom (form zoom), got %d (calls: %v)", n, r.calls)
	}
}

// TestCheckFormZoom_DispatchingBlocksFormOpen — when m.dispatching is true
// the `t` keystroke handler early-returns with a status message; no form
// is created and no zoom is emitted.
func TestCheckFormZoom_DispatchingBlocksFormOpen(t *testing.T) {
	r := tmuxFixture(t, "%1")
	r.windowWidth = 400

	m := compactModel(400, 1)
	m.dispatching = true

	updated, _ := m.Update(keyMsg("t"))
	u := updated.(Model)

	if u.creatingTask != nil {
		t.Error("expected creatingTask to remain nil while dispatching")
	}
	if u.formZoomed {
		t.Error("expected formZoomed=false while dispatching")
	}
	if n := countToggleZooms(r, "%1"); n != 0 {
		t.Errorf("expected 0 ToggleZoom calls when form open is blocked, got %d (calls: %v)",
			n, r.calls)
	}
}
