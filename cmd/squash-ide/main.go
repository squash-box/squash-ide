package main

import (
	"encoding/json"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"
	"github.com/squashbox/squash-ide/internal/dispatch"
	"github.com/squashbox/squash-ide/internal/task"
	"github.com/squashbox/squash-ide/internal/ui"
	"github.com/squashbox/squash-ide/internal/vault"
)

var vaultPath string

func main() {
	rootCmd := &cobra.Command{
		Use:   "squash-ide",
		Short: "Terminal task dispatcher for vault-based workflows",
		RunE:  runTUI,
	}

	rootCmd.PersistentFlags().StringVar(&vaultPath, "vault", "~/GIT/agentic/tasks/personal/", "path to the Obsidian vault")

	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List tasks from the vault as JSON",
		RunE:  runList,
	}
	listCmd.Flags().String("status", "", "filter tasks by status (backlog, active, blocked, archive)")

	spawnCmd := &cobra.Command{
		Use:   "spawn <task-id>",
		Short: "Create worktree and spawn terminal for a task",
		Args:  cobra.ExactArgs(1),
		RunE:  runSpawn,
	}
	spawnCmd.Flags().Bool("dry-run", false, "print intended actions without executing")

	rootCmd.AddCommand(listCmd, spawnCmd)

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func runTUI(cmd *cobra.Command, args []string) error {
	m := ui.New(vaultPath)
	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err := p.Run()
	return err
}

func runList(cmd *cobra.Command, args []string) error {
	tasks, err := vault.ReadAll(vaultPath)
	if err != nil {
		return fmt.Errorf("reading vault: %w", err)
	}

	statusFilter, _ := cmd.Flags().GetString("status")
	if statusFilter != "" {
		var filtered []task.Task
		for _, t := range tasks {
			if t.Status == statusFilter {
				filtered = append(filtered, t)
			}
		}
		tasks = filtered
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(tasks)
}

func runSpawn(cmd *cobra.Command, args []string) error {
	taskID := args[0]
	dryRun, _ := cmd.Flags().GetBool("dry-run")

	// Load tasks and find the target
	tasks, err := vault.ReadAll(vaultPath)
	if err != nil {
		return fmt.Errorf("reading vault: %w", err)
	}

	var target *task.Task
	for i := range tasks {
		if tasks[i].ID == taskID {
			target = &tasks[i]
			break
		}
	}
	if target == nil {
		return fmt.Errorf("task %s not found in vault", taskID)
	}

	if dryRun {
		fmt.Printf("=== DRY RUN for %s ===\n", taskID)
		fmt.Printf("Task:      %s — %s\n", target.ID, target.Title)
		fmt.Printf("Status:    %s\n", target.Status)
		fmt.Printf("Actions:   worktree → move-to-active → board → log → terminal\n")
		return nil
	}

	fmt.Printf("Dispatching %s...\n", taskID)
	res, err := dispatch.Run(vaultPath, *target)
	if err != nil {
		return err
	}

	fmt.Printf("\nDone! Task %s is now active.\n", taskID)
	fmt.Printf("  Branch:   %s\n", res.Branch)
	fmt.Printf("  Worktree: %s\n", res.WorktreePath)
	return nil
}
