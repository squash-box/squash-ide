package spawner

import (
	"fmt"
	"os/exec"

	"github.com/squashbox/squash-ide/internal/config"
	runexec "github.com/squashbox/squash-ide/internal/exec"
	"github.com/squashbox/squash-ide/internal/tmux"
)

// runner is the process runner used by the configured / auto-detect terminal
// paths. The tmux split-pane path stays on tmux.* helpers which have their
// own seam. Swap in tests via SetRunner.
var runner runexec.Runner = runexec.Default

// SetRunner swaps the runner; returns the previous value for restoration.
func SetRunner(r runexec.Runner) runexec.Runner {
	prev := runner
	runner = r
	return prev
}

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
// TaskBorderFormat returns the tmux pane-border-format string for a task pane
// with the default "working" state.
func TaskBorderFormat(taskID, title, project string) string {
	return TaskBorderFormatWithState(taskID, title, project, "working")
}

// TaskBorderFormatWithState returns the tmux pane-border-format string for a
// task pane with the badge color matching the given state.
func TaskBorderFormatWithState(taskID, title, project, state string) string {
	if len(title) > 30 {
		title = title[:27] + "..."
	}

	var badge, bg string
	switch state {
	case "idle":
		badge = "○ IDLE"
		bg = "colour214"
	case "input_required":
		badge = "⚠ INPUT REQUIRED"
		bg = "colour204"
	case "testing":
		badge = "⧖ TESTING"
		bg = "colour39"
	default: // working
		badge = "● WORKING"
		bg = "colour78"
	}

	return fmt.Sprintf(
		" #[bg=%s,fg=colour235,bold] %s #[default]  #[bold]%s#[default]  %s#[align=right,fg=colour243]%s ",
		bg, badge, taskID, title, project,
	)
}

func SpawnWith(cfg config.Config, vars map[string]string) error {
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

	// Kill placeholder if present — first task replaces it.
	if phPane, err := tmux.FindPaneByRole(tuiPane, tmux.RolePlaceholder); err == nil && phPane != "" {
		_ = tmux.KillPane(phPane)
	}

	// Split target: rightmost non-TUI pane, or TUI itself.
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

	// Tag pane with task metadata (drives pane-border-format).
	if taskID != "" {
		_ = tmux.SetPaneTask(newPane, taskID)
		_ = tmux.SetPaneOption(newPane, "@squash-title", title)
		_ = tmux.SetPaneOption(newPane, "@squash-project", project)
		_ = tmux.SetPaneBorderFormat(newPane, TaskBorderFormatWithState(taskID, title, project, "working"))
	}

	// Pin TUI + distribute remaining space equally among task panes.
	if _, err := tmux.ReTile(tuiPane, t.TUIWidth, t.PaneWidth, t.MinPaneWidth); err != nil {
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
	binPath, err := runner.LookPath(term.Command)
	if err != nil {
		return fmt.Errorf("terminal %q not found on PATH: %w", term.Command, err)
	}
	args := config.ExpandAll(term.Args, vars)
	if err := runner.Start(binPath, args, true); err != nil {
		return fmt.Errorf("spawning %s: %w", term.Command, err)
	}
	return nil
}

func runAutoDetect(workdir, execCmd string) error {
	for _, t := range terminals {
		binPath, err := runner.LookPath(t.bin)
		if err != nil {
			continue
		}
		if err := runner.Start(binPath, t.args(workdir, execCmd), true); err != nil {
			return fmt.Errorf("spawning %s: %w", t.bin, err)
		}
		return nil
	}
	return fmt.Errorf("no supported terminal emulator found (tried: ptyxis, gnome-terminal, x-terminal-emulator); set terminal.command in config to use another")
}
