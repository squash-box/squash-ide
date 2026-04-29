package tmux

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
)

// recorder swaps in for runOutFn so tests can match tmux calls by args.
type recorder struct {
	responses map[string]response
	calls     []call
}

type response struct {
	out string
	err error
}

type call struct {
	name string
	args []string
}

func (r *recorder) fn(name string, args ...string) (string, error) {
	r.calls = append(r.calls, call{name: name, args: append([]string(nil), args...)})
	key := name + " " + strings.Join(args, " ")
	if resp, ok := r.responses[key]; ok {
		return resp.out, resp.err
	}
	return "", nil
}

func (r *recorder) respond(cmd, out string, err error) {
	if r.responses == nil {
		r.responses = make(map[string]response)
	}
	r.responses[cmd] = response{out: out, err: err}
}

func newRecorder(t *testing.T) *recorder {
	r := &recorder{}
	prev := SetRunOutFn(r.fn)
	t.Cleanup(func() { SetRunOutFn(prev) })
	return r
}

func TestAvailable(t *testing.T) {
	// Pure stdlib — real behaviour depends on PATH. We just assert the
	// function runs and returns a bool without panicking.
	_ = Available()
}

func TestInSession(t *testing.T) {
	t.Setenv("TMUX", "")
	if InSession() {
		t.Error("expected not-in-session when TMUX is empty")
	}
	t.Setenv("TMUX", "/tmp/tmux-sock,123,4")
	if !InSession() {
		t.Error("expected in-session when TMUX is set")
	}
}

func TestCurrentPaneID(t *testing.T) {
	t.Setenv("TMUX_PANE", "%7")
	if got := CurrentPaneID(); got != "%7" {
		t.Errorf("got %q, want %%7", got)
	}
}

func TestListWindowPanes_ParsesFields(t *testing.T) {
	r := newRecorder(t)
	r.respond("tmux list-panes -F #{pane_id} #{pane_left} #{pane_width} #{pane_height} -t %1",
		"%1 0 60 40\n%2 60 80 40\n%3 140 80 40\n", nil)

	panes, err := ListWindowPanes("%1")
	if err != nil {
		t.Fatal(err)
	}
	if len(panes) != 3 {
		t.Fatalf("got %d panes, want 3", len(panes))
	}
	if panes[0].ID != "%1" || panes[0].Width != 60 {
		t.Errorf("pane[0]: %#v", panes[0])
	}
}

func TestListWindowPanes_MalformedLine(t *testing.T) {
	r := newRecorder(t)
	r.respond("tmux list-panes -F #{pane_id} #{pane_left} #{pane_width} #{pane_height} -t %1",
		"%1 0 60\n", nil) // missing height

	_, err := ListWindowPanes("%1")
	if err == nil {
		t.Fatal("expected err on malformed line")
	}
}

func TestWindowWidth_Parses(t *testing.T) {
	r := newRecorder(t)
	r.respond("tmux display-message -p -t %1 #{window_width}", "240\n", nil)

	w, err := WindowWidth("%1")
	if err != nil {
		t.Fatal(err)
	}
	if w != 240 {
		t.Errorf("got %d", w)
	}
}

func TestWindowWidth_InvalidOutput(t *testing.T) {
	r := newRecorder(t)
	r.respond("tmux display-message -p -t %1 #{window_width}", "nope\n", nil)
	_, err := WindowWidth("%1")
	if err == nil {
		t.Fatal("expected parse err")
	}
}

func TestSplitRight_BuildsCommand(t *testing.T) {
	r := newRecorder(t)
	r.respond("tmux split-window -h -t %1 -c /tmp/x -P -F #{pane_id} claude", "%4\n", nil)

	id, err := SplitRight("%1", "/tmp/x", "claude")
	if err != nil {
		t.Fatal(err)
	}
	if id != "%4" {
		t.Errorf("got %q", id)
	}
}

func TestSplitRight_RequiresTarget(t *testing.T) {
	_, err := SplitRight("", "/tmp/x", "claude")
	if err == nil {
		t.Fatal("expected err on empty target")
	}
}

func TestSplitRight_OmitsEmptyCwd(t *testing.T) {
	r := newRecorder(t)
	// Note: -c is NOT present in expected args.
	r.respond("tmux split-window -h -t %1 -P -F #{pane_id} claude", "%4\n", nil)

	if _, err := SplitRight("%1", "", "claude"); err != nil {
		t.Fatal(err)
	}
}

func TestResizePane_BuildsCommand(t *testing.T) {
	r := newRecorder(t)
	r.respond("tmux resize-pane -t %2 -x 80", "", nil)

	if err := ResizePane("%2", 80); err != nil {
		t.Fatal(err)
	}
}

func TestResizePane_ValidatesArgs(t *testing.T) {
	if err := ResizePane("", 80); err == nil {
		t.Error("expected err for empty pane")
	}
	if err := ResizePane("%1", 0); err == nil {
		t.Error("expected err for zero width")
	}
}

func TestReTile_NoPanes_PinsTUI(t *testing.T) {
	r := newRecorder(t)
	r.respond("tmux display-message -p -t %1 #{window_width}", "200\n", nil)
	r.respond("tmux list-panes -F #{pane_id} #{pane_left} #{pane_width} #{pane_height} -t %1",
		"%1 0 60 40\n", nil) // only the TUI
	r.respond("tmux resize-pane -t %1 -x 60", "", nil)

	widths, err := ReTile("%1", 60, 80, 80)
	if err != nil {
		t.Fatalf("ReTile: %v", err)
	}
	if widths != nil {
		t.Errorf("expected nil widths for no-pane case, got %v", widths)
	}
}

func TestReTile_RequiresTUI(t *testing.T) {
	if _, err := ReTile("", 60, 80, 80); err == nil {
		t.Error("expected err for empty tui pane id")
	}
}

func TestRightmostRightPaneID(t *testing.T) {
	r := newRecorder(t)
	r.respond("tmux list-panes -F #{pane_id} #{pane_left} #{pane_width} #{pane_height} -t %1",
		"%1 0 60 40\n%2 60 80 40\n%3 140 80 40\n", nil)

	got, err := RightmostRightPaneID("%1")
	if err != nil {
		t.Fatal(err)
	}
	if got != "%3" {
		t.Errorf("got %q", got)
	}
}

func TestRightmostRightPaneID_OnlyTUI(t *testing.T) {
	r := newRecorder(t)
	r.respond("tmux list-panes -F #{pane_id} #{pane_left} #{pane_width} #{pane_height} -t %1",
		"%1 0 60 40\n", nil)

	got, _ := RightmostRightPaneID("%1")
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestSetPaneRole_Empty(t *testing.T) {
	if err := SetPaneRole("", RoleTUI); err == nil {
		t.Error("expected err for empty pane id")
	}
}

func TestFindPaneByRole_Matches(t *testing.T) {
	r := newRecorder(t)
	r.respond("tmux list-panes -t %1 -F #{pane_id} #{@squash-role}",
		"%1 tui\n%2 placeholder\n%3 \n", nil)

	got, err := FindPaneByRole("%1", RolePlaceholder)
	if err != nil {
		t.Fatal(err)
	}
	if got != "%2" {
		t.Errorf("got %q", got)
	}
}

func TestFindPaneByRole_NoMatch(t *testing.T) {
	r := newRecorder(t)
	r.respond("tmux list-panes -t %1 -F #{pane_id} #{@squash-role}", "%1 tui\n", nil)
	got, err := FindPaneByRole("%1", RolePlaceholder)
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestFindPaneByTask_Matches(t *testing.T) {
	r := newRecorder(t)
	r.respond("tmux list-panes -t %1 -F #{pane_id} #{@squash-task}",
		"%1 \n%2 T-007\n%3 T-008\n", nil)

	got, err := FindPaneByTask("%1", "T-008")
	if err != nil {
		t.Fatal(err)
	}
	if got != "%3" {
		t.Errorf("got %q", got)
	}
}

func TestCountPanesByOption(t *testing.T) {
	r := newRecorder(t)
	r.respond("tmux list-panes -t %1 -F #{pane_id} #{@squash-task}",
		"%1 \n%2 T-007\n%3 T-008\n%4 T-009\n", nil)

	n, err := CountPanesByOption("%1", "@squash-task")
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Errorf("got %d, want 3", n)
	}
}

func TestCurrentSessionName_Success(t *testing.T) {
	r := newRecorder(t)
	r.respond("tmux display-message -p #S", "squash-ide\n", nil)
	if got := CurrentSessionName(); got != "squash-ide" {
		t.Errorf("got %q", got)
	}
}

func TestCurrentSessionName_OnError(t *testing.T) {
	r := newRecorder(t)
	r.respond("tmux display-message -p #S", "", errors.New("not in tmux"))
	if got := CurrentSessionName(); got != "" {
		t.Errorf("expected empty on error, got %q", got)
	}
}

func TestKillSession_EmptyName(t *testing.T) {
	if err := KillSession(""); err == nil {
		t.Error("expected err for empty name")
	}
}

func TestGetPaneOption_Empty(t *testing.T) {
	got, err := GetPaneOption("", "x")
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Errorf("got %q", got)
	}
}

func TestSetPaneOption_ErrorsOnEmpty(t *testing.T) {
	if err := SetPaneOption("", "x", "y"); err == nil {
		t.Error("expected err")
	}
}

func TestSetPaneRemainOnExit_BuildsCommand(t *testing.T) {
	r := newRecorder(t)
	r.respond("tmux set-option -pt %5 remain-on-exit on", "", nil)

	if err := SetPaneRemainOnExit("%5"); err != nil {
		t.Fatal(err)
	}
	if len(r.calls) != 1 {
		t.Fatalf("got %d calls, want 1: %#v", len(r.calls), r.calls)
	}
	wantArgs := []string{"set-option", "-pt", "%5", "remain-on-exit", "on"}
	if got := r.calls[0]; got.name != "tmux" || strings.Join(got.args, " ") != strings.Join(wantArgs, " ") {
		t.Errorf("got call %v, want tmux %v", got, wantArgs)
	}
}

func TestSetPaneRemainOnExit_ErrorsOnEmpty(t *testing.T) {
	if err := SetPaneRemainOnExit(""); err == nil {
		t.Error("expected err on empty pane id")
	} else if !strings.Contains(err.Error(), "pane id required") {
		t.Errorf("unexpected err: %v", err)
	}
}

func TestSetPaneRemainOnExit_WrapsTmuxError(t *testing.T) {
	r := newRecorder(t)
	r.respond("tmux set-option -pt %5 remain-on-exit on", "", errors.New("tmux: no such pane"))

	err := SetPaneRemainOnExit("%5")
	if err == nil {
		t.Fatal("expected err")
	}
	if !strings.Contains(err.Error(), "%5") {
		t.Errorf("err should include pane id, got: %v", err)
	}
	if !strings.Contains(err.Error(), "no such pane") {
		t.Errorf("err should wrap underlying tmux err, got: %v", err)
	}
}

func TestSetPaneTask_NoOpOnEmpty(t *testing.T) {
	if err := SetPaneTask("", "T-001"); err != nil {
		t.Error(err)
	}
	if err := SetPaneTask("%1", ""); err != nil {
		t.Error(err)
	}
}

func TestFindPaneByTask_NoOpOnEmpty(t *testing.T) {
	got, err := FindPaneByTask("", "T-001")
	if err != nil || got != "" {
		t.Errorf("got %q, %v", got, err)
	}
}

// Just to exercise `fmt` import used in the test file for Sprintf of wanted cmd keys.
var _ = fmt.Sprintf
var _ = os.Setenv
