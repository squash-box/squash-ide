package ui

import (
	"testing"

	"github.com/squashbox/squash-ide/internal/config"
	"github.com/squashbox/squash-ide/internal/ghx"
	"github.com/squashbox/squash-ide/internal/pane"
	"github.com/squashbox/squash-ide/internal/status"
	"github.com/squashbox/squash-ide/internal/task"
	"github.com/squashbox/squash-ide/internal/testutil/fakerunner"
)

// progressModel builds a minimal Model with the given Progress config and tasks.
func progressModel(prog config.Progress, tasks []task.Task) Model {
	cfg := config.Defaults()
	cfg.Progress = prog
	m := Model{cfg: cfg}
	m.allTasks = tasks
	return m
}

func TestProgressProbes_GatedByPollCI(t *testing.T) {
	tasks := []task.Task{{ID: "T-1", Status: "active", Title: "x"}}
	m := progressModel(config.Progress{Show: true, PollCI: false}, tasks)
	if probes := m.progressProbes(); probes != nil {
		t.Errorf("PollCI=false should yield no probes, got %v", probes)
	}
}

func TestProgressProbes_SkipsNonActive(t *testing.T) {
	// PollCI on, but only backlog tasks → no probes (and no gh calls).
	tasks := []task.Task{{ID: "T-1", Status: "backlog", Title: "x"}}
	m := progressModel(config.Progress{Show: true, PollCI: true}, tasks)
	if probes := m.progressProbes(); len(probes) != 0 {
		t.Errorf("backlog-only should yield no probes, got %v", probes)
	}
}

// runProgressPoll is the progress-tick body. This is where gh is invoked — never
// on the 1 s tickStatus loop (which only calls status.ReadAll). A passing PR
// rolls up to prRaised + ChecksPassing.
func TestRunProgressPoll_InvokesGHAndRollsUp(t *testing.T) {
	r := fakerunner.New(t)
	// A single PRChecksForBranch call yields both prRaised (non-error) and the
	// CI rollup — no separate PRURLForBranch shell-out.
	r.ExpectLookPath("gh").ReturnsLookPath("/usr/bin/gh")
	r.Expect("git", "-C", "/repo", "remote", "get-url", "origin").
		ReturnsOutput([]byte("git@github.com:foo/bar.git\n"))
	r.Expect("gh", "pr", "view", "feat/x", "-R", "foo/bar", "--json", "statusCheckRollup").
		ReturnsOutput([]byte(`{"statusCheckRollup":[{"__typename":"CheckRun","status":"COMPLETED","conclusion":"SUCCESS"}]}`))

	prev := ghx.SetRunner(r)
	t.Cleanup(func() { ghx.SetRunner(prev) })

	msg := runProgressPoll([]progressProbe{{id: "T-1", repoPath: "/repo", branch: "feat/x"}})
	pt, ok := msg.(progressTickMsg)
	if !ok {
		t.Fatalf("msg type %T", msg)
	}
	gp := pt.progress["T-1"]
	if !gp.prRaised {
		t.Error("prRaised should be true for an open PR")
	}
	if gp.checks != ghx.ChecksPassing {
		t.Errorf("checks = %q, want passing", gp.checks)
	}

	// Assert gh was actually invoked (the progress poll's job).
	var sawGH bool
	for _, c := range r.Calls() {
		if c.Name == "gh" {
			sawGH = true
		}
	}
	if !sawGH {
		t.Error("expected gh to be invoked on the progress poll")
	}
}

// A gh failure (here: gh absent) must be swallowed — the task degrades to grey
// (no PR, ChecksNone), the poll never panics, and the TUI never flips to error.
func TestRunProgressPoll_SwallowsGHErrors(t *testing.T) {
	r := fakerunner.New(t)
	r.ExpectLookPath("gh").ReturnsLookPath("") // ErrGHMissing — single poll call

	prev := ghx.SetRunner(r)
	t.Cleanup(func() { ghx.SetRunner(prev) })

	msg := runProgressPoll([]progressProbe{{id: "T-1", repoPath: "/repo", branch: "feat/x"}})
	gp := msg.(progressTickMsg).progress["T-1"]
	if gp.prRaised {
		t.Error("prRaised should be false when gh is missing")
	}
	if gp.checks != ghx.ChecksNone {
		t.Errorf("checks = %q, want none (degraded)", gp.checks)
	}
}

func TestRunProgressPoll_EmptyProbes(t *testing.T) {
	msg := runProgressPoll(nil)
	if len(msg.(progressTickMsg).progress) != 0 {
		t.Error("no probes should yield empty progress map")
	}
}

// Regression (T-053): a stage-only status entry (State="", Stage set) flowing
// through the native statusTickMsg arm must paint the pane border idle, not the
// default WORKING — keeping the border in agreement with the list badge (the
// T-023 invariant).
func TestStatusTick_StageOnlyEntry_PaneBorderIdle(t *testing.T) {
	mgr := newStubManager()
	m := nativeModel(t, mgr)
	m.allTasks = []task.Task{{ID: "T-1", Title: "x", Project: "p", Status: "active"}}

	statuses := map[string]status.File{
		"T-1": {TaskID: "T-1", State: "", Stage: status.StageTesting}, // stage-only
	}
	out, _ := m.Update(statusTickMsg{statuses: statuses})
	_ = out

	if got := mgr.states["T-1"]; got != pane.StateIdle {
		t.Errorf("pane state = %q, want %q (stage-only entry must not paint WORKING)", got, pane.StateIdle)
	}
}
