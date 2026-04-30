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

// focusTaskPane brings the configured tmux session forward and selects the
// pane tagged with the given taskID. Best-effort throughout: each tmux
// step's error is swallowed-and-logged so a transient tmux glitch never
// escalates beyond a stderr note. Skipped entirely when tmux is disabled
// (`--no-tmux`) so that path stays silent on click as documented.
func focusTaskPane(cfg config.Config, taskID string) {
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
