package tmux

import (
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"syscall"
)

// Available reports whether the tmux binary is on $PATH.
func Available() bool {
	_, err := exec.LookPath("tmux")
	return err == nil
}

// InSession reports whether the current process is running inside a tmux
// session — tmux sets $TMUX for any process spawned inside a pane.
func InSession() bool {
	return os.Getenv("TMUX") != ""
}

// CurrentPaneID returns the pane ID of the current process, as exported by
// tmux via $TMUX_PANE (e.g. "%4"). Returns "" if not inside tmux.
func CurrentPaneID() string {
	return os.Getenv("TMUX_PANE")
}

// Pane describes a single tmux pane.
type Pane struct {
	ID    string // tmux pane ID, e.g. "%3"
	Left  int    // pane_left — column position in the window
	Width int    // current width in columns
}

// ListWindowPanes returns every pane in the window that contains paneID,
// sorted by Left (leftmost first).
func ListWindowPanes(paneID string) ([]Pane, error) {
	target := paneID
	if target == "" {
		target = ""
	}
	args := []string{"list-panes", "-F", "#{pane_id} #{pane_left} #{pane_width}"}
	if target != "" {
		args = append(args, "-t", target)
	}
	out, err := runOut("tmux", args...)
	if err != nil {
		return nil, fmt.Errorf("tmux list-panes: %w", err)
	}
	var panes []Pane
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 3 {
			return nil, fmt.Errorf("tmux list-panes: unexpected line %q", line)
		}
		left, err := strconv.Atoi(fields[1])
		if err != nil {
			return nil, fmt.Errorf("tmux list-panes: bad pane_left %q: %w", fields[1], err)
		}
		width, err := strconv.Atoi(fields[2])
		if err != nil {
			return nil, fmt.Errorf("tmux list-panes: bad pane_width %q: %w", fields[2], err)
		}
		panes = append(panes, Pane{ID: fields[0], Left: left, Width: width})
	}
	sort.Slice(panes, func(i, j int) bool { return panes[i].Left < panes[j].Left })
	return panes, nil
}

// WindowWidth returns the total column count of the window containing paneID.
func WindowWidth(paneID string) (int, error) {
	args := []string{"display-message", "-p", "#{window_width}"}
	if paneID != "" {
		args = append(args, "-t", paneID)
	}
	out, err := runOut("tmux", args...)
	if err != nil {
		return 0, fmt.Errorf("tmux display-message window_width: %w", err)
	}
	w, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		return 0, fmt.Errorf("parse window_width %q: %w", out, err)
	}
	return w, nil
}

// SplitRight splits the target pane horizontally, opening a new pane to the
// right that runs cmd in cwd. Returns the new pane's ID.
//
// cmd is passed as a single shell-string argument to tmux split-window — tmux
// will execute it via the user's shell. The caller is responsible for shell
// quoting any embedded values (use config.BuildExec).
func SplitRight(target, cwd, cmd string) (string, error) {
	if target == "" {
		return "", fmt.Errorf("tmux SplitRight: target pane required")
	}
	args := []string{
		"split-window", "-h",
		"-t", target,
		"-c", cwd,
		"-P", "-F", "#{pane_id}",
		cmd,
	}
	out, err := runOut("tmux", args...)
	if err != nil {
		return "", fmt.Errorf("tmux split-window: %w", err)
	}
	return strings.TrimSpace(out), nil
}

// ResizePane sets the absolute width (in columns) of the pane.
func ResizePane(paneID string, width int) error {
	if paneID == "" {
		return fmt.Errorf("tmux ResizePane: pane id required")
	}
	if width < 1 {
		return fmt.Errorf("tmux ResizePane: width must be >= 1, got %d", width)
	}
	if _, err := runOut("tmux", "resize-pane", "-t", paneID, "-x", strconv.Itoa(width)); err != nil {
		return fmt.Errorf("tmux resize-pane %s -x %d: %w", paneID, width, err)
	}
	return nil
}

// ReTile pins the TUI pane to tuiWidth and shares the remaining horizontal
// space evenly across the other panes in the same window, refusing if any
// would fall below minPaneWidth. Returns the planned widths for the right
// panes (in left-to-right order) so callers can log/report.
//
// ReTile assumes the spawn has already happened — i.e. the new pane is
// already in the window. It computes layout based on the current window
// width and pane list.
func ReTile(tuiPaneID string, tuiWidth, minPaneWidth int) ([]int, error) {
	if tuiPaneID == "" {
		return nil, fmt.Errorf("tmux ReTile: tui pane id required")
	}
	totalCols, err := WindowWidth(tuiPaneID)
	if err != nil {
		return nil, err
	}
	panes, err := ListWindowPanes(tuiPaneID)
	if err != nil {
		return nil, err
	}
	// Right-side panes = everyone except the TUI.
	right := make([]Pane, 0, len(panes))
	for _, p := range panes {
		if p.ID != tuiPaneID {
			right = append(right, p)
		}
	}
	if len(right) == 0 {
		// Only the TUI is present — nothing to tile, just pin its width.
		return nil, ResizePane(tuiPaneID, tuiWidth)
	}
	widths, err := Tile(totalCols, tuiWidth, len(right), minPaneWidth)
	if err != nil {
		return nil, err
	}
	// Pin the TUI first; tmux re-flows neighbours as we resize, so order
	// matters less than just-finishing-the-job, but pinning the TUI up
	// front ensures the left edge is fixed before we touch the rest.
	if err := ResizePane(tuiPaneID, tuiWidth); err != nil {
		return nil, err
	}
	for i, p := range right {
		if err := ResizePane(p.ID, widths[i]); err != nil {
			return widths, fmt.Errorf("resizing right pane %s: %w", p.ID, err)
		}
	}
	return widths, nil
}

// RightmostRightPaneID returns the pane ID of the rightmost non-TUI pane in
// the window, or "" if no right pane exists. Used to pick the split target
// so new panes append to the right edge in FIFO order.
func RightmostRightPaneID(tuiPaneID string) (string, error) {
	panes, err := ListWindowPanes(tuiPaneID)
	if err != nil {
		return "", err
	}
	rightmost := ""
	rightmostLeft := -1
	for _, p := range panes {
		if p.ID == tuiPaneID {
			continue
		}
		if p.Left > rightmostLeft {
			rightmost = p.ID
			rightmostLeft = p.Left
		}
	}
	return rightmost, nil
}

// EnsureSession either attaches to a tmux session named name (if one exists)
// or creates a new one running cmd, then replaces the current process with
// the tmux client. This function does not return on success — exec replaces
// the process image. It only returns on error (e.g. tmux not found).
//
// cmd should be a fully-formed shell command string (the inner squash-ide
// invocation), not split into argv — tmux will run it via the user's shell.
func EnsureSession(name, cmd string) error {
	binPath, err := exec.LookPath("tmux")
	if err != nil {
		return fmt.Errorf("tmux not found on PATH: %w", err)
	}
	// new-session -A: attach if a session named <name> exists, otherwise
	// create it. -s <name>: name. The trailing argument is the command tmux
	// runs in the first pane.
	args := []string{"tmux", "new-session", "-A", "-s", name, cmd}
	// syscall.Exec replaces the current process. On success it does not
	// return; on failure it returns an error.
	return syscall.Exec(binPath, args, os.Environ())
}

// runOut runs cmd with args and returns combined stdout (trimmed of leading/
// trailing whitespace handled by callers). Stderr is folded into the error
// message on failure so the caller sees what tmux complained about.
func runOut(name string, args ...string) (string, error) {
	c := exec.Command(name, args...)
	out, err := c.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return string(out), fmt.Errorf("%s %s: %w (stderr: %s)",
				name, strings.Join(args, " "), err, strings.TrimSpace(string(ee.Stderr)))
		}
		return string(out), fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return string(out), nil
}
