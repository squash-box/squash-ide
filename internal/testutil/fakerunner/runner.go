// Package fakerunner is a record-and-replay implementation of exec.Runner
// for unit tests. Tests set up expected calls with Expect(...) and assert
// on the recorded Calls after the code under test runs.
//
// Usage:
//
//	r := fakerunner.New(t)
//	r.Expect("git", "fetch", "origin").ReturnsExitCode(0)
//	r.Expect("git", "worktree", "add", "/tmp/x", "-b", "feat/T-001-x", "origin/main").ReturnsExitCode(0)
//	path, err := worktree.CreateWith(r, "/repo", "feat/T-001-x")
//	// ...
package fakerunner

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/squashbox/squash-ide/internal/exec"
)

// Call records a single observed invocation.
type Call struct {
	Kind string // "Run", "Output", "Start", "LookPath", "Replace"
	Name string
	Args []string
	Env  []string
}

// Expectation is a queued response attached to a specific call shape.
type Expectation struct {
	kind string // "any", "run", "output", "start", "lookpath", "replace"
	name string
	args []string

	// Response.
	output  []byte
	err     error
	lookOK  string // value returned by LookPath on match ("" => ErrNotFound)
	matched bool   // flipped to true once consumed
}

// ReturnsOutput queues bytes to return from Output.
func (e *Expectation) ReturnsOutput(b []byte) *Expectation {
	e.output = b
	return e
}

// ReturnsExitErr queues an error to return.
func (e *Expectation) ReturnsExitErr(err error) *Expectation {
	e.err = err
	return e
}

// ReturnsLookPath sets the resolved path returned by LookPath on a match.
// Passing "" triggers a "not found" error instead.
func (e *Expectation) ReturnsLookPath(path string) *Expectation {
	e.lookOK = path
	if path == "" {
		e.err = fmt.Errorf("exec: %q: executable file not found in $PATH", e.name)
	}
	return e
}

// testingT is the subset of *testing.T the runner needs. An interface so
// the runner's own unit tests can stub it and observe failure signals
// without actually failing the outer test.
type testingT interface {
	Errorf(format string, args ...any)
	Helper()
	Cleanup(func())
}

// Runner is the fake. It is concurrency-safe so tests that exercise
// goroutines (or the -race detector) don't false-positive.
type Runner struct {
	t  testingT
	mu sync.Mutex

	expectations []*Expectation
	calls        []Call

	// AllowUnexpected, when true, lets unmatched calls succeed with zero
	// output rather than failing the test. Useful for tests that only
	// care about a subset of calls.
	AllowUnexpected bool
}

// New creates a fresh Runner bound to t. On test cleanup it asserts that
// every queued expectation was consumed (unless AllowUnexpected is set).
func New(t *testing.T) *Runner {
	t.Helper()
	r := &Runner{t: t}
	t.Cleanup(r.verify)
	return r
}

// Expect queues an expected call matching name + args exactly. Returns the
// Expectation so the test can chain ReturnsOutput / ReturnsExitErr.
func (r *Runner) Expect(name string, args ...string) *Expectation {
	r.mu.Lock()
	defer r.mu.Unlock()
	exp := &Expectation{kind: "any", name: name, args: append([]string(nil), args...)}
	r.expectations = append(r.expectations, exp)
	return exp
}

// ExpectLookPath queues a response to LookPath(name).
func (r *Runner) ExpectLookPath(name string) *Expectation {
	r.mu.Lock()
	defer r.mu.Unlock()
	exp := &Expectation{kind: "lookpath", name: name, lookOK: name}
	r.expectations = append(r.expectations, exp)
	return exp
}

// Calls returns a copy of the recorded calls.
func (r *Runner) Calls() []Call {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Call, len(r.calls))
	copy(out, r.calls)
	return out
}

// Run implements exec.Runner.
func (r *Runner) Run(_ context.Context, name string, args ...string) error {
	exp := r.match("run", name, args)
	r.record(Call{Kind: "Run", Name: name, Args: args})
	if exp == nil {
		return nil
	}
	return exp.err
}

// Output implements exec.Runner.
func (r *Runner) Output(_ context.Context, name string, args ...string) ([]byte, error) {
	exp := r.match("output", name, args)
	r.record(Call{Kind: "Output", Name: name, Args: args})
	if exp == nil {
		return nil, nil
	}
	return exp.output, exp.err
}

// Start implements exec.Runner.
func (r *Runner) Start(name string, args []string, _ bool) error {
	exp := r.match("start", name, args)
	r.record(Call{Kind: "Start", Name: name, Args: args})
	if exp == nil {
		return nil
	}
	return exp.err
}

// LookPath implements exec.Runner.
func (r *Runner) LookPath(name string) (string, error) {
	r.mu.Lock()
	var match *Expectation
	for _, exp := range r.expectations {
		if exp.matched {
			continue
		}
		if exp.kind == "lookpath" && exp.name == name {
			exp.matched = true
			match = exp
			break
		}
	}
	r.mu.Unlock()
	r.record(Call{Kind: "LookPath", Name: name})

	if match != nil {
		if match.err != nil {
			return "", match.err
		}
		return match.lookOK, nil
	}
	if r.AllowUnexpected {
		return name, nil
	}
	r.t.Helper()
	r.t.Errorf("fakerunner: unexpected LookPath(%q)", name)
	return "", fmt.Errorf("unexpected LookPath(%q)", name)
}

// Replace implements exec.Runner.
func (r *Runner) Replace(name string, args []string, env []string) error {
	exp := r.match("replace", name, args)
	r.record(Call{Kind: "Replace", Name: name, Args: args, Env: env})
	if exp == nil {
		return nil
	}
	return exp.err
}

// match finds and consumes the first queued expectation whose name+args
// match the call. Returns nil if AllowUnexpected is true and no match
// exists; otherwise fails the test.
func (r *Runner) match(_ string, name string, args []string) *Expectation {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, exp := range r.expectations {
		if exp.matched {
			continue
		}
		if exp.kind == "lookpath" {
			continue
		}
		if exp.name != name {
			continue
		}
		if !argsEqual(exp.args, args) {
			continue
		}
		exp.matched = true
		return exp
	}
	if r.AllowUnexpected {
		return nil
	}
	r.t.Helper()
	r.t.Errorf("fakerunner: unexpected call %s %s", name, strings.Join(args, " "))
	return nil
}

func (r *Runner) record(c Call) {
	r.mu.Lock()
	r.calls = append(r.calls, c)
	r.mu.Unlock()
}

// verify runs at test cleanup — fails the test if any expectation never matched.
func (r *Runner) verify() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.AllowUnexpected {
		return
	}
	for _, exp := range r.expectations {
		if !exp.matched {
			r.t.Helper()
			r.t.Errorf("fakerunner: unmatched expectation %s %s",
				exp.name, strings.Join(exp.args, " "))
		}
	}
}

func argsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Assert that Runner implements the interface at compile time.
var _ exec.Runner = (*Runner)(nil)
