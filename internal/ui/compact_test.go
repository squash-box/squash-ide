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

func TestRenderCard_CompactBacklog_OneLine(t *testing.T) {
	tk := task.Task{
		ID: "T-042", Type: "feature", Title: "An ordinary backlog title",
		Project: "squash-ide", Status: "backlog",
	}
	lines := renderCard(tk, false, CompactListWidth, nil, true)
	if len(lines) != 1 {
		t.Fatalf("compact backlog card: expected 1 line, got %d: %v", len(lines), lines)
	}
	if w := lipgloss.Width(lines[0]); w > CompactListWidth {
		t.Errorf("line width %d exceeds budget %d: %q", w, CompactListWidth, lines[0])
	}
}

func TestRenderCard_CompactActive_TwoLines(t *testing.T) {
	tk := task.Task{
		ID: "T-042", Type: "feature", Title: "An active task with a long title",
		Project: "squash-ide", Status: "active",
	}
	lines := renderCard(tk, false, CompactListWidth, nil, true)
	if len(lines) != 2 {
		t.Fatalf("compact active card: expected 2 lines (badge + title), got %d: %v",
			len(lines), lines)
	}
	// The title line must stay inside the budget; the badge line is
	// lipgloss-styled with padding and may be wider than the literal 20
	// chars of content, but must not exceed the budget.
	for i, line := range lines {
		if w := lipgloss.Width(line); w > CompactListWidth {
			t.Errorf("line %d width %d exceeds %d: %q", i, w, CompactListWidth, line)
		}
	}
}

func TestRenderCard_NonCompact_PreservesProjectLine(t *testing.T) {
	tk := task.Task{
		ID: "T-042", Type: "feature", Title: "Normal title",
		Project: "squash-ide", Status: "backlog",
	}
	compactLines := renderCard(tk, false, CompactListWidth, nil, true)
	normalLines := renderCard(tk, false, 60, nil, false)

	if len(normalLines) <= len(compactLines) {
		t.Errorf("non-compact should produce more lines than compact; got %d vs %d",
			len(normalLines), len(compactLines))
	}
	// Non-compact must show the project on its dedicated line.
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
	if len(lines) != 1 {
		t.Fatalf("expected 1 line for compact backlog, got %d", len(lines))
	}
	if w := lipgloss.Width(lines[0]); w > CompactListWidth {
		t.Errorf("truncation failed: line width %d exceeds %d: %q",
			w, CompactListWidth, lines[0])
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
type tmuxRecorder struct {
	calls []string
}

func (r *tmuxRecorder) fn(name string, args ...string) (string, error) {
	r.calls = append(r.calls, name+" "+strings.Join(args, " "))
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

	// Shrink the terminal to a narrow width with 3 active tasks.
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
