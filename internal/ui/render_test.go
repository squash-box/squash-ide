package ui

import (
	"reflect"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/squashbox/squash-ide/internal/ghx"
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

// A stage-only entry (T-053) has a non-nil sub with an empty State. It must
// render IDLE, not WORKING — an empty State means "no activity report", same
// as a nil sub. Without this, a task surfaced purely by its stage file would
// falsely flash WORKING.
func TestActiveBadge_EmptyStateRendersIdle(t *testing.T) {
	got := activeBadge(badgeTask(), &status.File{State: "", Stage: "implementation"})
	if !strings.Contains(got, "IDLE") {
		t.Errorf("empty state should render IDLE, got %q", got)
	}
	if strings.Contains(got, "WORKING") {
		t.Errorf("empty state must not render WORKING, got %q", got)
	}
}

// --- Stage lights (T-053) ----------------------------------------------------

func TestDeriveStageLights_TestingActive(t *testing.T) {
	got := deriveStageLights(status.StageTesting, ghProgress{})
	want := []stageLight{lightDone, lightDone, lightActive, lightPending, lightPending, lightPending}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestDeriveStageLights_NoStageAllPending(t *testing.T) {
	got := deriveStageLights("", ghProgress{})
	want := []stageLight{lightPending, lightPending, lightPending, lightPending, lightPending, lightPending}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestDeriveStageLights_AcceptancePRRaisedCIPassing(t *testing.T) {
	got := deriveStageLights(status.StageAcceptance, ghProgress{prRaised: true, checks: ghx.ChecksPassing})
	// First five green (a raised PR implies all Claude stages done), ci green.
	want := []stageLight{lightDone, lightDone, lightDone, lightDone, lightDone, lightDone}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// Light count tracks status.StageOrder, not a hardcoded 6 — the single-source-
// of-truth contract. If a stage is ever added, this guards that the derivation
// still produces one light per stage.
func TestDeriveStageLights_LengthTracksStageOrder(t *testing.T) {
	if got := len(deriveStageLights(status.StageTesting, ghProgress{})); got != len(status.StageOrder) {
		t.Errorf("light count = %d, want %d", got, len(status.StageOrder))
	}
}

func TestDeriveStageLights_CIFailingIsRed(t *testing.T) {
	got := deriveStageLights(status.StageAcceptance, ghProgress{prRaised: true, checks: ghx.ChecksFailing})
	if got[5] != lightFailed {
		t.Errorf("ci light = %v, want failed (red)", got[5])
	}
	if got[4] != lightDone {
		t.Errorf("pr light = %v, want done (green) — PR raised", got[4])
	}
}

func TestDeriveStageLights_NoPRStaysPending(t *testing.T) {
	got := deriveStageLights(status.StageImplementation, ghProgress{})
	if got[4] != lightPending || got[5] != lightPending {
		t.Errorf("pr/ci = %v/%v, want pending/pending when no PR", got[4], got[5])
	}
}

func TestRenderStageStrip_UnselectedIsOneCompactLine(t *testing.T) {
	lines := renderStageStrip(status.StageTesting, ghProgress{}, false, 17)
	if len(lines) != 1 {
		t.Fatalf("unselected strip should be 1 line, got %d: %v", len(lines), lines)
	}
	if w := lipgloss.Width(lines[0]); w > 17 {
		t.Errorf("compact strip width %d exceeds innerW 17", w)
	}
	// All six glyphs present.
	if !strings.Contains(lines[0], "●") || !strings.Contains(lines[0], "◐") || !strings.Contains(lines[0], "○") {
		t.Errorf("strip missing expected glyphs: %q", lines[0])
	}
}

func TestRenderStageStrip_SelectedIsVerticalLabelled(t *testing.T) {
	lines := renderStageStrip(status.StageTesting, ghProgress{}, true, 30)
	if len(lines) != 6 {
		t.Fatalf("selected strip should be 6 rows, got %d", len(lines))
	}
	joined := strings.Join(lines, "\n")
	for _, label := range []string{"planning", "implementation", "testing", "acceptance", "pr", "ci"} {
		if !strings.Contains(joined, label) {
			t.Errorf("vertical breakdown missing label %q", label)
		}
	}
}

func TestRenderStageStrip_CompactNoOverflow(t *testing.T) {
	// Selected (vertical) at a 20-col sidebar width: innerW = 17. Each row must
	// fit without wrap-corruption — assert no rendered row exceeds innerW.
	const innerW = 17
	lines := renderStageStrip(status.StageImplementation, ghProgress{}, true, innerW)
	for i, l := range lines {
		if w := lipgloss.Width(l); w > innerW {
			t.Errorf("row %d width %d exceeds innerW %d: %q", i, w, innerW, l)
		}
	}
}

// renderCard with the strip enabled adds rows to an active card; backlog cards
// never get a strip even when enabled.
func TestRenderCard_StripGatedToActive(t *testing.T) {
	active := task.Task{ID: "T-1", Type: "feature", Title: "x", Project: "p", Status: "active"}
	backlog := task.Task{ID: "T-2", Type: "feature", Title: "y", Project: "p", Status: "backlog"}

	withStrip := renderCard(active, false, 60, &status.File{State: "working", Stage: "testing"}, false, ghProgress{}, true)
	without := renderCard(active, false, 60, &status.File{State: "working", Stage: "testing"}, false, ghProgress{}, false)
	if len(withStrip) <= len(without) {
		t.Errorf("strip-enabled active card should be taller: %d vs %d", len(withStrip), len(without))
	}

	bl := renderCard(backlog, false, 60, nil, false, ghProgress{}, true)
	blNo := renderCard(backlog, false, 60, nil, false, ghProgress{}, false)
	if len(bl) != len(blNo) {
		t.Errorf("backlog card must never render a strip: %d vs %d", len(bl), len(blNo))
	}
}
