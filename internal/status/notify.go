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

// NotifyTTL is the libnotify --expire-time we pass to notify-send. Daemons
// that respect -t auto-dismiss after this; daemons that ignore it (notably
// GNOME Shell) still get the per-session dedup via the marker file +
// --replace-id, so stacking is prevented either way.
const NotifyTTL = 60 * time.Second

// NotifyRunner is the exec seam for the notify-watch fork-and-detach plus
// the watcher's notify-send shell-out. Production keeps it pointed at
// exec.Default; tests swap it for a fakerunner.
var NotifyRunner execpkg.Runner = execpkg.Default

// NotifyInputRequired is the fork-and-detach stub the CLI hook and MCP path
// call when a task transitions to input_required. It spawns a detached
// `squash-ide notify-watch <taskID> [message]` so the calling hook process
// can return immediately (Claude Code's hook contract requires hooks to
// finish quickly), while the watcher holds notify-send open with --wait
// and an actionable button. Best-effort — failures here log to stderr and
// are otherwise ignored.
func NotifyInputRequired(taskID, message string) {
	self, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "squash-ide: locating self for notify-watch: %v\n", err)
		return
	}
	args := []string{"notify-watch", taskID, message}
	// setpgid=true gives the watcher its own process group so the parent
	// (the short-lived hook) exiting doesn't drag it down.
	if err := NotifyRunner.Start(self, args, true); err != nil {
		fmt.Fprintf(os.Stderr, "squash-ide: notify-watch spawn failed: %v\n", err)
	}
}

// WatchResult reports the outcome of a NotifyAndWait pipeline.
type WatchResult struct {
	// Clicked is true when the user activated the notification's default
	// action (clicked the body or the "Focus" action button).
	Clicked bool
}

// NotifyAndWait fires notify-send with TTL, --replace-id (when a stale
// marker exists), --print-id, --wait, and --action="default=Focus", and
// blocks until the notification is dismissed, expires, or the user
// activates the action. Per-task marker dedup mirrors the pre-T-034
// behaviour: rapid fires while a notification is on-screen short-circuit;
// fires after TTL pass --replace-id so the daemon slot is reused rather
// than stacked.
//
// Best-effort: errors are logged to stderr and treated as "no action".
// Intended caller is the squash-ide notify-watch subcommand; the parent
// hook path goes through NotifyInputRequired which fork-and-detaches into
// a watcher that calls this.
func NotifyAndWait(ctx context.Context, taskID, message string) WatchResult {
	markerPath := notifyMarkerPath(taskID)
	prevID, fresh := readNotifyMarker(markerPath)
	if fresh && prevID != 0 {
		return WatchResult{}
	}
	args := []string{
		"-u", "critical",
		"-t", strconv.Itoa(int(NotifyTTL / time.Millisecond)),
		"--print-id",
		"--wait",
		"-A", "default=Focus",
	}
	if prevID != 0 {
		args = append(args, "-r", strconv.Itoa(prevID))
	}
	args = append(args,
		fmt.Sprintf("squash-ide: %s needs input", taskID),
		message,
	)
	out, err := NotifyRunner.Output(ctx, "notify-send", args...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "squash-ide: notify-send failed: %v\n", err)
		return WatchResult{}
	}
	newID, action := parseNotifyOutput(out)
	if newID != 0 {
		_ = writeNotifyMarker(markerPath, newID)
	}
	return WatchResult{Clicked: action == "default"}
}

// parseNotifyOutput parses notify-send's stdout. With --print-id the first
// line is the numeric daemon-assigned id; with --wait + --action the line
// after the id is the action key the user activated (only present on
// click — dismissal/timeout emits no action line). Anything that doesn't
// match this shape (e.g. pre-libnotify-0.7.9 without --print-id support)
// returns id=0 and action="".
func parseNotifyOutput(b []byte) (id int, action string) {
	text := strings.TrimRight(string(b), "\n")
	if text == "" {
		return 0, ""
	}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if id == 0 {
			if n, err := strconv.Atoi(line); err == nil {
				id = n
				continue
			}
		}
		if action == "" {
			action = line
		}
	}
	return id, action
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
// fresh (mtime within NotifyTTL). On any read/parse error returns 0, false.
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
	return parsed, time.Since(info.ModTime()) < NotifyTTL
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
