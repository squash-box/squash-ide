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
		return runTmux(cfg.Tmux, vars["cwd"], execCmd, vars["task_id"], vars["title"], vars["project"])
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
func runTmux(t config.Tmux, cwd, execCmd, taskID, title, project string) error {
	tuiPane := tmux.CurrentPaneID()
	if tuiPane == "" {
		return fmt.Errorf("tmux: $TMUX_PANE not set — cannot determine TUI pane")
	}

	// If the right-hand placeholder pane is still up, kill it before
	// splitting — the first task should replace the placeholder, not sit
	// next to it.
	if phPane, err := tmux.FindPaneByRole(tuiPane, tmux.RolePlaceholder); err == nil && phPane != "" {
		if killErr := tmux.KillPane(phPane); killErr != nil {
			fmt.Fprintf(os.Stderr, "warning: could not close placeholder pane: %v\n", killErr)
		}
	}

	// Remove all header panes before the horizontal split. Headers are
	// vertical sub-splits inside each column — if we SplitRight on a
	// content pane that has a header above it, tmux creates the new column
	// INSIDE the existing column rather than beside it. Removing headers
	// first restores each column to a single full-height pane so the
	// horizontal split works correctly. We re-add all headers after ReTile.
	tmux.KillAllByOption(tuiPane, "@squash-header")

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

	// Tag the new content pane with task metadata. Title is stored so
	// headers can be reconstructed after future splits.
	if taskID != "" {
		_ = tmux.SetPaneTask(newPane, taskID)
		_ = tmux.SetPaneOption(newPane, "@squash-title", title)
		_ = tmux.SetPaneOption(newPane, "@squash-project", project)
	}

	if _, err := tmux.ReTile(tuiPane, t.TUIWidth, t.MinPaneWidth); err != nil {
		_ = killPane(newPane)
		return fmt.Errorf("tmux: re-tile rejected new pane: %w", err)
	}

	// Re-add 1-row header panes above every content pane. This runs AFTER
	// ReTile so the columns are at their final widths before the vertical
	// sub-splits happen.
	addHeadersToAllTaskPanes(tuiPane)

	return nil
}

// addHeadersToAllTaskPanes finds every pane tagged with @squash-task and
// creates a 1-row header pane above it. Each content pane must also have
// @squash-title and @squash-project set.
func addHeadersToAllTaskPanes(tuiPane string) {
	panes, err := tmux.ListWindowPanes(tuiPane)
	if err != nil {
		return
	}
	for _, p := range panes {
		if p.ID == tuiPane {
			continue
		}
		taskID, _ := tmux.GetPaneOption(p.ID, "@squash-task")
		if taskID == "" {
			continue
		}
		title, _ := tmux.GetPaneOption(p.ID, "@squash-title")
		project, _ := tmux.GetPaneOption(p.ID, "@squash-project")

		headerCmd := buildHeaderCmd(taskID, title, project)
		headerPane, err := tmux.SplitTop(p.ID, 1, headerCmd)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: creating header for %s: %v\n", taskID, err)
			continue
		}
		_ = tmux.SetPaneOption(headerPane, "@squash-header", taskID)
	}
	// Ensure the TUI pane keeps focus after all the splits.
	_, _ = tmux.SelectPane(tuiPane)
}

// buildHeaderCmd builds the shell command for the 1-line header pane.
//
// Layout: ` [● WORKING]  #ID  Title            project `
//
// The badge uses the TUI colour palette (green bg 78, dark fg 235, bold).
// The project is right-aligned via ANSI cursor movement: jump to right
// edge (\033[999C), back up by project length (\033[<n>D), then print.
func buildHeaderCmd(taskID, title, project string) string {
	// Truncate title so it doesn't collide with right-aligned project.
	if len(title) > 30 {
		title = title[:27] + "..."
	}
	// Right-align: move cursor to far-right then back up by project width + 1 (padding).
	rLen := len(project) + 1
	return fmt.Sprintf(
		`printf '\033[48;5;78;38;5;235;1m ● WORKING \033[0m  \033[1m%s\033[0m  \033[38;5;243m%s\033[0m\033[999C\033[%dD\033[38;5;243m%s \033[0m' && exec sleep infinity`,
		taskID, title, rLen, project,
	)
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
