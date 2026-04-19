package ghx

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/squashbox/squash-ide/internal/testutil/fakerunner"
)

func TestPRURLForBranch_HappyPath(t *testing.T) {
	r := fakerunner.New(t)
	r.ExpectLookPath("gh").ReturnsLookPath("/usr/bin/gh")
	r.Expect("git", "-C", "/repo", "remote", "get-url", "origin").
		ReturnsOutput([]byte("git@github.com:squash-box/squash-ide.git\n"))
	r.Expect("gh", "pr", "list",
		"-R", "squash-box/squash-ide",
		"--head", "feat/T-029-x",
		"--json", "url",
		"--jq", ".[0].url",
	).ReturnsOutput([]byte("https://github.com/squash-box/squash-ide/pull/42\n"))

	got, err := PRURLForBranchWith(r, "/repo", "feat/T-029-x")
	if err != nil {
		t.Fatalf("PRURLForBranchWith: %v", err)
	}
	if got != "https://github.com/squash-box/squash-ide/pull/42" {
		t.Errorf("URL = %q (want trimmed)", got)
	}
}

func TestPRURLForBranch_HappyPath_HTTPSRemote(t *testing.T) {
	r := fakerunner.New(t)
	r.ExpectLookPath("gh").ReturnsLookPath("/usr/bin/gh")
	r.Expect("git", "-C", "/repo", "remote", "get-url", "origin").
		ReturnsOutput([]byte("https://github.com/squash-box/squash-ide.git\n"))
	r.Expect("gh", "pr", "list",
		"-R", "squash-box/squash-ide",
		"--head", "feat/x",
		"--json", "url",
		"--jq", ".[0].url",
	).ReturnsOutput([]byte("https://github.com/squash-box/squash-ide/pull/1\n"))

	got, err := PRURLForBranchWith(r, "/repo", "feat/x")
	if err != nil {
		t.Fatalf("PRURLForBranchWith: %v", err)
	}
	if got != "https://github.com/squash-box/squash-ide/pull/1" {
		t.Errorf("URL = %q", got)
	}
}

func TestPRURLForBranch_NoPR(t *testing.T) {
	r := fakerunner.New(t)
	r.ExpectLookPath("gh").ReturnsLookPath("/usr/bin/gh")
	r.Expect("git", "-C", "/repo", "remote", "get-url", "origin").
		ReturnsOutput([]byte("git@github.com:foo/bar.git"))
	r.Expect("gh", "pr", "list",
		"-R", "foo/bar",
		"--head", "feat/orphan",
		"--json", "url",
		"--jq", ".[0].url",
	).ReturnsOutput([]byte("\n"))

	_, err := PRURLForBranchWith(r, "/repo", "feat/orphan")
	if !errors.Is(err, ErrNoPR) {
		t.Fatalf("err = %v; want errors.Is(err, ErrNoPR)", err)
	}
}

func TestPRURLForBranch_GHMissing_NoRunnerCalls(t *testing.T) {
	r := fakerunner.New(t)
	// Only LookPath should be invoked; no Output calls expected.
	r.ExpectLookPath("gh").ReturnsLookPath("")

	_, err := PRURLForBranchWith(r, "/repo", "feat/x")
	if !errors.Is(err, ErrGHMissing) {
		t.Fatalf("err = %v; want errors.Is(err, ErrGHMissing)", err)
	}

	// Regression guard: no shell-out should have happened beyond LookPath.
	for _, c := range r.Calls() {
		if c.Kind == "Output" || c.Kind == "Run" || c.Kind == "Start" {
			t.Errorf("unexpected runner call after LookPath miss: %+v", c)
		}
	}
}

func TestPRURLForBranch_GHRunFails(t *testing.T) {
	r := fakerunner.New(t)
	r.ExpectLookPath("gh").ReturnsLookPath("/usr/bin/gh")
	r.Expect("git", "-C", "/repo", "remote", "get-url", "origin").
		ReturnsOutput([]byte("git@github.com:foo/bar.git"))
	r.Expect("gh", "pr", "list",
		"-R", "foo/bar",
		"--head", "feat/x",
		"--json", "url",
		"--jq", ".[0].url",
	).ReturnsExitErr(fmt.Errorf("gh: HTTP 502"))

	_, err := PRURLForBranchWith(r, "/repo", "feat/x")
	if err == nil {
		t.Fatal("expected error")
	}
	if errors.Is(err, ErrNoPR) || errors.Is(err, ErrGHMissing) {
		t.Errorf("transient gh failure should not present as a typed sentinel; got %v", err)
	}
}

func TestPRURLForBranch_NonGitHubRemote(t *testing.T) {
	r := fakerunner.New(t)
	r.ExpectLookPath("gh").ReturnsLookPath("/usr/bin/gh")
	r.Expect("git", "-C", "/repo", "remote", "get-url", "origin").
		ReturnsOutput([]byte("git@gitlab.com:foo/bar.git"))

	_, err := PRURLForBranchWith(r, "/repo", "feat/x")
	if !errors.Is(err, ErrNonGitHubRemote) {
		t.Fatalf("err = %v; want errors.Is(err, ErrNonGitHubRemote)", err)
	}
}

func TestPRURLForBranch_GitRemoteFails(t *testing.T) {
	r := fakerunner.New(t)
	r.ExpectLookPath("gh").ReturnsLookPath("/usr/bin/gh")
	r.Expect("git", "-C", "/repo", "remote", "get-url", "origin").
		ReturnsExitErr(fmt.Errorf("fatal: not a git repository"))

	_, err := PRURLForBranchWith(r, "/repo", "feat/x")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "remote") {
		t.Errorf("error should mention remote: %v", err)
	}
}

func TestParseGitHubSlug(t *testing.T) {
	cases := []struct {
		in, want string
		wantErr  bool
	}{
		{"git@github.com:foo/bar.git", "foo/bar", false},
		{"git@github.com:foo/bar.git\n", "foo/bar", false},
		{"https://github.com/foo/bar.git", "foo/bar", false},
		{"https://github.com/foo/bar", "foo/bar", false},
		{"http://github.com/foo/bar", "foo/bar", false},
		{"ssh://git@github.com/foo/bar.git", "foo/bar", false},
		{"git://github.com/foo/bar.git", "foo/bar", false},
		{"https://github.com/foo/bar/", "foo/bar", false},
		{"git@gitlab.com:foo/bar.git", "", true},
		{"https://example.com/foo/bar", "", true},
		{"random garbage", "", true},
	}
	for _, c := range cases {
		got, err := parseGitHubSlug(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("parseGitHubSlug(%q): expected error, got %q", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseGitHubSlug(%q): unexpected err %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("parseGitHubSlug(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSetRunner_RestoresDefault(t *testing.T) {
	orig := runner
	fake := fakerunner.New(t)
	fake.AllowUnexpected = true
	prev := SetRunner(fake)
	if prev != orig {
		t.Error("SetRunner should return previous runner")
	}
	if runner != fake {
		t.Error("runner should be swapped")
	}
	SetRunner(orig)
}
