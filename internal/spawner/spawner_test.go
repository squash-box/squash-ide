package spawner

import (
	"errors"
	"strings"
	"testing"

	"github.com/squashbox/squash-ide/internal/config"
	"github.com/squashbox/squash-ide/internal/testutil/fakerunner"
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
