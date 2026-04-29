package status

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	execpkg "github.com/squashbox/squash-ide/internal/exec"
)

// NotifyDir is the directory holding per-task notify-send marker files.
const NotifyDir = "/tmp/squash-ide/notify"

// notifyDirRef is the effective notify directory. Mirrors dirRef in status.go
// so tests can redirect via SetNotifyDirForTesting.
var notifyDirRef = NotifyDir

// notifyTTL is the libnotify --expire-time we pass to notify-send. Daemons
// that respect -t auto-dismiss after this; daemons that ignore it (notably
// GNOME Shell) still get the per-session dedup via the marker file +
// --replace-id, so stacking is prevented either way.
const notifyTTL = 60 * time.Second

// NotifyRunner is the exec seam for the notify-send shell-out. Production
// keeps it pointed at exec.Default; tests swap it for a fakerunner.
var NotifyRunner execpkg.Runner = execpkg.Default

// NotifyInputRequired sends a desktop notification via notify-send so the user
// knows a task pane is blocked awaiting input. Best-effort: notify-send may
// not be installed (e.g. macOS, headless CI), and we don't want that to fail
// the state transition that really matters — the file write. Errors are
// logged.
//
// Behaviour:
//   - If a fresh marker exists for this task (younger than notifyTTL), the
//     call short-circuits — the previous notification is still visible, so
//     piling on another would only create the dismiss-N-times problem.
//   - If a stale marker exists (older than notifyTTL but the recorded id is
//     still a valid daemon slot), we pass --replace-id so the daemon reuses
//     that slot in place rather than stacking.
//   - Otherwise we fire fresh, capturing the daemon-assigned id via
//     --print-id and recording it in the marker for the next call.
func NotifyInputRequired(taskID, message string) {
	markerPath := notifyMarkerPath(taskID)
	prevID, fresh := readNotifyMarker(markerPath)
	if fresh && prevID != 0 {
		return
	}
	args := []string{
		"-u", "critical",
		"-t", strconv.Itoa(int(notifyTTL / time.Millisecond)),
		"--print-id",
	}
	if prevID != 0 {
		args = append(args, "-r", strconv.Itoa(prevID))
	}
	args = append(args,
		fmt.Sprintf("squash-ide: %s needs input", taskID),
		message,
	)
	out, err := NotifyRunner.Output(context.Background(), "notify-send", args...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "squash-ide: notify-send failed: %v\n", err)
		return
	}
	newID, perr := strconv.Atoi(strings.TrimSpace(string(out)))
	if perr != nil {
		// notify-send pre-0.7.9 lacks --print-id. Skip the marker; next
		// call will fire fresh (no dedup but TTL still applies).
		return
	}
	_ = writeNotifyMarker(markerPath, newID)
}

// SetNotifyDirForTesting redirects the effective notify-marker directory to
// dir and returns a restore func. Mirrors SetDirForTesting.
func SetNotifyDirForTesting(dir string) (restore func()) {
	prev := notifyDirRef
	notifyDirRef = dir
	return func() { notifyDirRef = prev }
}

// RemoveNotify deletes the per-task notify marker file. It is not an error
// if the file does not exist. Called on transitions out of input_required
// (working/idle hooks) and on task end (Complete/Deactivate) so the next
// genuine input_required raises a fresh, visible notification.
func RemoveNotify(taskID string) error {
	err := os.Remove(notifyMarkerPath(taskID))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func notifyMarkerPath(taskID string) string {
	return filepath.Join(notifyDirRef, taskID+".id")
}

// readNotifyMarker returns the recorded notify-send id and whether it is
// fresh (mtime within notifyTTL). On any read/parse error returns 0, false.
func readNotifyMarker(path string) (id int, fresh bool) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	parsed, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, false
	}
	return parsed, time.Since(info.ModTime()) < notifyTTL
}

// writeNotifyMarker atomically writes id to path. Mirrors the temp-then-
// rename idiom in Write at status.go.
func writeNotifyMarker(path string, id int) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(strconv.Itoa(id)), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
