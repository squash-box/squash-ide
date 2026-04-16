package spawner

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"github.com/squashbox/squash-ide/internal/config"
	"github.com/squashbox/squash-ide/internal/tmux"
)

// terminal describes how to invoke a terminal emulator when auto-detecting.
type terminal struct {
	bin  string
	args func(workdir, execCmd string) []string
}

// terminals is the ordered list of terminal emulators to try when the
// user has not configured a specific terminal.
var terminals = []terminal{
	{
		bin: "ptyxis",
		args: func(workdir, execCmd string) []string {
			return []string{"-d", workdir, "--", "bash", "-c", execCmd}
		},
	},
	{
		bin: "gnome-terminal",
		args: func(workdir, execCmd string) []string {
			return []string{"--working-directory=" + workdir, "--", "bash", "-c", execCmd}
		},
	},
	{
		bin: "x-terminal-emulator",
		args: func(workdir, execCmd string) []string {
			// Debian alternatives — flag support varies, use -e
			return []string{"-e", "bash -c 'cd " + workdir + " && " + execCmd + "'"}
		},
	},
}

// SpawnWith dispatches a task: it renders the spawn command from cfg.Spawn
// (with templating) and either splits a new tmux pane to the right of the
// TUI (T-011 default) or opens a fresh OS terminal window (v1 fallback).
//
// vars is the templating context passed to spawn.args (and, for the OS-window
// path, terminal.args); required keys are at least {cwd}, {task_id},
// {worktree}, {repo}, {branch}. For the OS-window path the spawner additionally
// substitutes {exec} into terminal.args as the rendered spawn command string.
//
// Path selection:
//   - If cfg.Tmux.Enabled and we are inside a tmux session, use tmux split-window.
//   - Else if cfg.Terminal.Command is set, invoke that terminal binary.
//   - Else fall back to the built-in auto-detect list
//     (ptyxis → gnome-terminal → x-terminal-emulator), preserving T-007 behavior.
//
// OS-window spawns are detached via Setpgid so they survive if the parent exits.
func SpawnWith(cfg config.Config, vars map[string]string) error {
	// Render the spawn command (runs inside the terminal/pane) with templating.
	spawnArgs := config.ExpandAll(cfg.Spawn.Args, vars)
	execCmd := config.BuildExec(cfg.Spawn.Command, spawnArgs)

	if cfg.Tmux.Enabled && tmux.InSession() {
		return runTmux(cfg.Tmux, vars["cwd"], execCmd, vars["task_id"])
	}

	// Add {exec} to the templating context for terminal.args substitution.
	termVars := make(map[string]string, len(vars)+1)
	for k, v := range vars {
		termVars[k] = v
	}
	termVars["exec"] = execCmd

	if cfg.Terminal.Command != "" {
		return runConfigured(cfg.Terminal, termVars)
	}
	return runAutoDetect(vars["cwd"], execCmd)
}

// runTmux opens a new pane to the right of the TUI and re-tiles the
// non-TUI panes to share the available width equally. The TUI pane's width
// is pinned to cfg.TUIWidth. If the new pane would force any pane below
// cfg.MinPaneWidth, the spawn is aborted and the new pane (already created)
// is killed so the caller doesn't accumulate orphan panes on rejection.
func runTmux(t config.Tmux, cwd, execCmd, taskID string) error {
	tuiPane := tmux.CurrentPaneID()
	if tuiPane == "" {
		return fmt.Errorf("tmux: $TMUX_PANE not set — cannot determine TUI pane")
	}

	// If the right-hand placeholder pane is still up, kill it before
	// splitting — the first task should replace the placeholder, not sit
	// next to it. Lookup failures are non-fatal (we can still spawn; the
	// placeholder will just get orphaned and the user can close it).
	if phPane, err := tmux.FindPaneByRole(tuiPane, tmux.RolePlaceholder); err == nil && phPane != "" {
		if killErr := tmux.KillPane(phPane); killErr != nil {
			fmt.Fprintf(os.Stderr, "warning: could not close placeholder pane: %v\n", killErr)
		}
	}

	// Pick split target: rightmost existing right pane (so spawns append
	// left → right), or the TUI pane if no right panes exist yet.
	target, err := tmux.RightmostRightPaneID(tuiPane)
	if err != nil {
		return fmt.Errorf("tmux: locating split target: %w", err)
	}
	if target == "" {
		target = tuiPane
	}

	newPane, err := tmux.SplitRight(target, cwd, execCmd)
	if err != nil {
		return fmt.Errorf("tmux: splitting pane: %w", err)
	}

	// Tag the pane with the task ID so deactivate/complete can locate and
	// kill it later. Non-fatal — the pane still works without the tag.
	if taskID != "" {
		if tagErr := tmux.SetPaneTask(newPane, taskID); tagErr != nil {
			fmt.Fprintf(os.Stderr, "warning: tagging pane with task %s: %v\n", taskID, tagErr)
		}
	}

	if _, err := tmux.ReTile(tuiPane, t.TUIWidth, t.MinPaneWidth); err != nil {
		// Rejection: clean up the pane we just created so the layout
		// returns to its prior shape.
		_ = killPane(newPane)
		return fmt.Errorf("tmux: re-tile rejected new pane: %w", err)
	}
	return nil
}

// killPane closes a pane by ID. Best-effort — errors are returned but the
// caller decides whether to surface them; in the rejection-cleanup path we
// already have a more interesting error to propagate.
func killPane(paneID string) error {
	if paneID == "" {
		return nil
	}
	if err := exec.Command("tmux", "kill-pane", "-t", paneID).Run(); err != nil {
		return fmt.Errorf("tmux kill-pane %s: %w", paneID, err)
	}
	return nil
}

func runConfigured(term config.Terminal, vars map[string]string) error {
	binPath, err := exec.LookPath(term.Command)
	if err != nil {
		return fmt.Errorf("terminal %q not found on PATH: %w", term.Command, err)
	}
	args := config.ExpandAll(term.Args, vars)
	cmd := exec.Command(binPath, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("spawning %s: %w", term.Command, err)
	}
	return nil
}

func runAutoDetect(workdir, execCmd string) error {
	for _, t := range terminals {
		binPath, err := exec.LookPath(t.bin)
		if err != nil {
			continue
		}
		cmd := exec.Command(binPath, t.args(workdir, execCmd)...)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		if err := cmd.Start(); err != nil {
			return fmt.Errorf("spawning %s: %w", t.bin, err)
		}
		return nil
	}
	return fmt.Errorf("no supported terminal emulator found (tried: ptyxis, gnome-terminal, x-terminal-emulator); set terminal.command in config to use another")
}
