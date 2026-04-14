package slug

import "testing"

func TestFromTitle(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Bootstrap Go module + config + vault reader", "bootstrap-go-module-config-vault-reader"},
		{"Bubble Tea TUI — read-only task list", "bubble-tea-tui-read-only-task-list"},
		{"spawn subcommand — worktree + terminal", "spawn-subcommand-worktree-terminal"},
		{"Simple", "simple"},
		{"UPPER CASE", "upper-case"},
		{"already-slugged", "already-slugged"},
		{"   spaces   ", "spaces"},
		{"special!@#$%chars", "special-chars"},
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := FromTitle(tt.input)
			if got != tt.want {
				t.Errorf("FromTitle(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestFromTitle_MaxLength(t *testing.T) {
	long := "this is a very long title that should be truncated to forty characters maximum"
	got := FromTitle(long)
	if len(got) > 40 {
		t.Errorf("slug length %d exceeds max 40: %q", len(got), got)
	}
	if got[len(got)-1] == '-' {
		t.Errorf("slug ends with hyphen: %q", got)
	}
}
