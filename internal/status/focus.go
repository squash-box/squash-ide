package status

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Focus-request IPC for the native engine (T-039).
//
// In tmux mode the notify-watch subcommand focuses a task's pane directly with
// `tmux switch-client`/`select-pane` ([[T-034]]). The native engine has no tmux
// to drive — the pane lives inside the running TUI process, which the separate
// notify-watch process cannot reach in-memory. This file is the bridge: the
// watcher drops a small request marker, the TUI consumes it on its next status
// poll and calls Manager.FocusByTask.
//
// It deliberately reuses the file-IPC shape the status pipeline already trusts
// (a /tmp/squash-ide subdir + a test-redirect seam), staying tmux-free so the
// dispatch → status layering boundary [[T-034]] kept is preserved: status writes
// files, it never imports internal/tmux or internal/pane.

// FocusDir is the directory holding the focus-request marker.
const FocusDir = "/tmp/squash-ide/focus"

// focusDirRef is the effective focus directory. Mirrors dirRef/notifyDirRef so
// tests can redirect via SetFocusDirForTesting.
var focusDirRef = FocusDir

// focusFile is the single marker file name. There is at most one outstanding
// focus request — the latest click wins, so a new request overwrites any
// pending one rather than queuing.
const focusFile = "request.json"

// FocusStaleDuration bounds how long a focus request is honoured. A request the
// TUI never consumed (because no TUI was running when the notification was
// clicked) is dropped rather than acted on much later — TakeFocusRequest treats
// an older marker as absent. This is what makes notify-click "degrade gracefully
// when the TUI isn't running" instead of stealing focus on the next launch.
const FocusStaleDuration = 60 * time.Second

// focusRequest is the on-disk marker payload.
type focusRequest struct {
	TaskID  string `json:"task_id"`
	Updated int64  `json:"updated"` // unix timestamp
}

func focusPath() string { return filepath.Join(focusDirRef, focusFile) }

// RequestFocus records that taskID should be brought to the foreground. Called
// by the notify-watch process on click in native mode. Atomic (temp + rename)
// so the polling TUI never reads a half-written marker.
func RequestFocus(taskID string) error {
	if taskID == "" {
		return nil
	}
	if err := os.MkdirAll(focusDirRef, 0755); err != nil {
		return err
	}
	data, err := json.Marshal(focusRequest{TaskID: taskID, Updated: time.Now().Unix()})
	if err != nil {
		return err
	}
	tmp := focusPath() + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, focusPath())
}

// TakeFocusRequest reads and removes the pending focus request, returning its
// task id. ok is false when there is no request, the marker is unreadable, or it
// has aged past FocusStaleDuration. The marker is removed on every call (even a
// stale one) so a request is consumed exactly once and a stale one is cleaned up
// rather than retried forever.
func TakeFocusRequest() (taskID string, ok bool) {
	path := focusPath()
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false // no request (or unreadable) — nothing to do
	}
	_ = os.Remove(path) // consume exactly once

	var req focusRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return "", false
	}
	if req.TaskID == "" {
		return "", false
	}
	if req.Updated < time.Now().Add(-FocusStaleDuration).Unix() {
		return "", false // stale — the TUI wasn't running when it was clicked
	}
	return req.TaskID, true
}

// SetFocusDirForTesting redirects the effective focus directory to dir and
// returns a restore func. Mirrors SetDirForTesting / SetNotifyDirForTesting.
func SetFocusDirForTesting(dir string) (restore func()) {
	prev := focusDirRef
	focusDirRef = dir
	return func() { focusDirRef = prev }
}
