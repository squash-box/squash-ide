package fakerunner

import (
	"context"
	"errors"
	"testing"
)

func TestExpectRun_Match(t *testing.T) {
	r := New(t)
	r.Expect("git", "fetch", "origin")
	if err := r.Run(context.Background(), "git", "fetch", "origin"); err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(r.Calls()) != 1 {
		t.Fatalf("expected 1 call, got %d", len(r.Calls()))
	}
}

func TestExpectRun_ReturnsErr(t *testing.T) {
	r := New(t)
	want := errors.New("boom")
	r.Expect("git", "push").ReturnsExitErr(want)
	err := r.Run(context.Background(), "git", "push")
	if !errors.Is(err, want) {
		t.Fatalf("expected wrapped %v, got %v", want, err)
	}
}

func TestExpectOutput_ReturnsBytes(t *testing.T) {
	r := New(t)
	r.Expect("tmux", "display-message", "-p", "#{window_width}").
		ReturnsOutput([]byte("120\n"))
	out, err := r.Output(context.Background(), "tmux", "display-message", "-p", "#{window_width}")
	if err != nil {
		t.Fatalf("output: %v", err)
	}
	if string(out) != "120\n" {
		t.Fatalf("got %q", out)
	}
}

func TestLookPath_Match(t *testing.T) {
	r := New(t)
	r.ExpectLookPath("git").ReturnsLookPath("/usr/bin/git")
	p, err := r.LookPath("git")
	if err != nil {
		t.Fatalf("lookpath: %v", err)
	}
	if p != "/usr/bin/git" {
		t.Fatalf("got %q", p)
	}
}

func TestLookPath_NotFound(t *testing.T) {
	r := New(t)
	r.ExpectLookPath("ghost").ReturnsLookPath("")
	if _, err := r.LookPath("ghost"); err == nil {
		t.Fatal("expected err")
	}
}

func TestUnmatched_FailsTest(t *testing.T) {
	sub := &fakeT{T: t}
	r := &Runner{t: sub}
	r.Expect("git", "fetch", "origin")
	r.verify()
	if !sub.failed {
		t.Fatal("expected unmatched expectation to fail the test")
	}
}

func TestUnexpectedCall_FailsUnlessAllowed(t *testing.T) {
	sub := &fakeT{T: t}
	r := &Runner{t: sub}
	_ = r.Run(context.Background(), "ghost")
	if !sub.failed {
		t.Fatal("unexpected call should have flagged the test")
	}
}

func TestAllowUnexpected_SwallowsCalls(t *testing.T) {
	r := New(t)
	r.AllowUnexpected = true
	if err := r.Run(context.Background(), "ghost", "arg"); err != nil {
		t.Fatalf("run with AllowUnexpected: %v", err)
	}
	if got := r.Calls(); len(got) != 1 {
		t.Fatalf("expected 1 recorded call, got %d", len(got))
	}
}

// fakeT is a minimal testingT stand-in that swallows Errorf so we can
// observe whether the runner would have failed a real test.
type fakeT struct {
	*testing.T
	failed bool
}

func (f *fakeT) Errorf(format string, args ...any) { f.failed = true }
func (f *fakeT) Helper()                           {}
func (f *fakeT) Cleanup(func())                    {}
