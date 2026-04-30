package main

import (
	"context"
	"strings"
	"testing"

	"github.com/squashbox/squash-ide/internal/config"
	"github.com/squashbox/squash-ide/internal/status"
	"github.com/squashbox/squash-ide/internal/testutil/fakerunner"
	"github.com/squashbox/squash-ide/internal/tmux"
)

// tmuxRecorder is a minimal stand-in for the spawner package's
// tmuxRecorder, scoped to what notify-watch actually issues
// (switch-client, list-panes for FindPaneByTask, select-pane).
type tmuxRecorder struct {
	calls []string
	// listPanesOut is returned for any `tmux list-panes -F #{pane_id} #{@squash-task}` call.
	listPanesOut string
	// errOn maps a substring to an error to return on calls containing it.
	errOn map[string]error
}

func (r *tmuxRecorder) fn(name string, args ...string) (string, error) {
	line := name + " " + strings.Join(args, " ")
	r.calls = append(r.calls, line)
	for needle, err := range r.errOn {
		if strings.Contains(line, needle) {
			return "", err
		}
	}
	if name == "tmux" && len(args) > 0 && args[0] == "list-panes" {
		return r.listPanesOut, nil
	}
	return "", nil
}

func (r *tmuxRecorder) countContains(needle string) int {
	n := 0
	for _, c := range r.calls {
		if strings.Contains(c, needle) {
			n++
		}
	}
	return n
}

func swapTmuxRunOut(t *testing.T, r *tmuxRecorder) {
	t.Helper()
	prev := tmux.SetRunOutFn(r.fn)
	t.Cleanup(func() { tmux.SetRunOutFn(prev) })
}

func swapNotifyRunner(t *testing.T) *fakerunner.Runner {
	t.Helper()
	r := fakerunner.New(t)
	prev := status.NotifyRunner
	status.NotifyRunner = r
	t.Cleanup(func() { status.NotifyRunner = prev })
	return r
}

func tmuxEnabled() config.Config {
	return config.Config{
		Tmux: config.Tmux{
			Enabled:     true,
			SessionName: "squash-ide",
		},
	}
}

func TestFocusTaskPane_HappyPath(t *testing.T) {
	r := &tmuxRecorder{
		listPanesOut: "%1 \n%2 T-007\n%3 T-100\n",
	}
	swapTmuxRunOut(t, r)

	focusTaskPane(tmuxEnabled(), "T-100")

	if r.countContains("switch-client -t squash-ide") != 1 {
		t.Errorf("expected one switch-client call, calls = %v", r.calls)
	}
	if r.countContains("list-panes -t squash-ide:") != 1 {
		t.Errorf("expected one list-panes call against the session, calls = %v", r.calls)
	}
	if r.countContains("select-pane -t %3") != 1 {
		t.Errorf("expected select-pane on the matching pane, calls = %v", r.calls)
	}
}

func TestFocusTaskPane_NoMatchingPane_NoSelect(t *testing.T) {
	r := &tmuxRecorder{
		listPanesOut: "%1 \n%2 T-007\n",
	}
	swapTmuxRunOut(t, r)

	focusTaskPane(tmuxEnabled(), "T-100")

	if r.countContains("select-pane") != 0 {
		t.Errorf("select-pane should not fire when no pane matches: %v", r.calls)
	}
	if r.countContains("switch-client") != 1 {
		t.Errorf("switch-client should still fire (best-effort): %v", r.calls)
	}
}

func TestFocusTaskPane_TmuxDisabled_NoCalls(t *testing.T) {
	r := &tmuxRecorder{}
	swapTmuxRunOut(t, r)

	cfg := config.Config{Tmux: config.Tmux{Enabled: false, SessionName: "squash-ide"}}
	focusTaskPane(cfg, "T-100")

	if len(r.calls) != 0 {
		t.Errorf("expected zero tmux calls under --no-tmux, got %v", r.calls)
	}
}

func TestFocusTaskPane_EmptySession_NoCalls(t *testing.T) {
	r := &tmuxRecorder{}
	swapTmuxRunOut(t, r)

	cfg := config.Config{Tmux: config.Tmux{Enabled: true, SessionName: ""}}
	focusTaskPane(cfg, "T-100")

	if len(r.calls) != 0 {
		t.Errorf("expected zero tmux calls with empty session, got %v", r.calls)
	}
}

func TestFocusTaskPane_SwitchClientErrorSwallowed(t *testing.T) {
	r := &tmuxRecorder{
		listPanesOut: "%1 \n%3 T-100\n",
		errOn: map[string]error{
			"switch-client": context.Canceled,
		},
	}
	swapTmuxRunOut(t, r)

	// Must not panic, and must still attempt the find + select.
	focusTaskPane(tmuxEnabled(), "T-100")

	if r.countContains("select-pane -t %3") != 1 {
		t.Errorf("select-pane should still fire after switch-client error: %v", r.calls)
	}
}

func TestRunNotifyWatch_Dismissed_NoFocus(t *testing.T) {
	d := t.TempDir()
	restore := status.SetNotifyDirForTesting(d)
	t.Cleanup(restore)

	fr := swapNotifyRunner(t)
	fr.Expect("notify-send",
		"-u", "critical",
		"-t", "60000",
		"--print-id",
		"--wait",
		"-A", "default=Focus",
		"squash-ide: T-100 needs input",
		"hi",
	).ReturnsOutput([]byte("42\n")) // id only, no action line — dismiss/timeout

	tr := &tmuxRecorder{listPanesOut: "%1 \n%3 T-100\n"}
	swapTmuxRunOut(t, tr)

	// Mirror runNotifyWatch's wiring without going through loadConfig
	// (which depends on the user's home/config dir at test time).
	res := status.NotifyAndWait(context.Background(), "T-100", "hi")
	if res.Clicked {
		t.Fatalf("Clicked = true on id-only output")
	}
	// On no click we never call focusTaskPane — assert that path stayed cold.
	if len(tr.calls) != 0 {
		t.Errorf("expected no tmux calls when not clicked, got %v", tr.calls)
	}
}

func TestRunNotifyWatch_Clicked_Focuses(t *testing.T) {
	d := t.TempDir()
	restore := status.SetNotifyDirForTesting(d)
	t.Cleanup(restore)

	fr := swapNotifyRunner(t)
	fr.Expect("notify-send",
		"-u", "critical",
		"-t", "60000",
		"--print-id",
		"--wait",
		"-A", "default=Focus",
		"squash-ide: T-100 needs input",
		"hi",
	).ReturnsOutput([]byte("42\ndefault\n"))

	tr := &tmuxRecorder{listPanesOut: "%1 \n%3 T-100\n"}
	swapTmuxRunOut(t, tr)

	res := status.NotifyAndWait(context.Background(), "T-100", "hi")
	if !res.Clicked {
		t.Fatalf("Clicked = false after default action emitted")
	}
	focusTaskPane(tmuxEnabled(), "T-100")

	if tr.countContains("switch-client -t squash-ide") != 1 {
		t.Errorf("expected switch-client, calls = %v", tr.calls)
	}
	if tr.countContains("select-pane -t %3") != 1 {
		t.Errorf("expected select-pane on T-100's pane, calls = %v", tr.calls)
	}
}
