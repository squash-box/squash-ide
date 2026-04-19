package spawner

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/squashbox/squash-ide/internal/config"
	"github.com/squashbox/squash-ide/internal/testutil/fakerunner"
	"github.com/squashbox/squash-ide/internal/tmux"
)

func swapRunner(t *testing.T, fake *fakerunner.Runner) {
	t.Helper()
	prev := SetRunner(fake)
	t.Cleanup(func() { SetRunner(prev) })
}

func TestTaskBorderFormat_Default(t *testing.T) {
	s := TaskBorderFormat("T-001", "hello", "squash-ide")
	if !strings.Contains(s, "T-001") || !strings.Contains(s, "hello") {
		t.Errorf("unexpected format: %q", s)
	}
	if !strings.Contains(s, "WORKING") {
		t.Errorf("default state should be working, got %q", s)
	}
}

func TestTaskBorderFormatWithState_AllStates(t *testing.T) {
	cases := map[string]string{
		"working":        "WORKING",
		"idle":           "IDLE",
		"input_required": "INPUT REQUIRED",
		"testing":        "TESTING",
	}
	for state, want := range cases {
		got := TaskBorderFormatWithState("T-001", "t", "p", state)
		if !strings.Contains(got, want) {
			t.Errorf("state=%q: want badge %q, got %q", state, want, got)
		}
	}
}

func TestTaskBorderFormat_TruncatesLongTitle(t *testing.T) {
	long := strings.Repeat("x", 80)
	s := TaskBorderFormat("T-001", long, "proj")
	// Title is truncated to 30 (27 + "..."). The rendering line contains
	// various formatting codes, so just assert the full 80-char title is
	// not present.
	if strings.Contains(s, long) {
		t.Error("expected long title to be truncated")
	}
}

func TestRunConfigured_Happy(t *testing.T) {
	r := fakerunner.New(t)
	swapRunner(t, r)
	r.ExpectLookPath("my-term").ReturnsLookPath("/usr/bin/my-term")
	r.Expect("/usr/bin/my-term", "--workdir=/tmp/x", "--", "claude")

	err := runConfigured(
		config.Terminal{Command: "my-term", Args: []string{"--workdir={cwd}", "--", "{exec}"}},
		map[string]string{"cwd": "/tmp/x", "exec": "claude"},
	)
	if err != nil {
		t.Fatalf("runConfigured: %v", err)
	}
}

func TestRunConfigured_LookPathFails(t *testing.T) {
	r := fakerunner.New(t)
	swapRunner(t, r)
	r.ExpectLookPath("ghost-term").ReturnsLookPath("")

	err := runConfigured(
		config.Terminal{Command: "ghost-term", Args: []string{"x"}},
		map[string]string{},
	)
	if err == nil {
		t.Fatal("expected err")
	}
	if !strings.Contains(err.Error(), "ghost-term") {
		t.Errorf("should mention binary: %v", err)
	}
}

func TestRunConfigured_StartFails(t *testing.T) {
	r := fakerunner.New(t)
	swapRunner(t, r)
	r.ExpectLookPath("term").ReturnsLookPath("/usr/bin/term")
	r.Expect("/usr/bin/term", "arg").ReturnsExitErr(errors.New("boom"))

	err := runConfigured(
		config.Terminal{Command: "term", Args: []string{"arg"}},
		map[string]string{},
	)
	if err == nil {
		t.Fatal("expected err")
	}
}

func TestRunAutoDetect_UsesFirstProbe(t *testing.T) {
	r := fakerunner.New(t)
	swapRunner(t, r)
	// First probe (ptyxis) succeeds.
	r.ExpectLookPath("ptyxis").ReturnsLookPath("/usr/bin/ptyxis")
	r.Expect("/usr/bin/ptyxis", "-d", "/tmp/wd", "--", "bash", "-c", "claude")

	if err := runAutoDetect("/tmp/wd", "claude"); err != nil {
		t.Fatalf("runAutoDetect: %v", err)
	}
}

func TestRunAutoDetect_FallsBackOnLookupFail(t *testing.T) {
	r := fakerunner.New(t)
	swapRunner(t, r)
	r.ExpectLookPath("ptyxis").ReturnsLookPath("")
	r.ExpectLookPath("gnome-terminal").ReturnsLookPath("/usr/bin/gnome-terminal")
	r.Expect("/usr/bin/gnome-terminal", "--working-directory=/tmp/wd", "--", "bash", "-c", "claude")

	if err := runAutoDetect("/tmp/wd", "claude"); err != nil {
		t.Fatalf("runAutoDetect: %v", err)
	}
}

func TestRunAutoDetect_NoTerminalsFound(t *testing.T) {
	r := fakerunner.New(t)
	swapRunner(t, r)
	r.ExpectLookPath("ptyxis").ReturnsLookPath("")
	r.ExpectLookPath("gnome-terminal").ReturnsLookPath("")
	r.ExpectLookPath("x-terminal-emulator").ReturnsLookPath("")

	err := runAutoDetect("/tmp/wd", "claude")
	if err == nil {
		t.Fatal("expected err")
	}
	if !strings.Contains(err.Error(), "no supported terminal") {
		t.Errorf("expected no-terminal error, got: %v", err)
	}
}

func TestSpawnWith_NoTmux_RoutesToConfigured(t *testing.T) {
	r := fakerunner.New(t)
	swapRunner(t, r)
	r.ExpectLookPath("customterm").ReturnsLookPath("/usr/bin/customterm")
	r.Expect("/usr/bin/customterm", "--", "claude '/implement T-001'")

	cfg := config.Config{
		Tmux:     config.Tmux{Enabled: false},
		Terminal: config.Terminal{Command: "customterm", Args: []string{"--", "{exec}"}},
		Spawn:    config.Spawn{Command: "claude", Args: []string{"/implement {task_id}"}},
	}

	err := SpawnWith(cfg, map[string]string{
		"cwd":     "/tmp/x",
		"task_id": "T-001",
	})
	if err != nil {
		t.Fatalf("SpawnWith: %v", err)
	}
}

// tmuxRecorder stubs tmux.runOutFn for runTmux tests. It records each tmux
// invocation as a single "name args..." line and answers a fixed set of
// queries (list-panes, display-message, split-window) so runTmux can drive
// SplitRight + ReTile without a real tmux server.
type tmuxRecorder struct {
	calls []string
	// newPaneID is what split-window returns.
	newPaneID string
	// windowWidth answered for display-message #{window_width}.
	windowWidth int
	// placeholderPane is returned by FindPaneByRole(placeholder) when set.
	placeholderPane string
	// rightPanes is appended to ListWindowPanes output (each line is
	// "<pane_id> <left> <width> <height>"). The TUI pane is always added by
	// the helper using the configured tuiPaneID.
	rightPanes []string
	// errOn maps a substring of the call string to an error to return.
	errOn map[string]error
	// tuiPaneID seeds list-panes responses with the TUI pane row.
	tuiPaneID string
}

func (r *tmuxRecorder) fn(name string, args ...string) (string, error) {
	line := name + " " + strings.Join(args, " ")
	r.calls = append(r.calls, line)
	for needle, err := range r.errOn {
		if strings.Contains(line, needle) {
			return "", err
		}
	}
	if name != "tmux" || len(args) == 0 {
		return "", nil
	}
	switch args[0] {
	case "list-panes":
		// FindPaneByRole queries `-F #{pane_id} #{@squash-role}`.
		if containsArg(args, "#{pane_id} #{@squash-role}") {
			if r.placeholderPane != "" {
				return r.placeholderPane + " placeholder\n", nil
			}
			return "", nil
		}
		// ListWindowPanes queries `-F #{pane_id} #{pane_left} #{pane_width} #{pane_height}`.
		if containsArg(args, "#{pane_id} #{pane_left} #{pane_width} #{pane_height}") {
			out := r.tuiPaneID + " 0 60 40\n"
			for _, p := range r.rightPanes {
				out += p + "\n"
			}
			return out, nil
		}
		return "", nil
	case "display-message":
		if containsArg(args, "#{window_width}") {
			if r.windowWidth > 0 {
				return fmt.Sprintf("%d\n", r.windowWidth), nil
			}
			return "", nil
		}
		return "", nil
	case "split-window":
		return r.newPaneID + "\n", nil
	}
	return "", nil
}

func containsArg(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

func swapTmuxRunOut(t *testing.T, r *tmuxRecorder) {
	t.Helper()
	prev := tmux.SetRunOutFn(r.fn)
	t.Cleanup(func() { tmux.SetRunOutFn(prev) })
}

func lastTmuxCall(r *tmuxRecorder) string {
	if len(r.calls) == 0 {
		return ""
	}
	return r.calls[len(r.calls)-1]
}

func countCalls(r *tmuxRecorder, needle string) int {
	n := 0
	for _, c := range r.calls {
		if strings.Contains(c, needle) {
			n++
		}
	}
	return n
}

func TestRunTmux_RestoresFocusToTUIPaneAfterSpawn(t *testing.T) {
	t.Setenv("TMUX_PANE", "%1")
	r := &tmuxRecorder{
		tuiPaneID:   "%1",
		newPaneID:   "%2",
		windowWidth: 300,
	}
	swapTmuxRunOut(t, r)

	cfg := config.Tmux{TUIWidth: 60, PaneWidth: 0, MinPaneWidth: 80}
	if err := runTmux(cfg, "/tmp/wd", "claude", "T-031", "title", "squash-ide"); err != nil {
		t.Fatalf("runTmux: %v", err)
	}

	want := "tmux select-pane -t %1"
	if last := lastTmuxCall(r); last != want {
		t.Errorf("last tmux call = %q, want %q\nfull call sequence:\n%s",
			last, want, strings.Join(r.calls, "\n"))
	}
}

func TestRunTmux_PreservesFocusAfterPlaceholderReplacement(t *testing.T) {
	t.Setenv("TMUX_PANE", "%1")
	r := &tmuxRecorder{
		tuiPaneID:       "%1",
		newPaneID:       "%9",
		windowWidth:     300,
		placeholderPane: "%5",
	}
	swapTmuxRunOut(t, r)

	cfg := config.Tmux{TUIWidth: 60, PaneWidth: 0, MinPaneWidth: 80}
	if err := runTmux(cfg, "/tmp/wd", "claude", "T-031", "title", "squash-ide"); err != nil {
		t.Fatalf("runTmux: %v", err)
	}

	// Placeholder kill-pane goes through tmux.KillPane → exec.Command
	// directly (separate seam from runOutFn) so it is not observable here;
	// the load-bearing assertion is that the final tmux call is still
	// select-pane on the TUI.
	want := "tmux select-pane -t %1"
	if last := lastTmuxCall(r); last != want {
		t.Errorf("last tmux call = %q, want %q\nfull call sequence:\n%s",
			last, want, strings.Join(r.calls, "\n"))
	}
}

func TestRunTmux_NoSelectPaneOnReTileReject(t *testing.T) {
	t.Setenv("TMUX_PANE", "%1")
	// totalCols=300, tui=60, panes already=3 + new(1)=4: avail = 300-60-4 = 236;
	// minPaneWidth=80 → need 4*80=320 > 236, so Tile rejects.
	r := &tmuxRecorder{
		tuiPaneID:   "%1",
		newPaneID:   "%9",
		windowWidth: 300,
		rightPanes: []string{
			"%2 60 80 40",
			"%3 140 80 40",
			"%4 220 80 40",
		},
	}
	swapTmuxRunOut(t, r)

	cfg := config.Tmux{TUIWidth: 60, PaneWidth: 0, MinPaneWidth: 80}
	err := runTmux(cfg, "/tmp/wd", "claude", "T-031", "title", "squash-ide")
	if err == nil {
		t.Fatal("expected re-tile rejection error")
	}
	if !strings.Contains(err.Error(), "re-tile rejected new pane") {
		t.Errorf("error should wrap re-tile rejection, got: %v", err)
	}
	// killPane on the new pane shells exec.Command directly (separate seam
	// from runOutFn) so it is not observable here. The load-bearing
	// assertion for this test is that select-pane is NOT issued on the
	// reject path — otherwise we risk selecting a pane that is being torn
	// down concurrently.
	if countCalls(r, "select-pane") != 0 {
		t.Errorf("must not select-pane on reject path; calls:\n%s",
			strings.Join(r.calls, "\n"))
	}
}

func TestRunTmux_SelectPaneErrorSwallowed(t *testing.T) {
	t.Setenv("TMUX_PANE", "%1")
	r := &tmuxRecorder{
		tuiPaneID:   "%1",
		newPaneID:   "%2",
		windowWidth: 300,
		errOn: map[string]error{
			"select-pane -t %1": errors.New("target pane vanished"),
		},
	}
	swapTmuxRunOut(t, r)

	cfg := config.Tmux{TUIWidth: 60, PaneWidth: 0, MinPaneWidth: 80}
	if err := runTmux(cfg, "/tmp/wd", "claude", "T-031", "title", "squash-ide"); err != nil {
		t.Fatalf("runTmux must swallow SelectPane errors, got: %v", err)
	}
	if countCalls(r, "select-pane -t %1") != 1 {
		t.Errorf("expected select-pane to fire even when it errors; calls:\n%s",
			strings.Join(r.calls, "\n"))
	}
}

func TestSetRunner_RoundTrip(t *testing.T) {
	orig := runner
	fake := fakerunner.New(t)
	prev := SetRunner(fake)
	if prev != orig {
		t.Error("should return previous runner")
	}
	if runner != fake {
		t.Error("should set to fake")
	}
	SetRunner(orig)
}
