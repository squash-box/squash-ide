package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/squashbox/squash-ide/internal/status"
)

// validStates enumerates the state values the TUI knows how to render. Keep
// aligned with the enum in cmd/squash-ide-mcp/main.go and the switch in
// internal/ui/render.go — any drift breaks the badge.
var validStates = []string{"idle", "working", "input_required", "testing"}

// newStatusCmd returns the `status` subcommand. Factored out so tests can
// exercise it without touching the rest of the cobra tree.
func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status <state> [message]",
		Short: "Report task status from a shell hook (Claude Code Notification/PostToolUse/Stop)",
		Long: `Report a task status from a shell hook. Intended to be invoked by Claude
Code's hooks (Notification, PostToolUse, Stop) so tool-permission dialogs
that pause the model mid-turn flip the TUI badge without needing an MCP
tool call.

Reads SQUASH_TASK_ID from the environment — dispatch bakes it into the
generated .claude/settings.json hook commands.`,
		Args: cobra.RangeArgs(1, 2),
		RunE: runStatus,
	}
}

func runStatus(cmd *cobra.Command, args []string) error {
	taskID := os.Getenv("SQUASH_TASK_ID")
	if taskID == "" {
		return fmt.Errorf("SQUASH_TASK_ID not set — hook must run inside a dispatched task worktree")
	}

	state := args[0]
	if !isValidState(state) {
		return fmt.Errorf("invalid state %q (allowed: %s)", state, strings.Join(validStates, ", "))
	}

	message := ""
	if len(args) == 2 {
		message = args[1]
	}

	if err := status.Write(taskID, state, message); err != nil {
		return fmt.Errorf("writing status for %s: %w", taskID, err)
	}

	fmt.Fprintf(os.Stderr, "squash-ide: %s → %s: %s\n", taskID, state, message)

	if state == "input_required" {
		status.NotifyInputRequired(taskID, message)
	}
	return nil
}

func isValidState(s string) bool {
	for _, v := range validStates {
		if v == s {
			return true
		}
	}
	return false
}
