package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/squashbox/squash-ide/internal/task"
	"github.com/squashbox/squash-ide/internal/tmux"
)

// compactModel builds a Model with a configurable width and active count
// for isCompact predicate tests. It bypasses modelWithTasks's width=80
// default so the predicate's width arm is exercised directly.
//
// Sets both m.width (pane width) and m.windowWidth (outer terminal width)
// to the same value — the common non-tmux case where the two are
// equivalent. Tests that exercise the divergent path (pane pinned narrow
// while window is wide) override m.windowWidth after construction.
func compactModel(width, activeCount int) Model {
	var tasks []task.Task
	for i := 0; i < activeCount; i++ {
		tasks = append(tasks, task.Task{
			ID: "T-00" + string(rune('1'+i)), Status: "active",
			Title: "active task", Project: "p", Type: "feature",
		})
	}
	// Pad with backlog tasks so the model has "real" data.
	tasks = append(tasks, task.Task{
		ID: "T-099", Status: "backlog", Title: "backlog", Project: "p", Type: "feature",
	})
	m := modelWithTasks(tasks)
	m.width = width
	m.windowWidth = width
	return m
}

func TestIsCompact_TruthTable(t *testing.T) {
	cases := []struct {
		name        string
		width       int
		activeCount int
		want        bool
	}{
		{"wide terminal, many active", 400, 5, false},
		{"narrow width, 2 active — compact", 299, 2, true},
		{"exactly 300 — not less than threshold", 300, 2, false},
		{"width below, but only 1 active", 299, 1, false},
		{"width below, zero active", 299, 0, false},
		{"very narrow, 2 active", 100, 2, true},
		{"width zero — ignored (startup)", 0, 5, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := compactModel(tc.width, tc.activeCount)
			if got := m.isCompact(); got != tc.want {
				t.Errorf("isCompact() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestIsCompact_WindowWidthArm covers the post-T-028 predicate semantics:
// isCompact keys on m.windowWidth (the outer terminal width) and only
// falls back to m.width when windowWidth is unset (non-tmux path). The
// "window wide / pane narrow" case is the one the bug missed — pane is
// pinned at 20 by tmux but the user has widened the terminal, so compact
// must release.
func TestIsCompact_WindowWidthArm(t *testing.T) {
	cases := []struct {
		name        string
		windowWidth int
		width       int
		activeCount int
		want        bool
	}{
		{"window wide, pane narrow — release (the fix)", 400, 20, 3, false},
		{"window at threshold — not less than 300", 300, 20, 3, false},
		{"window narrow, pane narrow — stay compact", 299, 20, 3, true},
		{"non-tmux fallback (windowWidth=0), narrow", 0, 299, 3, true},
		{"non-tmux fallback (windowWidth=0), wide", 0, 400, 3, false},
		{"windowWidth shadows m.width when wide", 400, 100, 3, false},
		{"windowWidth shadows m.width when narrow", 100, 400, 3, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := compactModel(tc.width, tc.activeCount)
			m.windowWidth = tc.windowWidth
			if got := m.isCompact(); got != tc.want {
				t.Errorf("isCompact(windowWidth=%d, width=%d, active=%d) = %v, want %v",
					tc.windowWidth, tc.width, tc.activeCount, got, tc.want)
			}
		})
	}
}

func TestIsCompact_DialogsDisable(t *testing.T) {
	base := compactModel(200, 3)
	if !base.isCompact() {
		t.Fatal("precondition: base model should be compact")
	}

	t.Run("spawn confirm", func(t *testing.T) {
		m := base
		tk := task.Task{ID: "T-001"}
		m.confirming = &tk
		if m.isCompact() {
			t.Error("expected compact to stand down while confirm dialog open")
		}
	})
	t.Run("complete confirm", func(t *testing.T) {
		m := base
		tk := task.Task{ID: "T-001"}
		m.completing = &tk
		if m.isCompact() {
			t.Error("expected compact to stand down while complete dialog open")
		}
	})
	t.Run("deactivate confirm", func(t *testing.T) {
		m := base
		tk := task.Task{ID: "T-001"}
		m.deactivating = &tk
		if m.isCompact() {
			t.Error("expected compact to stand down while deactivate dialog open")
		}
	})
	t.Run("block reason input", func(t *testing.T) {
		m := base
		tk := task.Task{ID: "T-001"}
		m.blocking = &tk
		if m.isCompact() {
			t.Error("expected compact to stand down while block input open")
		}
	})
}

func TestRenderTopBarCompact_FitsWidth(t *testing.T) {
	cases := []struct {
		name   string
		counts map[string]int
	}{
		{"all states populated", map[string]int{"active": 3, "backlog": 2, "blocked": 1}},
		{"active and backlog only", map[string]int{"active": 3, "backlog": 2}},
		{"empty counts", map[string]int{}},
		{"large counts", map[string]int{"active": 99, "backlog": 99, "blocked": 99}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := renderTopBarCompact(CompactListWidth, tc.counts)
			if w := lipgloss.Width(got); w > CompactListWidth {
				t.Errorf("width %d exceeds budget %d: %q",
					w, CompactListWidth, got)
			}
		})
	}
}

func TestRenderTopBarCompact_IncludesCountsAndAppStub(t *testing.T) {
	got := renderTopBarCompact(CompactListWidth, map[string]int{"active": 3, "backlog": 2})
	if !strings.Contains(got, "sq") {
		t.Errorf("expected app stub 'sq' in %q", got)
	}
	if !strings.Contains(got, "3a") {
		t.Errorf("expected '3a' (active count) in %q", got)
	}
	if !strings.Contains(got, "2b") {
		t.Errorf("expected '2b' (backlog count) in %q", got)
	}
}

func TestHelpLineCompact_FitsWidth(t *testing.T) {
	cases := []struct {
		name         string
		filterActive bool
		filterSet    bool
	}{
		{"default", false, false},
		{"filter active", true, false},
		{"filter set but not active", false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := helpLineCompact(tc.filterActive, tc.filterSet)
			if w := lipgloss.Width(got); w > CompactListWidth {
				t.Errorf("help-line width %d exceeds budget %d: %q",
					w, CompactListWidth, got)
			}
			if got == "" {
				t.Error("help line should not be empty")
			}
		})
	}
}

func TestRenderCard_CompactBacklog_ThreeLines(t *testing.T) {
	tk := task.Task{
		ID: "T-042", Type: "feature", Title: "An ordinary backlog title",
		Project: "squash-ide", Status: "backlog",
	}
	lines := renderCard(tk, false, CompactListWidth, nil, true)
	if len(lines) != 3 {
		t.Fatalf("compact backlog card: expected 3 lines (id/title/project), got %d: %v", len(lines), lines)
	}
	if !strings.Contains(lines[0], "#") {
		t.Errorf("line 0 (id) should contain '#': %q", lines[0])
	}
	if !strings.Contains(lines[1], "An ordinary") {
		t.Errorf("line 1 (title) should contain title text: %q", lines[1])
	}
	if !strings.Contains(lines[2], "squash-ide") {
		t.Errorf("line 2 (project) should contain project name: %q", lines[2])
	}
	for i, line := range lines {
		if w := lipgloss.Width(line); w > CompactListWidth {
			t.Errorf("line %d width %d exceeds %d: %q", i, w, CompactListWidth, line)
		}
	}
}

func TestRenderCard_CompactActive_FourLines(t *testing.T) {
	tk := task.Task{
		ID: "T-042", Type: "feature", Title: "An active task with a long title",
		Project: "squash-ide", Status: "active",
	}
	lines := renderCard(tk, false, CompactListWidth, nil, true)
	if len(lines) != 4 {
		t.Fatalf("compact active card: expected 4 lines (badge/id/title/project), got %d: %v",
			len(lines), lines)
	}
	if !strings.Contains(lines[1], "#") {
		t.Errorf("line 1 (id) should contain '#': %q", lines[1])
	}
	// Title is truncated; check for the first word rather than the full string.
	if !strings.Contains(lines[2], "An") {
		t.Errorf("line 2 (title) should contain title text: %q", lines[2])
	}
	if !strings.Contains(lines[3], "squash-ide") {
		t.Errorf("line 3 (project) should contain project name: %q", lines[3])
	}
	for i, line := range lines {
		if w := lipgloss.Width(line); w > CompactListWidth {
			t.Errorf("line %d width %d exceeds %d: %q", i, w, CompactListWidth, line)
		}
	}
}

// TestRenderCard_CompactIsOneLineTallerThanExpanded locks in the height
// contract from T-030: compact cards are exactly 1 row taller than
// expanded cards for the same task status.
func TestRenderCard_CompactIsOneLineTallerThanExpanded(t *testing.T) {
	cases := []struct {
		name   string
		status string
	}{
		{"backlog", "backlog"},
		{"active", "active"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tk := task.Task{
				ID: "T-042", Type: "feature", Title: "Normal title",
				Project: "squash-ide", Status: tc.status,
			}
			compactLines := renderCard(tk, false, CompactListWidth, nil, true)
			expandedLines := renderCard(tk, false, 60, nil, false)
			if got, want := len(compactLines), len(expandedLines)+1; got != want {
				t.Errorf("compact should be exactly 1 line taller than expanded: compact=%d expanded=%d",
					len(compactLines), len(expandedLines))
			}
		})
	}
}

func TestRenderCard_NonCompact_PreservesProjectLine(t *testing.T) {
	tk := task.Task{
		ID: "T-042", Type: "feature", Title: "Normal title",
		Project: "squash-ide", Status: "backlog",
	}
	compactLines := renderCard(tk, false, CompactListWidth, nil, true)
	normalLines := renderCard(tk, false, 60, nil, false)

	// Post-T-030 the relationship inverts: compact is now taller than
	// non-compact because id and title each get their own row.
	if len(normalLines) >= len(compactLines) {
		t.Errorf("compact should produce more lines than non-compact; got compact=%d normal=%d",
			len(compactLines), len(normalLines))
	}
	// Non-compact must still show the project on its dedicated line.
	joined := strings.Join(normalLines, "\n")
	if !strings.Contains(joined, "squash-ide") {
		t.Errorf("non-compact card should include project name: %s", joined)
	}
}

func TestRenderCard_CompactTruncatesLongTitle(t *testing.T) {
	tk := task.Task{
		ID:      "T-042",
		Type:    "feature",
		Title:   "A title that is far too long to possibly fit a twenty column compact card render",
		Project: "squash-ide",
		Status:  "backlog",
	}
	lines := renderCard(tk, false, CompactListWidth, nil, true)
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines for compact backlog, got %d", len(lines))
	}
	// Title is on line 1 now (id/title/project).
	if w := lipgloss.Width(lines[1]); w > CompactListWidth {
		t.Errorf("title truncation failed: line 1 width %d exceeds %d: %q",
			w, CompactListWidth, lines[1])
	}
	// And every line stays inside the budget.
	for i, line := range lines {
		if w := lipgloss.Width(line); w > CompactListWidth {
			t.Errorf("line %d width %d exceeds %d: %q", i, w, CompactListWidth, line)
		}
	}
}

func TestRenderCard_CompactTruncatesLongProject(t *testing.T) {
	// merton-planning-rag is 19 cols — fits inside innerW=17 only after
	// clipping to "merton-planni…" (17 cols).
	tk := task.Task{
		ID: "T-042", Type: "feature", Title: "short",
		Project: "merton-planning-rag", Status: "backlog",
	}
	lines := renderCard(tk, false, CompactListWidth, nil, true)
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d", len(lines))
	}
	if w := lipgloss.Width(lines[2]); w > CompactListWidth {
		t.Errorf("project truncation failed: line 2 width %d exceeds %d: %q",
			w, CompactListWidth, lines[2])
	}
}

func TestRenderCard_CompactSelected_CursorStripeAllLines(t *testing.T) {
	tk := task.Task{
		ID: "T-042", Type: "feature", Title: "Selected title",
		Project: "squash-ide", Status: "active",
	}
	lines := renderCard(tk, true, CompactListWidth, nil, true)
	if len(lines) != 4 {
		t.Fatalf("expected 4 lines for selected compact active, got %d", len(lines))
	}
	for i, line := range lines {
		if !strings.Contains(line, "▍") {
			t.Errorf("line %d missing cursor stripe ▍: %q", i, line)
		}
	}
}

func TestRenderCard_Compact_EmptyTitle(t *testing.T) {
	tk := task.Task{
		ID: "T-042", Type: "feature", Title: "",
		Project: "squash-ide", Status: "backlog",
	}
	lines := renderCard(tk, false, CompactListWidth, nil, true)
	if len(lines) != 3 {
		t.Fatalf("empty title should not collapse card: expected 3 lines, got %d: %v", len(lines), lines)
	}
}

func TestRenderCard_Compact_EmptyProject(t *testing.T) {
	tk := task.Task{
		ID: "T-042", Type: "feature", Title: "Title",
		Project: "", Status: "active",
	}
	lines := renderCard(tk, false, CompactListWidth, nil, true)
	if len(lines) != 4 {
		t.Fatalf("empty project should not collapse card: expected 4 lines, got %d: %v", len(lines), lines)
	}
}

func TestRenderCard_CompactEastAsianWidth(t *testing.T) {
	// Each CJK char is double-width; truncate() must respect lipgloss.Width.
	tk := task.Task{
		ID: "T-042", Type: "feature",
		Title:   "日本語タイトル日本語タイトル日本語タイトル",
		Project: "p", Status: "backlog",
	}
	lines := renderCard(tk, false, CompactListWidth, nil, true)
	for i, line := range lines {
		if w := lipgloss.Width(line); w > CompactListWidth {
			t.Errorf("line %d width %d exceeds %d: %q", i, w, CompactListWidth, line)
		}
	}
}

func TestListViewRender_CompactSelectsNarrowWidth(t *testing.T) {
	m := compactModel(200, 3) // triggers compact
	if !m.isCompact() {
		t.Fatal("precondition: should be compact")
	}
	out := m.listViewRender()
	for i, line := range strings.Split(out, "\n") {
		if w := lipgloss.Width(line); w > CompactListWidth {
			t.Errorf("listViewRender line %d width %d exceeds %d: %q",
				i, w, CompactListWidth, line)
		}
	}
}

func TestListViewRender_NonCompactKeepsNormalWidth(t *testing.T) {
	// Wide terminal + 3 active — predicate fails on the width arm, so render
	// should use the normal TUIWidth=60 budget.
	m := compactModel(400, 3)
	if m.isCompact() {
		t.Fatal("precondition: should not be compact at width=400")
	}
	out := m.listViewRender()
	// Any line width at 20 in a 60-col budget would indicate compact
	// leaked in. Instead, assert that some content is wider than 20.
	maxW := 0
	for _, line := range strings.Split(out, "\n") {
		if w := lipgloss.Width(line); w > maxW {
			maxW = w
		}
	}
	if maxW <= CompactListWidth {
		t.Errorf("expected normal render to use >%d cols, max line width was %d",
			CompactListWidth, maxW)
	}
}

func TestListViewRender_SingleActiveNotCompact(t *testing.T) {
	// Narrow terminal but only 1 active — activeCount arm fails.
	m := compactModel(200, 1)
	if m.isCompact() {
		t.Fatal("expected not-compact with only 1 active task")
	}
	out := m.listViewRender()
	maxW := 0
	for _, line := range strings.Split(out, "\n") {
		if w := lipgloss.Width(line); w > maxW {
			maxW = w
		}
	}
	if maxW <= CompactListWidth {
		t.Errorf("expected non-compact render width > %d, got %d",
			CompactListWidth, maxW)
	}
}

// tmuxRecorder installs a runOutFn that captures every tmux call. Tests
// inspect its `calls` slice to assert the expected resize-pane shape.
//
// Set windowWidth before invoking the code under test to stub
// `display-message ... #{window_width}` — the tmux call refreshWindowWidth
// makes. A zero windowWidth leaves the call unanswered (returns "" which
// makes WindowWidth error out, mirroring a transient tmux failure).
type tmuxRecorder struct {
	calls       []string
	windowWidth int
}

func (r *tmuxRecorder) fn(name string, args ...string) (string, error) {
	r.calls = append(r.calls, name+" "+strings.Join(args, " "))
	if r.windowWidth > 0 && len(args) > 0 && args[0] == "display-message" {
		for _, a := range args {
			if a == "#{window_width}" {
				return itoa(r.windowWidth) + "\n", nil
			}
		}
	}
	return "", nil
}

// tmuxFixture installs a fake tmux runner and sets the TMUX / TMUX_PANE
// env vars so code paths gated on tmux.InSession() and CurrentPaneID()
// take their real branches under test.
func tmuxFixture(t *testing.T, pane string) *tmuxRecorder {
	t.Helper()
	r := &tmuxRecorder{}
	prev := tmux.SetRunOutFn(r.fn)
	t.Cleanup(func() { tmux.SetRunOutFn(prev) })
	t.Setenv("TMUX", "/tmp/tmux-sock,1,0")
	t.Setenv("TMUX_PANE", pane)
	return r
}

// countResizes returns how many "tmux resize-pane ... -x <width>" calls
// the recorder captured against the given pane.
func countResizes(r *tmuxRecorder, pane string, width int) int {
	needle := "resize-pane -t " + pane + " "
	widthFlag := "-x " + itoa(width)
	n := 0
	for _, c := range r.calls {
		if strings.Contains(c, needle) && strings.Contains(c, widthFlag) {
			n++
		}
	}
	return n
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func TestCheckCompactPane_EntersCompactOnResize(t *testing.T) {
	r := tmuxFixture(t, "%1")

	m := compactModel(400, 3) // wide — not compact yet
	if m.isCompact() {
		t.Fatal("precondition: not compact at width=400")
	}
	m.compact = false

	// Shrink the outer terminal to a narrow width — tmux will report the
	// new window width via display-message on the next refresh.
	r.windowWidth = 200
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 200, Height: 50})
	u := updated.(Model)

	if !u.compact {
		t.Error("expected m.compact to flip true after narrowing")
	}
	if n := countResizes(r, "%1", CompactListWidth); n != 1 {
		t.Errorf("expected one resize-pane to %d, got %d (calls: %v)",
			CompactListWidth, n, r.calls)
	}
}

func TestCheckCompactPane_ExitsCompactOnWiden(t *testing.T) {
	r := tmuxFixture(t, "%1")

	m := compactModel(200, 3)
	m.compact = true // start in compact
	if !m.isCompact() {
		t.Fatal("precondition: should be compact at width=200")
	}

	// Outer terminal widened — tmux reports the new window width.
	r.windowWidth = 400
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 400, Height: 50})
	u := updated.(Model)

	if u.compact {
		t.Error("expected m.compact to flip false after widening")
	}
	// Default TUIWidth is 60.
	if n := countResizes(r, "%1", 60); n != 1 {
		t.Errorf("expected one resize-pane back to 60, got %d (calls: %v)",
			n, r.calls)
	}
}

func TestCheckCompactPane_IdempotentWithoutTransition(t *testing.T) {
	r := tmuxFixture(t, "%1")
	r.windowWidth = 200

	m := compactModel(200, 3)
	m.compact = true

	// Second WindowSizeMsg at the same width — no state change.
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 200, Height: 50})
	u := updated.(Model)

	if !u.compact {
		t.Error("compact state should remain true")
	}
	if countResizes(r, "%1", CompactListWidth) != 0 {
		t.Errorf("expected no resize-pane calls for idempotent update, got calls: %v", r.calls)
	}
	if countResizes(r, "%1", 60) != 0 {
		t.Errorf("expected no resize-pane-to-60 calls, got calls: %v", r.calls)
	}
}

func TestCheckCompactPane_DialogExpandsPane(t *testing.T) {
	r := tmuxFixture(t, "%1")
	r.windowWidth = 200

	m := compactModel(200, 3)
	m.compact = true

	// Opening the spawn-confirm dialog via the key handler should flip
	// isCompact() to false and resize the pane back to TUIWidth.
	//
	// The cursor in compactModel sits on an active task; to reach a
	// backlog task we move the cursor past the active ones.
	for i := 0; i < 10; i++ {
		if !m.filtered[m.cursor].isHeader && !m.filtered[m.cursor].isPlaceholder {
			if m.filtered[m.cursor].task.Status == "backlog" {
				break
			}
		}
		m.moveCursor(1)
	}
	if m.filtered[m.cursor].task.Status != "backlog" {
		t.Fatalf("could not position cursor on a backlog task (got %q)",
			m.filtered[m.cursor].task.Status)
	}

	updated, _ := m.Update(enterKeyMsg())
	u := updated.(Model)

	if u.confirming == nil {
		t.Fatal("expected confirm dialog to open")
	}
	if u.compact {
		t.Error("compact should flip false while dialog is open")
	}
	if n := countResizes(r, "%1", 60); n != 1 {
		t.Errorf("expected one resize-pane back to 60 on dialog open, got %d (calls: %v)",
			n, r.calls)
	}
}

func TestCheckCompactPane_NoOpOutsideTmux(t *testing.T) {
	// TMUX unset — InSession() false — the Update gate should short-circuit
	// and no tmux calls should be made.
	t.Setenv("TMUX", "")
	t.Setenv("TMUX_PANE", "")
	r := &tmuxRecorder{}
	prev := tmux.SetRunOutFn(r.fn)
	t.Cleanup(func() { tmux.SetRunOutFn(prev) })

	m := compactModel(200, 3)
	_, _ = m.Update(tea.WindowSizeMsg{Width: 200, Height: 50})

	if len(r.calls) != 0 {
		t.Errorf("expected zero tmux calls outside tmux session, got: %v", r.calls)
	}
}

// TestCheckCompactPane_ReleasesWhenPanePinnedButWindowWidens is the T-028
// regression. Pre-fix, isCompact keyed on m.width — once tmux pinned the
// pane to CompactListWidth (m.width=20), the predicate stayed true even
// after the user widened the outer terminal, and compact never released.
//
// Post-fix, refreshWindowWidth pulls the outer window width from tmux on
// every checkCompactPane call, so the predicate sees the widened terminal
// and flips compact off — even though m.width is still pinned at 20.
func TestCheckCompactPane_ReleasesWhenPanePinnedButWindowWidens(t *testing.T) {
	r := tmuxFixture(t, "%1")

	// Start in compact, pane pinned at CompactListWidth (mirrors what
	// tmux leaves m.width at after the resize-pane that engaged compact).
	m := compactModel(CompactListWidth, 3)
	m.compact = true
	if !m.isCompact() {
		t.Fatal("precondition: should be compact at narrow width")
	}

	// User widens the outer terminal. tmux does NOT deliver a
	// WindowSizeMsg (the pane stays pinned at 20), but the next
	// checkCompactPane call must still release.
	r.windowWidth = 400
	m.checkCompactPane("%1")

	if m.compact {
		t.Error("expected compact to release when windowWidth widened past threshold")
	}
	if n := countResizes(r, "%1", 60); n != 1 {
		t.Errorf("expected one resize-pane back to 60 on release, got %d (calls: %v)",
			n, r.calls)
	}
}

// TestCheckCompactPane_KeystrokeReleasesAfterWiden proves the keystroke
// release path works end-to-end through the Update → handleKey flow when
// no WindowSizeMsg is delivered. This is the user-visible recovery path
// described in the T-028 acceptance criteria ("press any key after
// widening the terminal").
func TestCheckCompactPane_KeystrokeReleasesAfterWiden(t *testing.T) {
	r := tmuxFixture(t, "%1")

	m := compactModel(CompactListWidth, 3)
	m.compact = true

	// Outer terminal widened; pane still pinned at 20 (m.width unchanged).
	r.windowWidth = 400

	// Any keystroke routed through Update → handleKey ends in
	// checkCompactPane, which now reads the widened window width.
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	u := updated.(Model)

	if u.compact {
		t.Error("expected compact to release after keystroke once windowWidth widened")
	}
	if n := countResizes(r, "%1", 60); n != 1 {
		t.Errorf("expected one resize-pane back to 60 on keystroke release, got %d (calls: %v)",
			n, r.calls)
	}
}

// TestRefreshWindowWidth_ErrorsAreSwallowed asserts the cosmetic-failure
// pattern from compact.go's package comment: a tmux read failure must not
// flip the predicate or panic — the previous cached value stands. Mirrors
// the swallow-on-error contract used for tmux.ResizePane.
func TestRefreshWindowWidth_ErrorsAreSwallowed(t *testing.T) {
	// tmuxFixture installs a runner that returns "" for every call when
	// windowWidth is left at 0; WindowWidth's strconv.Atoi then fails,
	// driving the error path.
	_ = tmuxFixture(t, "%1")

	m := compactModel(200, 3)
	m.windowWidth = 250 // pre-existing cached value
	m.refreshWindowWidth("%1")

	if m.windowWidth != 250 {
		t.Errorf("expected windowWidth to remain at 250 on tmux error, got %d", m.windowWidth)
	}
}

func TestListViewRender_CompactDisabledInDialog(t *testing.T) {
	m := compactModel(200, 3)
	tk := task.Task{ID: "T-001"}
	m.confirming = &tk
	if m.isCompact() {
		t.Fatal("compact should stand down while dialog is open")
	}
	out := m.listViewRender()
	// The full spawn prompt text must render (it doesn't fit in compact).
	if !strings.Contains(out, "Spawn T-001?") {
		t.Errorf("expected full spawn prompt in output, got: %s", out)
	}
}
