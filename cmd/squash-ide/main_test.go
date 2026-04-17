package main

import (
	"strings"
	"testing"

	"github.com/squashbox/squash-ide/internal/task"
)

func TestFindTask_Match(t *testing.T) {
	tasks := []task.Task{
		{ID: "T-001", Title: "one"},
		{ID: "T-002", Title: "two"},
	}
	got := findTask(tasks, "T-002")
	if got == nil {
		t.Fatal("expected match")
	}
	if got.Title != "two" {
		t.Errorf("got %q", got.Title)
	}
}

func TestFindTask_NoMatch(t *testing.T) {
	if findTask([]task.Task{{ID: "T-001"}}, "T-999") != nil {
		t.Error("expected nil")
	}
}

func TestFindTask_EmptyList(t *testing.T) {
	if findTask(nil, "T-001") != nil {
		t.Error("expected nil")
	}
}

func TestShellQuote_SafeChars(t *testing.T) {
	cases := []string{
		"plain",
		"/abs/path",
		"with-hyphen",
		"with_underscore",
		"a.b.c",
		"a=b",
		"T-001",
	}
	for _, c := range cases {
		got := shellQuote(c)
		if got != c {
			t.Errorf("shellQuote(%q) = %q, want unquoted", c, got)
		}
	}
}

func TestShellQuote_NeedsQuoting(t *testing.T) {
	cases := map[string]string{
		"":            "''",
		"has space":   "'has space'",
		`has 'quote'`: `'has '\''quote'\'''`,
		"semi;colon":  "'semi;colon'",
		"dollar$sign": "'dollar$sign'",
	}
	for in, want := range cases {
		if got := shellQuote(in); got != want {
			t.Errorf("shellQuote(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBuildSelfInvocation_BaseCase(t *testing.T) {
	// Zero out test globals — they're package-level in main.go.
	flagVault = ""
	flagTerminal = ""
	flagSpawnCmd = ""
	flagTUIWidth = 0
	flagMinPaneWidth = 0

	got := buildSelfInvocation()
	// Argv[0] prefix + --in-tmux suffix.
	if !strings.HasSuffix(got, "--in-tmux") {
		t.Errorf("missing --in-tmux: %q", got)
	}
}

func TestBuildSelfInvocation_ForwardsFlags(t *testing.T) {
	flagVault = "/tmp/vault"
	flagTerminal = "gnome-terminal"
	flagTUIWidth = 80
	flagMinPaneWidth = 90
	defer func() {
		flagVault, flagTerminal, flagTUIWidth, flagMinPaneWidth = "", "", 0, 0
	}()

	got := buildSelfInvocation()
	for _, want := range []string{
		"--vault", "/tmp/vault", "--terminal", "gnome-terminal",
		"--tui-width=80", "--min-pane-width=90", "--in-tmux",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
}

func TestBuildPlaceholderInvocation(t *testing.T) {
	flagVault = "/tmp/vault"
	flagTUIWidth = 60
	defer func() { flagVault, flagTUIWidth = "", 0 }()

	got := buildPlaceholderInvocation()
	if !strings.Contains(got, "placeholder") {
		t.Errorf("missing 'placeholder' subcommand: %q", got)
	}
	if !strings.Contains(got, "--in-tmux") {
		t.Errorf("missing --in-tmux: %q", got)
	}
	if !strings.Contains(got, "--tui-width=60") {
		t.Errorf("missing --tui-width: %q", got)
	}
}
