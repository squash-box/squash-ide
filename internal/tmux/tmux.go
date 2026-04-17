package tmux

import (
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"github.com/charmbracelet/x/term"
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
	ID     string // tmux pane ID, e.g. "%3"
	Left   int    // pane_left — column position in the window
	Width  int    // current width in columns
	Height int    // current height in rows
}

// ListWindowPanes returns every pane in the window that contains paneID,
// sorted by Left (leftmost first).
func ListWindowPanes(paneID string) ([]Pane, error) {
	target := paneID
	if target == "" {
		target = ""
	}
	args := []string{"list-panes", "-F", "#{pane_id} #{pane_left} #{pane_width} #{pane_height}"}
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
		if len(fields) != 4 {
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
		height, err := strconv.Atoi(fields[3])
		if err != nil {
			return nil, fmt.Errorf("tmux list-panes: bad pane_height %q: %w", fields[3], err)
		}
		panes = append(panes, Pane{ID: fields[0], Left: left, Width: width, Height: height})
	}
	sort.Slice(panes, func(i, j int) bool { return panes[i].Left < panes[j].Left })
	return panes, nil
}

// WindowWidth returns the total column count of the window containing paneID.
func WindowWidth(paneID string) (int, error) {
	// Flag order matters: tmux's display-message parses the format string
	// as the trailing positional argument, so any -t must come *before*
	// that string — otherwise tmux sees two positionals and rejects with
	// "too many arguments".
	args := []string{"display-message", "-p"}
	if paneID != "" {
		args = append(args, "-t", paneID)
	}
	args = append(args, "#{window_width}")
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
	}
	// Only pass -c when a cwd is specified; tmux dislikes empty -c.
	if cwd != "" {
		args = append(args, "-c", cwd)
	}
	args = append(args, "-P", "-F", "#{pane_id}", cmd)
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

// ReTile pins the TUI pane to tuiWidth and divides the remaining
// horizontal space equally among task columns. Each column gets at
// least paneWidth (or minPaneWidth if paneWidth is 0); the spawn is
// rejected if that minimum cannot be met. Any leftover columns from
// integer division are distributed one-per-column to the leftmost panes.
//
// Right-side panes are grouped by column (same Left value) so that
// header+content vertical pairs count as one column.
func ReTile(tuiPaneID string, tuiWidth, paneWidth, minPaneWidth int) ([]int, error) {
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

	// Collect all non-TUI panes (each is its own column).
	var columns []Pane
	for _, p := range panes {
		if p.ID != tuiPaneID {
			columns = append(columns, p)
		}
	}
	if len(columns) == 0 {
		return nil, ResizePane(tuiPaneID, tuiWidth)
	}

	// Use paneWidth as the floor; fall back to minPaneWidth.
	floor := minPaneWidth
	if paneWidth > floor {
		floor = paneWidth
	}

	widths, err := Tile(totalCols, tuiWidth, len(columns), floor)
	if err != nil {
		return nil, err
	}

	// Pin TUI first, then resize all task columns except the last one.
	// The last column absorbs the remainder naturally, which avoids the
	// "neighbor stealing" problem where sequential resize-pane calls on
	// adjacent panes fight each other.
	if err := ResizePane(tuiPaneID, tuiWidth); err != nil {
		return nil, err
	}
	for i := 0; i < len(columns)-1; i++ {
		if err := ResizePane(columns[i].ID, widths[i]); err != nil {
			return widths, fmt.Errorf("resizing column at left=%d: %w", columns[i].Left, err)
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

// EnsureSessionWithPlaceholder is like EnsureSession but, when creating a
// fresh session, also splits a right-hand placeholder pane running
// placeholderCmd and pins the TUI pane to tuiWidth columns. The placeholder
// pane is tagged with @squash-role=placeholder so the spawner can find and
// kill it on first task activation.
//
// Existing sessions are attached as-is — the placeholder/TUI layout inside
// the session is whatever it is; this function does not reshape it.
//
// Like EnsureSession, this function does not return on success (exec
// replaces the process). It only returns on setup failure.
func EnsureSessionWithPlaceholder(name, tuiCmd string, tuiWidth int) error {
	binPath, err := exec.LookPath("tmux")
	if err != nil {
		return fmt.Errorf("tmux not found on PATH: %w", err)
	}

	// Attach path: session already exists — don't reshape, just attach.
	if hasSession(name) {
		args := []string{"tmux", "attach-session", "-t", name}
		return syscall.Exec(binPath, args, os.Environ())
	}

	// Fresh session path: create detached, split, tag, resize, then attach.
	//
	// -d keeps us out of the client until we've built the layout, so the
	// user doesn't briefly see a single-pane session flash into a split.
	//
	// -x/-y size the session to the CURRENT terminal so that the attach
	// below doesn't trigger a proportional rescale of the panes we're
	// about to set up. Without this, tmux would create the session at its
	// default (~80x24) and then stretch everything on attach — undoing
	// the TUI width pin.
	newArgs := []string{"new-session", "-d", "-s", name}
	if w, h, err := term.GetSize(os.Stdout.Fd()); err == nil && w > 0 && h > 0 {
		newArgs = append(newArgs, "-x", strconv.Itoa(w), "-y", strconv.Itoa(h))
	}
	newArgs = append(newArgs, tuiCmd)
	if _, err := runOut("tmux", newArgs...); err != nil {
		return fmt.Errorf("tmux new-session: %w", err)
	}

	tuiPane, err := firstPaneID(name)
	if err != nil {
		// Best-effort cleanup: kill the half-built session so the next
		// invocation starts clean instead of attaching to a broken layout.
		_ = exec.Command("tmux", "kill-session", "-t", name).Run()
		return fmt.Errorf("locating tui pane after new-session: %w", err)
	}

	// Tag the TUI so the resize hook and role-based lookups can find it.
	if err := SetPaneRole(tuiPane, RoleTUI); err != nil {
		fmt.Fprintf(os.Stderr, "warning: tagging tui pane: %v\n", err)
	}
	_ = SetPaneBorderFormat(tuiPane, "")

	// NOTE: TUI resize and placeholder/task pane creation are NOT done
	// here. The inner process (runTUI → respawnActive) handles ALL
	// right-side pane creation and tiling after checking the vault.
	// This avoids races between the bootstrap and the inner process.

	// Chrome: the squash-ide TUI owns its own header/footer, so tmux's
	// default status bar is noise. And the active/inactive pane-border
	// colours default to green/grey, which looks like a painted half-
	// border where our TUI meets the placeholder — flatten both to a
	// single muted grey so the split reads as a clean divider.
	//
	// Note the scope flag: `status` is session-scoped (-t), while the
	// pane-border styles are window-scoped (-w -t). Using the wrong scope
	// silently succeeds but doesn't apply the setting.
	chromeOpts := []struct {
		scope, key, value string
	}{
		{"-t", "status", "off"},
		{"-t", "mouse", "on"},
		{"-w", "pane-border-style", "fg=colour240"},
		{"-w", "pane-active-border-style", "fg=colour240"},
		{"-w", "pane-border-status", "top"},
	}
	for _, opt := range chromeOpts {
		args := []string{"set-option"}
		if opt.scope == "-w" {
			args = append(args, "-w", "-t", name)
		} else {
			args = append(args, "-t", name)
		}
		args = append(args, opt.key, opt.value)
		if _, err := runOut("tmux", args...); err != nil {
			fmt.Fprintf(os.Stderr, "warning: setting %s: %v\n", opt.key, err)
		}
	}

	// Install a client-resized hook that calls `squash-ide retile` to
	// re-pin the TUI and distribute space equally among task panes.
	// Using a subcommand avoids shell escaping issues in run-shell.
	hookCmd := fmt.Sprintf("run-shell '%s retile --in-tmux 2>/dev/null; true'", os.Args[0])
	if _, err := runOut("tmux", "set-hook", "-t", name, "client-resized", hookCmd); err != nil {
		fmt.Fprintf(os.Stderr, "warning: installing resize hook: %v\n", err)
	}

	args := []string{"tmux", "attach-session", "-t", name}
	return syscall.Exec(binPath, args, os.Environ())
}

// hasSession reports whether a tmux session with the given name exists.
func hasSession(name string) bool {
	err := exec.Command("tmux", "has-session", "-t", name).Run()
	return err == nil
}

// firstPaneID returns the pane ID of the first (and, in a freshly created
// session, only) pane in the named session.
func firstPaneID(session string) (string, error) {
	out, err := runOut("tmux", "list-panes", "-t", session, "-F", "#{pane_id}")
	if err != nil {
		return "", err
	}
	line := strings.TrimSpace(out)
	if line == "" {
		return "", fmt.Errorf("tmux list-panes returned no panes for session %s", session)
	}
	// If tmux somehow returns multiple panes, take the first.
	if idx := strings.Index(line, "\n"); idx >= 0 {
		line = line[:idx]
	}
	return line, nil
}

// --- Pane role tagging ------------------------------------------------------
//
// We tag panes we create (placeholder, future special panes) with a tmux
// user option @squash-role=<role>. This lets subsequent callers locate panes
// by purpose without tracking IDs in a side channel.

// Role is the value stored under @squash-role.
type Role string

const (
	// RolePlaceholder marks the right-hand "no active tasks" pane.
	RolePlaceholder Role = "placeholder"
	// RoleTUI marks the pane running the squash-ide TUI itself.
	RoleTUI Role = "tui"
)

// SetPaneRole tags a pane with @squash-role=<role>.
func SetPaneRole(paneID string, role Role) error {
	if paneID == "" {
		return fmt.Errorf("tmux SetPaneRole: pane id required")
	}
	if _, err := runOut("tmux", "set-option", "-pt", paneID, "@squash-role", string(role)); err != nil {
		return fmt.Errorf("tmux set-option @squash-role: %w", err)
	}
	return nil
}

// FindPaneByRole returns the first pane in the window containing windowTarget
// whose @squash-role matches role, or "" if no such pane exists. windowTarget
// can be any pane in the window — tmux resolves the containing window.
func FindPaneByRole(windowTarget string, role Role) (string, error) {
	if windowTarget == "" {
		return "", fmt.Errorf("tmux FindPaneByRole: window target required")
	}
	out, err := runOut("tmux", "list-panes", "-t", windowTarget,
		"-F", "#{pane_id} #{@squash-role}")
	if err != nil {
		return "", fmt.Errorf("tmux list-panes: %w", err)
	}
	want := string(role)
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] == want {
			return fields[0], nil
		}
	}
	return "", nil
}

// ToggleZoom toggles the zoom state of a pane. When zoomed, the pane fills
// the entire window; when unzoomed, it returns to its normal size.
func ToggleZoom(paneID string) {
	if paneID != "" {
		_, _ = runOut("tmux", "resize-pane", "-Z", "-t", paneID)
	}
}

// SetPaneBorderFormat sets the per-pane border format string. This is
// rendered by tmux in the pane-border-status line for this specific pane.
func SetPaneBorderFormat(paneID, format string) error {
	if paneID == "" {
		return nil
	}
	_, err := runOut("tmux", "set-option", "-p", "-t", paneID, "pane-border-format", format)
	return err
}

// SelectPane makes the given pane the active (focused) pane in its window.
func SelectPane(paneID string) (string, error) {
	return runOut("tmux", "select-pane", "-t", paneID)
}

// SetPaneOption sets a user-defined tmux option on a pane.
func SetPaneOption(paneID, key, value string) error {
	if paneID == "" {
		return fmt.Errorf("tmux SetPaneOption: pane id required")
	}
	if _, err := runOut("tmux", "set-option", "-pt", paneID, key, value); err != nil {
		return fmt.Errorf("tmux set-option %s: %w", key, err)
	}
	return nil
}

// SetPaneTask tags a pane with @squash-task=<taskID> so the deactivate
// flow can locate the pane associated with a given task.
func SetPaneTask(paneID, taskID string) error {
	if paneID == "" || taskID == "" {
		return nil
	}
	if _, err := runOut("tmux", "set-option", "-pt", paneID, "@squash-task", taskID); err != nil {
		return fmt.Errorf("tmux set-option @squash-task: %w", err)
	}
	return nil
}

// FindPaneByTask returns the first pane in the window whose @squash-task
// matches taskID, or "" if no such pane exists.
func FindPaneByTask(windowTarget, taskID string) (string, error) {
	if windowTarget == "" || taskID == "" {
		return "", nil
	}
	out, err := runOut("tmux", "list-panes", "-t", windowTarget,
		"-F", "#{pane_id} #{@squash-task}")
	if err != nil {
		return "", fmt.Errorf("tmux list-panes: %w", err)
	}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] == taskID {
			return fields[0], nil
		}
	}
	return "", nil
}

// FindPaneByOption returns the first pane in the window whose user option
// named optName matches value, or "" if no such pane exists. This is the
// generic form — callers like FindPaneByTask and dispatch.Deactivate use
// it to locate panes tagged with @squash-header, @squash-task, etc.
func FindPaneByOption(windowTarget, optName, value string) (string, error) {
	if windowTarget == "" || optName == "" {
		return "", nil
	}
	fmtStr := fmt.Sprintf("#{pane_id} #{%s}", optName)
	out, err := runOut("tmux", "list-panes", "-t", windowTarget, "-F", fmtStr)
	if err != nil {
		return "", fmt.Errorf("tmux list-panes: %w", err)
	}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] == value {
			return fields[0], nil
		}
	}
	return "", nil
}

// GetPaneOption returns the value of a user-defined tmux option on a pane,
// or "" if the option is not set.
func GetPaneOption(paneID, key string) (string, error) {
	if paneID == "" {
		return "", nil
	}
	fmtStr := fmt.Sprintf("#{%s}", key)
	out, err := runOut("tmux", "display-message", "-p", "-t", paneID, fmtStr)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// CountPanesByOption returns how many panes in the window have a non-empty
// value for the given user option.
func CountPanesByOption(windowTarget, optName string) (int, error) {
	if windowTarget == "" {
		return 0, nil
	}
	fmtStr := fmt.Sprintf("#{pane_id} #{%s}", optName)
	out, err := runOut("tmux", "list-panes", "-t", windowTarget, "-F", fmtStr)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] != "" {
			count++
		}
	}
	return count, nil
}

// SpawnPlaceholder creates a right-hand placeholder pane next to the TUI,
// tagged with @squash-role=placeholder. Used to restore the placeholder
// after all task panes have been deactivated. The placeholder runs
// `squash-ide placeholder --in-tmux`.
func SpawnPlaceholder(tuiPaneID string, tuiWidth int) error {
	// Build the placeholder command. os.Args[0] is the squash-ide binary.
	bin := os.Args[0]
	placeholderCmd := bin + " placeholder --in-tmux"

	phPane, err := SplitRight(tuiPaneID, "", placeholderCmd)
	if err != nil {
		return fmt.Errorf("spawning placeholder: %w", err)
	}
	if err := SetPaneRole(phPane, RolePlaceholder); err != nil {
		return fmt.Errorf("tagging placeholder: %w", err)
	}
	if err := ResizePane(tuiPaneID, tuiWidth); err != nil {
		return fmt.Errorf("re-pinning tui: %w", err)
	}
	// Keep focus on the TUI.
	_, _ = SelectPane(tuiPaneID)
	return nil
}

// KillPane closes a pane by ID. No-op if paneID is empty.
func KillPane(paneID string) error {
	if paneID == "" {
		return nil
	}
	if err := exec.Command("tmux", "kill-pane", "-t", paneID).Run(); err != nil {
		return fmt.Errorf("tmux kill-pane %s: %w", paneID, err)
	}
	return nil
}

// KillSession terminates a tmux session by name. No-op if the session
// doesn't exist (tmux returns non-zero, we swallow that case).
func KillSession(name string) error {
	if name == "" {
		return fmt.Errorf("tmux KillSession: name required")
	}
	// Silence output — "no such session" is expected on a no-op.
	cmd := exec.Command("tmux", "kill-session", "-t", name)
	_ = cmd.Run()
	return nil
}

// CurrentSessionName returns the name of the tmux session containing the
// current pane, or "" if not in tmux / the lookup fails.
func CurrentSessionName() string {
	out, err := runOut("tmux", "display-message", "-p", "#S")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
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
