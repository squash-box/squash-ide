package status

import (
	"context"
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

func TestNotifyInputRequired_StartsDetachedWatcher(t *testing.T) {
	withTempNotifyDir(t)
	r := withRunner(t)

	self, err := os.Executable()
	if err != nil {
		t.Skipf("os.Executable failed: %v", err)
	}
	r.Expect(self, "notify-watch", "T-100", "hi")

	NotifyInputRequired("T-100", "hi")

	calls := r.Calls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 Start call, got %d: %+v", len(calls), calls)
	}
	if calls[0].Kind != "Start" {
		t.Errorf("kind = %q, want Start", calls[0].Kind)
	}
	if calls[0].Name != self {
		t.Errorf("name = %q, want %q (self)", calls[0].Name, self)
	}
	wantArgs := []string{"notify-watch", "T-100", "hi"}
	if !equalArgs(calls[0].Args, wantArgs) {
		t.Errorf("args = %v, want %v", calls[0].Args, wantArgs)
	}
}

func TestNotifyInputRequired_StartFailLogged(t *testing.T) {
	withTempNotifyDir(t)
	r := withRunner(t)

	self, err := os.Executable()
	if err != nil {
		t.Skipf("os.Executable failed: %v", err)
	}
	r.Expect(self, "notify-watch", "T-100", "hi").
		ReturnsExitErr(errors.New("fork failed"))

	// Must not panic; status side effects (caller's WriteFile) must be
	// unaffected — verified at the caller (cmd/squash-ide/status_test.go).
	NotifyInputRequired("T-100", "hi")
}

func TestNotifyAndWait_FirstFire_NoMarker(t *testing.T) {
	d := withTempNotifyDir(t)
	r := withRunner(t)

	r.Expect("notify-send",
		"-u", "critical",
		"-t", "60000",
		"--print-id",
		"--wait",
		"-A", "default=Focus",
		"squash-ide: T-100 needs input",
		"hello",
	).ReturnsOutput([]byte("42\n"))

	res := NotifyAndWait(context.Background(), "T-100", "hello")
	if res.Clicked {
		t.Errorf("Clicked = true with no action emitted")
	}

	data, err := os.ReadFile(filepath.Join(d, "T-100.id"))
	if err != nil {
		t.Fatalf("marker not written: %v", err)
	}
	if got := strings.TrimSpace(string(data)); got != "42" {
		t.Errorf("marker = %q, want 42", got)
	}
}

func TestNotifyAndWait_ClickedAction(t *testing.T) {
	withTempNotifyDir(t)
	r := withRunner(t)

	r.Expect("notify-send",
		"-u", "critical",
		"-t", "60000",
		"--print-id",
		"--wait",
		"-A", "default=Focus",
		"squash-ide: T-200 needs input",
		"perm needed",
	).ReturnsOutput([]byte("9\ndefault\n"))

	res := NotifyAndWait(context.Background(), "T-200", "perm needed")
	if !res.Clicked {
		t.Errorf("Clicked = false, want true on default action")
	}
}

func TestNotifyAndWait_NonDefaultAction_NotClicked(t *testing.T) {
	withTempNotifyDir(t)
	r := withRunner(t)

	r.Expect("notify-send",
		"-u", "critical",
		"-t", "60000",
		"--print-id",
		"--wait",
		"-A", "default=Focus",
		"squash-ide: T-300 needs input",
		"x",
	).ReturnsOutput([]byte("11\nclose\n"))

	res := NotifyAndWait(context.Background(), "T-300", "x")
	if res.Clicked {
		t.Errorf("Clicked = true on non-default action")
	}
}

func TestNotifyAndWait_FreshMarker_Skipped(t *testing.T) {
	d := withTempNotifyDir(t)
	r := withRunner(t)

	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	markerPath := filepath.Join(d, "T-100.id")
	if err := os.WriteFile(markerPath, []byte("42"), 0o644); err != nil {
		t.Fatal(err)
	}

	res := NotifyAndWait(context.Background(), "T-100", "second")
	if res.Clicked {
		t.Errorf("Clicked = true when short-circuited")
	}
	if calls := r.Calls(); len(calls) != 0 {
		t.Errorf("expected zero calls (skip-if-fresh), got %d: %+v", len(calls), calls)
	}

	data, _ := os.ReadFile(markerPath)
	if got := strings.TrimSpace(string(data)); got != "42" {
		t.Errorf("marker = %q, want 42 (unchanged)", got)
	}
}

func TestNotifyAndWait_StaleMarker_UsesReplaceID(t *testing.T) {
	d := withTempNotifyDir(t)
	r := withRunner(t)

	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	markerPath := filepath.Join(d, "T-100.id")
	if err := os.WriteFile(markerPath, []byte("42"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * NotifyTTL)
	if err := os.Chtimes(markerPath, old, old); err != nil {
		t.Fatal(err)
	}

	r.Expect("notify-send",
		"-u", "critical",
		"-t", "60000",
		"--print-id",
		"--wait",
		"-A", "default=Focus",
		"-r", "42",
		"squash-ide: T-100 needs input",
		"third",
	).ReturnsOutput([]byte("43\n"))

	NotifyAndWait(context.Background(), "T-100", "third")

	data, _ := os.ReadFile(markerPath)
	if got := strings.TrimSpace(string(data)); got != "43" {
		t.Errorf("marker = %q, want 43 (refreshed)", got)
	}
}

func TestNotifyAndWait_PrintIDUnsupported_NoMarker(t *testing.T) {
	d := withTempNotifyDir(t)
	r := withRunner(t)

	r.Expect("notify-send",
		"-u", "critical",
		"-t", "60000",
		"--print-id",
		"--wait",
		"-A", "default=Focus",
		"squash-ide: T-100 needs input",
		"hi",
	).ReturnsOutput([]byte("")) // older notify-send: no id printed

	NotifyAndWait(context.Background(), "T-100", "hi")

	if _, err := os.Stat(filepath.Join(d, "T-100.id")); !os.IsNotExist(err) {
		t.Errorf("marker should not exist on unparseable output: %v", err)
	}
}

func TestNotifyAndWait_NotifySendFails_NoPanic(t *testing.T) {
	d := withTempNotifyDir(t)
	r := withRunner(t)

	r.Expect("notify-send",
		"-u", "critical",
		"-t", "60000",
		"--print-id",
		"--wait",
		"-A", "default=Focus",
		"squash-ide: T-100 needs input",
		"hi",
	).ReturnsExitErr(errors.New("exec: \"notify-send\": executable file not found"))

	res := NotifyAndWait(context.Background(), "T-100", "hi")
	if res.Clicked {
		t.Errorf("Clicked = true on exec failure")
	}

	if _, err := os.Stat(filepath.Join(d, "T-100.id")); !os.IsNotExist(err) {
		t.Errorf("marker should not exist on exec failure: %v", err)
	}
}

func TestNotifyAndWait_CreatesMarkerDir(t *testing.T) {
	d := withTempNotifyDir(t)
	r := withRunner(t)

	if _, err := os.Stat(d); !os.IsNotExist(err) {
		t.Fatalf("marker dir should not exist yet: %v", err)
	}

	r.Expect("notify-send",
		"-u", "critical",
		"-t", "60000",
		"--print-id",
		"--wait",
		"-A", "default=Focus",
		"squash-ide: T-200 needs input",
		"first",
	).ReturnsOutput([]byte("7\n"))

	NotifyAndWait(context.Background(), "T-200", "first")

	if _, err := os.Stat(filepath.Join(d, "T-200.id")); err != nil {
		t.Errorf("marker not written / dir not created: %v", err)
	}
}

func TestNotifyAndWait_CorruptMarker_FiresFresh(t *testing.T) {
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
		"--wait",
		"-A", "default=Focus",
		"squash-ide: T-100 needs input",
		"hi",
	).ReturnsOutput([]byte("99\n"))

	NotifyAndWait(context.Background(), "T-100", "hi")

	data, _ := os.ReadFile(markerPath)
	if got := strings.TrimSpace(string(data)); got != "99" {
		t.Errorf("marker = %q, want 99 (overwritten)", got)
	}
}

func TestParseNotifyOutput(t *testing.T) {
	cases := []struct {
		name       string
		in         string
		wantID     int
		wantAction string
	}{
		{"empty", "", 0, ""},
		{"id only", "42\n", 42, ""},
		{"id and action", "42\ndefault\n", 42, "default"},
		{"trailing newlines", "7\ndefault\n\n", 7, "default"},
		{"non-numeric only", "some banner\n", 0, "some banner"},
		{"action without id", "\ndefault\n", 0, "default"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id, action := parseNotifyOutput([]byte(tc.in))
			if id != tc.wantID {
				t.Errorf("id = %d, want %d", id, tc.wantID)
			}
			if action != tc.wantAction {
				t.Errorf("action = %q, want %q", action, tc.wantAction)
			}
		})
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

func equalArgs(a, b []string) bool {
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

// Compile-time check that NotifyRunner is wired through the exec.Runner
// interface (catches future signature drift in one of the seams).
var _ execpkg.Runner = NotifyRunner
