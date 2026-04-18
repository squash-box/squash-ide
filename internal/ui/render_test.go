package ui

import (
	"strings"
	"testing"

	"github.com/squashbox/squash-ide/internal/status"
	"github.com/squashbox/squash-ide/internal/task"
)

// Tests in this file pin the four-state badge renderer and, critically,
// the nil-sub collapse to IDLE — the primary regression guard for T-023.
// Before the fix, a task whose status file had aged past StaleDuration
// would render `● WORKING` in the list while the tmux pane border
// remained on `○ IDLE`. The nil-sub case now lands on IDLE too.

func badgeTask() task.Task {
	return task.Task{ID: "T-999", Type: "feature", Title: "t", Project: "p", Status: "active"}
}

func TestActiveBadge_NilSubRendersIdle(t *testing.T) {
	got := activeBadge(badgeTask(), nil)
	if !strings.Contains(got, "IDLE") {
		t.Errorf("nil sub should render IDLE, got %q", got)
	}
	if strings.Contains(got, "WORKING") {
		t.Errorf("nil sub must not render WORKING, got %q", got)
	}
}

func TestActiveBadge_IdleState(t *testing.T) {
	got := activeBadge(badgeTask(), &status.File{State: "idle"})
	if !strings.Contains(got, "IDLE") {
		t.Errorf("idle state should render IDLE, got %q", got)
	}
}

func TestActiveBadge_WorkingState(t *testing.T) {
	got := activeBadge(badgeTask(), &status.File{State: "working"})
	if !strings.Contains(got, "WORKING") {
		t.Errorf("working state should render WORKING, got %q", got)
	}
}

func TestActiveBadge_InputRequiredState(t *testing.T) {
	got := activeBadge(badgeTask(), &status.File{State: "input_required"})
	if !strings.Contains(got, "INPUT REQUIRED") {
		t.Errorf("input_required should render INPUT REQUIRED, got %q", got)
	}
}

func TestActiveBadge_TestingState(t *testing.T) {
	got := activeBadge(badgeTask(), &status.File{State: "testing"})
	if !strings.Contains(got, "TESTING") {
		t.Errorf("testing should render TESTING, got %q", got)
	}
}

// An unknown non-nil state still falls through to WORKING — it means
// "a writer reported *something*, just not a state we recognise", which is
// distinct from "no report at all" (nil → IDLE). Preserving this discrimination
// keeps the renderer honest about where its data came from.
func TestActiveBadge_UnknownStateRendersWorking(t *testing.T) {
	got := activeBadge(badgeTask(), &status.File{State: "bogus"})
	if !strings.Contains(got, "WORKING") {
		t.Errorf("unknown state should render WORKING, got %q", got)
	}
}
