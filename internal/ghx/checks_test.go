package ghx

import (
	"errors"
	"fmt"
	"testing"

	"github.com/squashbox/squash-ide/internal/testutil/fakerunner"
)

// rollupJSON wraps a statusCheckRollup array in the gh pr view envelope.
func rollupJSON(inner string) []byte {
	return []byte(fmt.Sprintf(`{"statusCheckRollup":%s}`, inner))
}

func expectPRView(r *fakerunner.Runner, slug, branch string, out []byte) {
	r.ExpectLookPath("gh").ReturnsLookPath("/usr/bin/gh")
	r.Expect("git", "-C", "/repo", "remote", "get-url", "origin").
		ReturnsOutput([]byte("git@github.com:" + slug + ".git\n"))
	r.Expect("gh", "pr", "view", branch, "-R", slug, "--json", "statusCheckRollup").
		ReturnsOutput(out)
}

func TestPRChecksForBranch_Passing(t *testing.T) {
	r := fakerunner.New(t)
	expectPRView(r, "foo/bar", "feat/x", rollupJSON(
		`[{"__typename":"CheckRun","status":"COMPLETED","conclusion":"SUCCESS"}]`))

	got, err := PRChecksForBranchWith(r, "/repo", "feat/x")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != ChecksPassing {
		t.Errorf("got %q, want passing", got)
	}
}

func TestPRChecksForBranch_Failing(t *testing.T) {
	r := fakerunner.New(t)
	expectPRView(r, "foo/bar", "feat/x", rollupJSON(
		`[{"__typename":"CheckRun","status":"COMPLETED","conclusion":"SUCCESS"},
		  {"__typename":"CheckRun","status":"COMPLETED","conclusion":"FAILURE"}]`))

	got, err := PRChecksForBranchWith(r, "/repo", "feat/x")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != ChecksFailing {
		t.Errorf("got %q, want failing", got)
	}
}

func TestPRChecksForBranch_Pending(t *testing.T) {
	r := fakerunner.New(t)
	expectPRView(r, "foo/bar", "feat/x", rollupJSON(
		`[{"__typename":"CheckRun","status":"IN_PROGRESS","conclusion":""}]`))

	got, err := PRChecksForBranchWith(r, "/repo", "feat/x")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != ChecksPending {
		t.Errorf("got %q, want pending", got)
	}
}

func TestPRChecksForBranch_EmptyRollupIsPending(t *testing.T) {
	r := fakerunner.New(t)
	expectPRView(r, "foo/bar", "feat/x", rollupJSON(`[]`))

	got, err := PRChecksForBranchWith(r, "/repo", "feat/x")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != ChecksPending {
		t.Errorf("got %q, want pending (PR exists, no checks)", got)
	}
}

func TestPRChecksForBranch_StatusContextFailure(t *testing.T) {
	r := fakerunner.New(t)
	expectPRView(r, "foo/bar", "feat/x", rollupJSON(
		`[{"__typename":"StatusContext","state":"FAILURE"}]`))

	got, err := PRChecksForBranchWith(r, "/repo", "feat/x")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != ChecksFailing {
		t.Errorf("got %q, want failing", got)
	}
}

func TestPRChecksForBranch_NoPR(t *testing.T) {
	r := fakerunner.New(t)
	r.ExpectLookPath("gh").ReturnsLookPath("/usr/bin/gh")
	r.Expect("git", "-C", "/repo", "remote", "get-url", "origin").
		ReturnsOutput([]byte("git@github.com:foo/bar.git"))
	r.Expect("gh", "pr", "view", "feat/orphan", "-R", "foo/bar", "--json", "statusCheckRollup").
		ReturnsExitErr(fmt.Errorf("no pull requests found for branch \"feat/orphan\""))

	got, err := PRChecksForBranchWith(r, "/repo", "feat/orphan")
	if !errors.Is(err, ErrNoPR) {
		t.Fatalf("err = %v; want errors.Is(err, ErrNoPR)", err)
	}
	if got != ChecksNone {
		t.Errorf("got %q, want none", got)
	}
}

func TestPRChecksForBranch_GHMissing_NoRunnerCalls(t *testing.T) {
	r := fakerunner.New(t)
	r.ExpectLookPath("gh").ReturnsLookPath("")

	got, err := PRChecksForBranchWith(r, "/repo", "feat/x")
	if !errors.Is(err, ErrGHMissing) {
		t.Fatalf("err = %v; want ErrGHMissing", err)
	}
	if got != ChecksNone {
		t.Errorf("got %q, want none", got)
	}
	for _, c := range r.Calls() {
		if c.Kind == "Output" || c.Kind == "Run" {
			t.Errorf("unexpected runner call after LookPath miss: %+v", c)
		}
	}
}

func TestPRChecksForBranch_NonGitHubRemote(t *testing.T) {
	r := fakerunner.New(t)
	r.ExpectLookPath("gh").ReturnsLookPath("/usr/bin/gh")
	r.Expect("git", "-C", "/repo", "remote", "get-url", "origin").
		ReturnsOutput([]byte("git@gitlab.com:foo/bar.git"))

	_, err := PRChecksForBranchWith(r, "/repo", "feat/x")
	if !errors.Is(err, ErrNonGitHubRemote) {
		t.Fatalf("err = %v; want ErrNonGitHubRemote", err)
	}
}

func TestPRChecksForBranch_TransientGHFailureNotSentinel(t *testing.T) {
	r := fakerunner.New(t)
	r.ExpectLookPath("gh").ReturnsLookPath("/usr/bin/gh")
	r.Expect("git", "-C", "/repo", "remote", "get-url", "origin").
		ReturnsOutput([]byte("git@github.com:foo/bar.git"))
	r.Expect("gh", "pr", "view", "feat/x", "-R", "foo/bar", "--json", "statusCheckRollup").
		ReturnsExitErr(fmt.Errorf("HTTP 502"))

	_, err := PRChecksForBranchWith(r, "/repo", "feat/x")
	if err == nil {
		t.Fatal("expected error")
	}
	if errors.Is(err, ErrNoPR) || errors.Is(err, ErrGHMissing) {
		t.Errorf("transient gh failure should not present as a sentinel; got %v", err)
	}
}

func TestRollupChecks_Table(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want ChecksState
	}{
		{"all success", `[{"__typename":"CheckRun","status":"COMPLETED","conclusion":"SUCCESS"}]`, ChecksPassing},
		{"neutral+skipped pass", `[{"__typename":"CheckRun","status":"COMPLETED","conclusion":"NEUTRAL"},{"__typename":"CheckRun","status":"COMPLETED","conclusion":"SKIPPED"}]`, ChecksPassing},
		{"one failure", `[{"__typename":"CheckRun","status":"COMPLETED","conclusion":"FAILURE"}]`, ChecksFailing},
		{"cancelled fails", `[{"__typename":"CheckRun","status":"COMPLETED","conclusion":"CANCELLED"}]`, ChecksFailing},
		{"completed empty conclusion is not red", `[{"__typename":"CheckRun","status":"COMPLETED","conclusion":""}]`, ChecksPassing},
		{"queued pending", `[{"__typename":"CheckRun","status":"QUEUED","conclusion":""}]`, ChecksPending},
		{"pending wins over success", `[{"__typename":"CheckRun","status":"COMPLETED","conclusion":"SUCCESS"},{"__typename":"CheckRun","status":"IN_PROGRESS"}]`, ChecksPending},
		{"failure wins over pending", `[{"__typename":"CheckRun","status":"IN_PROGRESS"},{"__typename":"CheckRun","status":"COMPLETED","conclusion":"FAILURE"}]`, ChecksFailing},
		{"status context pending", `[{"__typename":"StatusContext","state":"PENDING"}]`, ChecksPending},
		{"empty", `[]`, ChecksPending},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := rollupChecks(rollupJSON(c.in))
			if err != nil {
				t.Fatalf("err: %v", err)
			}
			if got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestRollupChecks_BadJSON(t *testing.T) {
	if _, err := rollupChecks([]byte("not json")); err == nil {
		t.Error("expected error on bad JSON")
	}
}
