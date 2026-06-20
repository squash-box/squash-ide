package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/squashbox/squash-ide/internal/config"
	"github.com/squashbox/squash-ide/internal/status"
	"github.com/squashbox/squash-ide/internal/tmux"
)

// newNotifyWatchCmd returns the hidden notify-watch subcommand. It is
// spawned detached by status.NotifyInputRequired and is not intended for
// direct invocation; the CLI surface stays narrow.
func newNotifyWatchCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "notify-watch <task-id> [message]",
		Short: "Internal: hold notify-send open and focus the task pane on click",
		Long: `Internal subcommand spawned detached by status.NotifyInputRequired.
Holds notify-send open with --wait and a "Focus" default action; on click,
brings the squash-ide tmux session forward and selects the pane bound to
the task. Not part of the user-facing CLI surface.`,
		Hidden: true,
		Args:   cobra.RangeArgs(1, 2),
		RunE:   runNotifyWatch,
	}
}

func runNotifyWatch(cmd *cobra.Command, args []string) error {
	taskID := args[0]
	message := ""
	if len(args) == 2 {
		message = args[1]
	}

	res := status.NotifyAndWait(cmd.Context(), taskID, message)
	if !res.Clicked {
		return nil
	}

	cfg, err := loadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "squash-ide notify-watch: config load failed: %v\n", err)
		return nil
	}
	focusTaskPane(cfg, taskID)
	return nil
}

// focusTaskPane focuses the pane bound to taskID, by whichever engine is
// configured. Best-effort throughout: a failure never escalates beyond a stderr
// note.
//
//   - Native: the pane lives inside the running TUI process, which this separate
//     notify-watch process can't reach in-memory, so it drops a focus-request
//     marker the TUI consumes on its next status poll ([[T-039]]). If no TUI is
//     running the marker is simply never consumed (and ages out), so a click
//     degrades gracefully — the native analogue of the `--no-tmux` silent click.
//   - tmux: bring the session forward and select-pane on the @squash-task tag
//     ([[T-034]]). Skipped when tmux is disabled (`--no-tmux`) so that path stays
//     silent on click as documented.
func focusTaskPane(cfg config.Config, taskID string) {
	if cfg.Engine == config.EngineNative {
		if err := status.RequestFocus(taskID); err != nil {
			fmt.Fprintf(os.Stderr, "squash-ide notify-watch: focus request %s: %v\n", taskID, err)
		}
		return
	}
	if !cfg.Tmux.Enabled {
		return
	}
	session := cfg.Tmux.SessionName
	if session == "" {
		return
	}
	if _, err := tmux.SwitchClient(session); err != nil {
		fmt.Fprintf(os.Stderr, "squash-ide notify-watch: switch-client %s: %v\n", session, err)
	}
	paneID, err := tmux.FindPaneByTask(session+":", taskID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "squash-ide notify-watch: find-pane %s: %v\n", taskID, err)
		return
	}
	if paneID == "" {
		fmt.Fprintf(os.Stderr, "squash-ide notify-watch: no pane tagged for %s\n", taskID)
		return
	}
	if _, err := tmux.SelectPane(paneID); err != nil {
		fmt.Fprintf(os.Stderr, "squash-ide notify-watch: select-pane %s: %v\n", paneID, err)
	}
}
