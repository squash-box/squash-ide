package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/squashbox/squash-ide/internal/status"
	"github.com/squashbox/squash-ide/internal/testutil/fakerunner"
)

// statusTempDir redirects the status package's output directory to a fresh
// tempdir for the duration of the test. Returns the dir path so tests can
// assert on file contents. Also redirects the per-task notify-marker dir
// so tests that drive input_required transitions don't leak marker files
// into /tmp/squash-ide/notify/.
func statusTempDir(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	restoreStatus := status.SetDirForTesting(filepath.Join(root, "status"))
	restoreNotify := status.SetNotifyDirForTesting(filepath.Join(root, "notify"))
	t.Cleanup(restoreStatus)
	t.Cleanup(restoreNotify)

	// Stub the notify exec seam. An input_required transition calls
	// status.NotifyInputRequired, which fork-and-detaches os.Executable() with
	// a "notify-watch" arg. Under `go test`, os.Executable() IS this test
	// binary, and a test binary ignores positional args and re-runs the WHOLE
	// suite — which hits the input_required tests again and forks once more:
	// an exponential fork bomb that OOMs the machine (it surfaces as
	// `make test-unit` exhausting RAM even at -p 2). Pointing NotifyRunner at a
	// fakerunner keeps the fork from ever happening; AllowUnexpected lets tests
	// that don't care about the notify call ignore it without wiring an
	// expectation. Production is unaffected: there os.Executable() is the real
	// squash-ide binary and `notify-watch` is a genuine subcommand.
	fr := fakerunner.New(t)
	fr.AllowUnexpected = true
	prevRunner := status.NotifyRunner
	status.NotifyRunner = fr
	t.Cleanup(func() { status.NotifyRunner = prevRunner })

	return filepath.Join(root, "status")
}

func TestStatus_HappyPath_InputRequired(t *testing.T) {
	d := statusTempDir(t)
	t.Setenv("SQUASH_TASK_ID", "T-999")

	if err := runStatus(&cobra.Command{}, []string{"input_required", "Waiting on edit perms"}); err != nil {
		t.Fatalf("runStatus: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(d, "T-999.json"))
	if err != nil {
		t.Fatalf("reading status file: %v", err)
	}
	var f status.File
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatalf("parsing status file: %v", err)
	}
	if f.State != "input_required" {
		t.Errorf("state = %q, want input_required", f.State)
	}
	if f.Message != "Waiting on edit perms" {
		t.Errorf("message = %q", f.Message)
	}
	if f.TaskID != "T-999" {
		t.Errorf("task id = %q", f.TaskID)
	}
}

func TestStatus_AllValidStates(t *testing.T) {
	d := statusTempDir(t)
	t.Setenv("SQUASH_TASK_ID", "T-555")

	for _, state := range []string{"idle", "working", "input_required", "testing"} {
		if err := runStatus(&cobra.Command{}, []string{state, "msg"}); err != nil {
			t.Fatalf("state %q: %v", state, err)
		}
		data, err := os.ReadFile(filepath.Join(d, "T-555.json"))
		if err != nil {
			t.Fatalf("state %q: read: %v", state, err)
		}
		var f status.File
		_ = json.Unmarshal(data, &f)
		if f.State != state {
			t.Errorf("state %q: wrote %q", state, f.State)
		}
	}
}

func TestStatus_MissingEnv(t *testing.T) {
	statusTempDir(t)
	t.Setenv("SQUASH_TASK_ID", "")

	err := runStatus(&cobra.Command{}, []string{"working", "msg"})
	if err == nil {
		t.Fatal("expected err without SQUASH_TASK_ID")
	}
	if !strings.Contains(err.Error(), "SQUASH_TASK_ID") {
		t.Errorf("err should name the missing var: %v", err)
	}
}

func TestStatus_InvalidState(t *testing.T) {
	statusTempDir(t)
	t.Setenv("SQUASH_TASK_ID", "T-001")

	err := runStatus(&cobra.Command{}, []string{"neverheardofit", "x"})
	if err == nil {
		t.Fatal("expected err for invalid state")
	}
	if !strings.Contains(err.Error(), "invalid state") {
		t.Errorf("err should say invalid state: %v", err)
	}
	// All allowed values should be enumerated in the error.
	for _, s := range []string{"idle", "working", "input_required", "testing"} {
		if !strings.Contains(err.Error(), s) {
			t.Errorf("err should enumerate %q: %v", s, err)
		}
	}
}

func TestStatus_OmittedMessage(t *testing.T) {
	d := statusTempDir(t)
	t.Setenv("SQUASH_TASK_ID", "T-111")

	if err := runStatus(&cobra.Command{}, []string{"working"}); err != nil {
		t.Fatalf("runStatus: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(d, "T-111.json"))
	if err != nil {
		t.Fatal(err)
	}
	var f status.File
	_ = json.Unmarshal(data, &f)
	if f.Message != "" {
		t.Errorf("message = %q, want empty", f.Message)
	}
	if f.State != "working" {
		t.Errorf("state = %q", f.State)
	}
}

func TestStatus_InvalidState_NoFileWritten(t *testing.T) {
	d := statusTempDir(t)
	t.Setenv("SQUASH_TASK_ID", "T-222")

	_ = runStatus(&cobra.Command{}, []string{"wat", "msg"})

	if _, err := os.Stat(filepath.Join(d, "T-222.json")); !os.IsNotExist(err) {
		t.Errorf("status file should not exist on invalid state: %v", err)
	}
}

func TestStatusCmd_Registered(t *testing.T) {
	// Sanity check: the subcommand builds and advertises RangeArgs(1,2).
	cmd := newStatusCmd()
	if cmd.Use == "" || !strings.HasPrefix(cmd.Use, "status") {
		t.Errorf("unexpected Use: %q", cmd.Use)
	}
	// Cobra constructs arg validators lazily, so we verify by calling Args.
	if err := cmd.Args(cmd, []string{}); err == nil {
		t.Error("expected err for zero args")
	}
	if err := cmd.Args(cmd, []string{"a", "b", "c"}); err == nil {
		t.Error("expected err for 3 args")
	}
	if err := cmd.Args(cmd, []string{"a"}); err != nil {
		t.Errorf("1 arg should be accepted: %v", err)
	}
	if err := cmd.Args(cmd, []string{"a", "b"}); err != nil {
		t.Errorf("2 args should be accepted: %v", err)
	}
}

func TestIsValidState(t *testing.T) {
	for _, s := range []string{"idle", "working", "input_required", "testing"} {
		if !isValidState(s) {
			t.Errorf("expected %q valid", s)
		}
	}
	for _, s := range []string{"", "idl", "WORKING", "done"} {
		if isValidState(s) {
			t.Errorf("expected %q invalid", s)
		}
	}
}
