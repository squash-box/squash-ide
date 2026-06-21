package pane

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

// TestPane_RenderFloatsStatsRight: a wide pane shows the badge on the left and
// the CPU%/mem readout floated to the right, after the title, with intervening
// padding.
func TestPane_RenderFloatsStatsRight(t *testing.T) {
	fake := newFakePTY(t)
	m := NewManager(WithStarter(fake), WithConstraints(Constraints{MinWidth: 10, Gutter: 1}))
	region := Rect{W: 80, H: 12}
	if _, err := m.Spawn(SpawnSpec{Command: fakeCmd("claude"), TaskID: "T-099", Title: "fix the thing"}); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	m.Resize(region)

	m.SetStatsByTask("T-099", 3.2, true, 145*(1<<20), true)

	out := m.Render(region)
	header := strings.SplitN(out, "\n", 3)[1] // line 0 is the top border, line 1 the header
	if !strings.Contains(header, "WORKING") || !strings.Contains(header, "T-099") {
		t.Errorf("header missing badge/id:\n%s", header)
	}
	if !strings.Contains(header, "3.2%") || !strings.Contains(header, "145M") {
		t.Errorf("header missing floated stats:\n%s", header)
	}
	// Stats come after the title, and the title comes after the id.
	idIdx := strings.Index(header, "T-099")
	titleIdx := strings.Index(header, "fix the thing")
	cpuIdx := strings.Index(header, "3.2%")
	if !(idIdx < titleIdx && titleIdx < cpuIdx) {
		t.Errorf("expected order id < title < stats, got id=%d title=%d cpu=%d", idIdx, titleIdx, cpuIdx)
	}
}

// TestPane_RenderStatsCPUInvalidShowsDash: when cpu isn't valid yet the header
// shows "—" for cpu but still shows mem.
func TestPane_RenderStatsCPUInvalidShowsDash(t *testing.T) {
	fake := newFakePTY(t)
	m := NewManager(WithStarter(fake), WithConstraints(Constraints{MinWidth: 10, Gutter: 1}))
	region := Rect{W: 80, H: 12}
	_, _ = m.Spawn(SpawnSpec{Command: fakeCmd("claude"), TaskID: "T-099", Title: "x"})
	m.Resize(region)

	m.SetStatsByTask("T-099", 0, false, 940*1024, true)

	header := strings.SplitN(m.Render(region), "\n", 3)[1]
	if !strings.Contains(header, "—") {
		t.Errorf("cpu-invalid header should show a dash:\n%s", header)
	}
	if !strings.Contains(header, "940K") {
		t.Errorf("cpu-invalid header should still show mem:\n%s", header)
	}
	if strings.Contains(header, "%") {
		t.Errorf("cpu-invalid header should not show a percentage:\n%s", header)
	}
}

// TestPane_HeaderDropsStatsWhenNarrow: at one cell narrower than the exact fit
// the stats are dropped while the badge + id + title remain, and the header
// never exceeds the width. The exact-fit boundary keeps the stats.
func TestPane_HeaderDropsStatsWhenNarrow(t *testing.T) {
	p := &Pane{id: "pane-1", taskID: "T-099", title: "fix"}
	stats := statsSnapshot{cpuPct: 3.2, cpuValid: true, memBytes: 145 * (1 << 20), ok: true}

	// Build the left segment to measure the exact-fit boundary.
	left := stateBadge(StateWorking, false) + " " + headerMetaStyle.Render("T-099") + " " + headerTitleStyle.Render("fix")
	right := headerStatsStyle.Render(statsText(stats))
	fit := lipgloss.Width(left) + 2 + lipgloss.Width(right)

	atFit := p.headerLine(StateWorking, fit, false, stats)
	if !strings.Contains(atFit, "3.2%") || !strings.Contains(atFit, "145M") {
		t.Errorf("at the exact-fit width the stats should render:\n%s", atFit)
	}
	if w := lipgloss.Width(atFit); w > fit {
		t.Errorf("header width %d exceeds %d at exact fit", w, fit)
	}

	narrow := p.headerLine(StateWorking, fit-1, false, stats)
	if strings.Contains(narrow, "3.2%") {
		t.Errorf("one cell narrower should drop the stats:\n%s", narrow)
	}
	if !strings.Contains(narrow, "T-099") {
		t.Errorf("dropping stats must keep the task id:\n%s", narrow)
	}
	if w := lipgloss.Width(narrow); w > fit-1 {
		t.Errorf("header width %d exceeds %d when narrow", w, fit-1)
	}
}

// TestPane_HeaderNoStatsRegression: with statsOK=false the header renders
// byte-identically to a header built without any stats path — the regression-
// safe no-stats output.
func TestPane_HeaderNoStatsRegression(t *testing.T) {
	p := &Pane{id: "pane-1", taskID: "T-099", title: "fix the thing"}
	noStats := p.headerLine(StateWorking, 60, false, statsSnapshot{ok: false})
	// The pre-T-055 behaviour: badge + id + title, MaxWidth-truncated.
	left := stateBadge(StateWorking, false) + " " + headerMetaStyle.Render("T-099") + " " + headerTitleStyle.Render("fix the thing")
	want := lipgloss.NewStyle().MaxWidth(60).Render(left)
	if noStats != want {
		t.Errorf("statsOK=false header should equal the no-stats render\n got: %q\nwant: %q", noStats, want)
	}
}

// TestPane_SetStatsFrozenWhenDead: a dead pane ignores SetStats — its last
// reading is frozen (the remain-on-exit invariant).
func TestPane_SetStatsFrozenWhenDead(t *testing.T) {
	fake := newFakePTY(t)
	m := NewManager(WithStarter(fake))
	p, _ := m.Spawn(SpawnSpec{Command: fakeCmd("claude"), TaskID: "T-099"})

	p.SetStats(5.0, true, 1000, true)
	p.markDead()
	p.SetStats(99.0, true, 9999, true) // must be ignored

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cpuPct != 5.0 || p.memBytes != 1000 {
		t.Errorf("dead pane stats should stay frozen at 5.0/1000, got %v/%d", p.cpuPct, p.memBytes)
	}
}

// TestManager_SetStatsByTaskUnknownNoOp: an unknown task id is a silent no-op.
func TestManager_SetStatsByTaskUnknownNoOp(t *testing.T) {
	m := NewManager(WithStarter(newFakePTY(t)))
	m.SetStatsByTask("nope", 1, true, 1, true) // must not panic
}

// TestManager_PIDsByTask: returns {taskID: pid} for a live task-bound pane,
// skips dead panes, panes with no task id, and (by construction) the modal.
func TestManager_PIDsByTask(t *testing.T) {
	fake := newFakePTY(t)
	m := NewManager(WithStarter(fake))

	_, _ = m.Spawn(SpawnSpec{Command: fakeCmd("claude"), TaskID: "T-1"})
	_, _ = m.Spawn(SpawnSpec{Command: fakeCmd("claude")}) // no task id — skipped
	dead, _ := m.Spawn(SpawnSpec{Command: fakeCmd("claude"), TaskID: "T-dead"})
	dead.markDead()

	pids := m.PIDsByTask()
	if pids["T-1"] != 4242 { // fakeProc.Pid() == 4242
		t.Errorf("PIDsByTask[T-1] = %d, want 4242", pids["T-1"])
	}
	if _, ok := pids["T-dead"]; ok {
		t.Error("a dead pane should be excluded from PIDsByTask")
	}
	if len(pids) != 1 {
		t.Errorf("PIDsByTask = %v, want exactly {T-1: 4242}", pids)
	}
}

// TestManager_SetStatsByTaskDeadFrozen: SetStatsByTask on a dead pane is frozen.
func TestManager_SetStatsByTaskDeadFrozen(t *testing.T) {
	fake := newFakePTY(t)
	m := NewManager(WithStarter(fake))
	p, _ := m.Spawn(SpawnSpec{Command: fakeCmd("claude"), TaskID: "T-1"})
	m.SetStatsByTask("T-1", 5, true, 1000, true)
	p.markDead()
	m.SetStatsByTask("T-1", 50, true, 5000, true)

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.memBytes != 1000 {
		t.Errorf("dead pane mem should stay frozen at 1000, got %d", p.memBytes)
	}
}

// TestManager_PIDsByTaskEmpty: an empty manager returns an empty map, not nil-deref.
func TestManager_PIDsByTaskEmpty(t *testing.T) {
	m := NewManager(WithStarter(newFakePTY(t)))
	if pids := m.PIDsByTask(); len(pids) != 0 {
		t.Errorf("empty manager PIDsByTask = %v, want empty", pids)
	}
}
