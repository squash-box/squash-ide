package status

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	execpkg "github.com/squashbox/squash-ide/internal/exec"
	"github.com/squashbox/squash-ide/internal/testutil/fakerunner"
)

// withTempNotifyDir swaps the package-level notify dir to a temp path and
// restores it at test end. Mirrors withTempDir.
func withTempNotifyDir(t *testing.T) string {
	t.Helper()
	d := filepath.Join(t.TempDir(), "notify")
	orig := notifyDirRef
	notifyDirRef = d
	t.Cleanup(func() { notifyDirRef = orig })
	return d
}

// withRunner installs a fake runner as NotifyRunner and restores it at
// test end. Returns the fake so the test can queue expectations and read
// recorded calls.
func withRunner(t *testing.T) *fakerunner.Runner {
	t.Helper()
	r := fakerunner.New(t)
	orig := NotifyRunner
	NotifyRunner = r
	t.Cleanup(func() { NotifyRunner = orig })
	return r
}

func TestNotifyInputRequired_FirstFire_NoMarker(t *testing.T) {
	d := withTempNotifyDir(t)
	r := withRunner(t)

	r.Expect("notify-send",
		"-u", "critical",
		"-t", "60000",
		"--print-id",
		"squash-ide: T-100 needs input",
		"hello",
	).ReturnsOutput([]byte("42\n"))

	NotifyInputRequired("T-100", "hello")

	calls := r.Calls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d: %+v", len(calls), calls)
	}

	data, err := os.ReadFile(filepath.Join(d, "T-100.id"))
	if err != nil {
		t.Fatalf("marker not written: %v", err)
	}
	if got := strings.TrimSpace(string(data)); got != "42" {
		t.Errorf("marker = %q, want 42", got)
	}
}

func TestNotifyInputRequired_FreshMarker_Skipped(t *testing.T) {
	d := withTempNotifyDir(t)
	r := withRunner(t)

	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	markerPath := filepath.Join(d, "T-100.id")
	if err := os.WriteFile(markerPath, []byte("42"), 0o644); err != nil {
		t.Fatal(err)
	}

	NotifyInputRequired("T-100", "second")

	if calls := r.Calls(); len(calls) != 0 {
		t.Errorf("expected zero calls (skip-if-fresh), got %d: %+v", len(calls), calls)
	}

	// Marker must be untouched (id 42 preserved).
	data, _ := os.ReadFile(markerPath)
	if got := strings.TrimSpace(string(data)); got != "42" {
		t.Errorf("marker = %q, want 42 (unchanged)", got)
	}
}

func TestNotifyInputRequired_StaleMarker_UsesReplaceID(t *testing.T) {
	d := withTempNotifyDir(t)
	r := withRunner(t)

	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	markerPath := filepath.Join(d, "T-100.id")
	if err := os.WriteFile(markerPath, []byte("42"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Backdate the marker past notifyTTL.
	old := time.Now().Add(-2 * notifyTTL)
	if err := os.Chtimes(markerPath, old, old); err != nil {
		t.Fatal(err)
	}

	r.Expect("notify-send",
		"-u", "critical",
		"-t", "60000",
		"--print-id",
		"-r", "42",
		"squash-ide: T-100 needs input",
		"third",
	).ReturnsOutput([]byte("43\n"))

	NotifyInputRequired("T-100", "third")

	data, _ := os.ReadFile(markerPath)
	if got := strings.TrimSpace(string(data)); got != "43" {
		t.Errorf("marker = %q, want 43 (refreshed)", got)
	}
}

func TestNotifyInputRequired_PrintIDUnsupported_NoMarker(t *testing.T) {
	d := withTempNotifyDir(t)
	r := withRunner(t)

	r.Expect("notify-send",
		"-u", "critical",
		"-t", "60000",
		"--print-id",
		"squash-ide: T-100 needs input",
		"hi",
	).ReturnsOutput([]byte("")) // older notify-send: no id printed

	NotifyInputRequired("T-100", "hi")

	if _, err := os.Stat(filepath.Join(d, "T-100.id")); !os.IsNotExist(err) {
		t.Errorf("marker should not exist on unparseable output: %v", err)
	}
}

func TestNotifyInputRequired_NotifySendFails_NoPanic(t *testing.T) {
	d := withTempNotifyDir(t)
	r := withRunner(t)

	r.Expect("notify-send",
		"-u", "critical",
		"-t", "60000",
		"--print-id",
		"squash-ide: T-100 needs input",
		"hi",
	).ReturnsExitErr(errors.New("exec: \"notify-send\": executable file not found"))

	// Must not panic.
	NotifyInputRequired("T-100", "hi")

	if _, err := os.Stat(filepath.Join(d, "T-100.id")); !os.IsNotExist(err) {
		t.Errorf("marker should not exist on exec failure: %v", err)
	}
}

func TestNotifyInputRequired_CreatesMarkerDir(t *testing.T) {
	d := withTempNotifyDir(t)
	r := withRunner(t)

	if _, err := os.Stat(d); !os.IsNotExist(err) {
		t.Fatalf("marker dir should not exist yet: %v", err)
	}

	r.Expect("notify-send",
		"-u", "critical",
		"-t", "60000",
		"--print-id",
		"squash-ide: T-200 needs input",
		"first",
	).ReturnsOutput([]byte("7\n"))

	NotifyInputRequired("T-200", "first")

	if _, err := os.Stat(filepath.Join(d, "T-200.id")); err != nil {
		t.Errorf("marker not written / dir not created: %v", err)
	}
}

func TestNotifyInputRequired_CorruptMarker_FiresFresh(t *testing.T) {
	d := withTempNotifyDir(t)
	r := withRunner(t)

	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	markerPath := filepath.Join(d, "T-100.id")
	if err := os.WriteFile(markerPath, []byte("not-a-number"), 0o644); err != nil {
		t.Fatal(err)
	}

	r.Expect("notify-send",
		"-u", "critical",
		"-t", "60000",
		"--print-id",
		"squash-ide: T-100 needs input",
		"hi",
	).ReturnsOutput([]byte("99\n"))

	NotifyInputRequired("T-100", "hi")

	data, _ := os.ReadFile(markerPath)
	if got := strings.TrimSpace(string(data)); got != "99" {
		t.Errorf("marker = %q, want 99 (overwritten)", got)
	}
}

func TestNotifyInputRequired_TwoRapidFires_SecondShortCircuits(t *testing.T) {
	withTempNotifyDir(t)
	r := withRunner(t)

	r.Expect("notify-send",
		"-u", "critical",
		"-t", "60000",
		"--print-id",
		"squash-ide: T-100 needs input",
		"first",
	).ReturnsOutput([]byte("42\n"))

	NotifyInputRequired("T-100", "first")
	NotifyInputRequired("T-100", "second")

	if calls := r.Calls(); len(calls) != 1 {
		t.Errorf("expected 1 call (second short-circuits), got %d: %+v", len(calls), calls)
	}
}

func TestRemoveNotify_MissingIsNoOp(t *testing.T) {
	withTempNotifyDir(t)
	if err := RemoveNotify("T-never-existed"); err != nil {
		t.Errorf("remove missing: %v", err)
	}
}

func TestRemoveNotify_ExistingDeletes(t *testing.T) {
	d := withTempNotifyDir(t)
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	markerPath := filepath.Join(d, "T-010.id")
	if err := os.WriteFile(markerPath, []byte("1"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := RemoveNotify("T-010"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, err := os.Stat(markerPath); !os.IsNotExist(err) {
		t.Errorf("file still present: %v", err)
	}
}

func TestSetNotifyDirForTesting_RestoresOnCall(t *testing.T) {
	orig := notifyDirRef
	restore := SetNotifyDirForTesting("/tmp/scratch")
	if notifyDirRef != "/tmp/scratch" {
		t.Errorf("SetNotifyDirForTesting did not redirect: %q", notifyDirRef)
	}
	restore()
	if notifyDirRef != orig {
		t.Errorf("restore did not return to %q (got %q)", orig, notifyDirRef)
	}
}

// Compile-time check that NotifyRunner is wired through the exec.Runner
// interface (catches future signature drift in one of the seams).
var _ execpkg.Runner = NotifyRunner
