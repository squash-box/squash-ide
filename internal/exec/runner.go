// Package exec provides a thin abstraction over os/exec and syscall.Exec so
// code that forks external processes (git, tmux, terminal emulators) can be
// unit-tested with a record-and-replay fake runner.
//
// Production code calls Default (the os-backed implementation). Tests inject
// a fake via the package's constructor functions or by swapping exec.Default
// in a t.Cleanup block.
package exec

import (
	"context"
	"fmt"
	osexec "os/exec"
	"syscall"
)

// Runner is the seam between squash-ide's external-process users and the
// real exec/syscall packages. Keeping it small keeps fakes trivial.
type Runner interface {
	// Run executes name with args, returning the error (nil on exit 0).
	Run(ctx context.Context, name string, args ...string) error

	// Output executes name with args and returns its combined stdout. On a
	// non-zero exit the error includes the stderr tail.
	Output(ctx context.Context, name string, args ...string) ([]byte, error)

	// Start spawns name with args without waiting for it to exit. Used for
	// detached terminal emulators that outlive the parent process.
	Start(name string, args []string, setpgid bool) error

	// LookPath is os/exec.LookPath, exposed on the interface so fakes can
	// simulate "binary not found" without touching PATH.
	LookPath(name string) (string, error)

	// Replace replaces the current process image with name+args via
	// syscall.Exec. This does not return on success; on failure it returns
	// the syscall error.
	Replace(name string, args []string, env []string) error
}

// Default is the production Runner — it wraps os/exec and syscall.Exec.
// Swap it in tests with t.Cleanup to restore.
var Default Runner = OSRunner{}

// OSRunner is the real implementation. Exposed so callers can embed it if
// they want to override only some methods.
type OSRunner struct{}

// Run implements Runner.Run using os/exec.
func (OSRunner) Run(ctx context.Context, name string, args ...string) error {
	c := osexec.CommandContext(ctx, name, args...)
	if err := c.Run(); err != nil {
		return wrap(name, args, err, c)
	}
	return nil
}

// Output implements Runner.Output using os/exec.
func (OSRunner) Output(ctx context.Context, name string, args ...string) ([]byte, error) {
	c := osexec.CommandContext(ctx, name, args...)
	out, err := c.Output()
	if err != nil {
		return out, wrap(name, args, err, c)
	}
	return out, nil
}

// Start implements Runner.Start. The detached flag maps to Setpgid so the
// spawned process survives parent exit.
func (OSRunner) Start(name string, args []string, setpgid bool) error {
	c := osexec.Command(name, args...)
	if setpgid {
		c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	}
	if err := c.Start(); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}

// LookPath implements Runner.LookPath.
func (OSRunner) LookPath(name string) (string, error) { return osexec.LookPath(name) }

// Replace implements Runner.Replace via syscall.Exec. name must be an
// absolute path (callers typically LookPath first).
func (OSRunner) Replace(name string, args []string, env []string) error {
	return syscall.Exec(name, args, env)
}

// wrap annotates exec errors with the command name and — when present —
// the stderr tail, so callers can produce actionable messages without
// manually stitching the pieces together.
func wrap(name string, args []string, err error, _ *osexec.Cmd) error {
	if ee, ok := err.(*osexec.ExitError); ok && len(ee.Stderr) > 0 {
		return fmt.Errorf("%s %v: %w (stderr: %s)", name, args, err, trimTail(ee.Stderr))
	}
	return fmt.Errorf("%s %v: %w", name, args, err)
}

func trimTail(b []byte) string {
	// Trim trailing newlines so the wrapped message reads as a single line.
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r') {
		b = b[:len(b)-1]
	}
	return string(b)
}
