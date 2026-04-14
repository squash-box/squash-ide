package main

import (
	"encoding/json"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"
	"github.com/squashbox/squash-ide/internal/slug"
	"github.com/squashbox/squash-ide/internal/spawner"
	"github.com/squashbox/squash-ide/internal/task"
	"github.com/squashbox/squash-ide/internal/taskops"
	"github.com/squashbox/squash-ide/internal/ui"
	"github.com/squashbox/squash-ide/internal/vault"
	"github.com/squashbox/squash-ide/internal/worktree"
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
	vaultRoot := vault.ExpandHome(vaultPath)

	// 1. Load tasks and find the target
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

	// 2. Validate status
	if target.Status != "backlog" {
		return fmt.Errorf("task %s has status %q — only backlog tasks can be spawned", taskID, target.Status)
	}

	// 3. Resolve repo path
	repoPath := target.Repo
	if repoPath == "" {
		resolved, err := vault.ReadEntityRepo(vaultPath, target.Project)
		if err != nil {
			return fmt.Errorf("resolving repo for project %s: %w", target.Project, err)
		}
		repoPath = resolved
	} else {
		repoPath = vault.ExpandHome(repoPath)
	}

	// 4. Derive slug and branch
	taskSlug := slug.FromTitle(target.Title)
	branch := fmt.Sprintf("feat/%s-%s", target.ID, taskSlug)

	if dryRun {
		fmt.Printf("=== DRY RUN for %s ===\n", taskID)
		fmt.Printf("Task:      %s — %s\n", target.ID, target.Title)
		fmt.Printf("Repo:      %s\n", repoPath)
		fmt.Printf("Branch:    %s\n", branch)
		fmt.Printf("Actions:\n")
		fmt.Printf("  1. git fetch origin\n")
		fmt.Printf("  2. git worktree add → branch %s\n", branch)
		fmt.Printf("  3. Move task %s → tasks/active/\n", taskID)
		fmt.Printf("  4. Update tasks/board.md\n")
		fmt.Printf("  5. Append to wiki/log.md\n")
		fmt.Printf("  6. Spawn: gnome-terminal → claude '/implement %s'\n", taskID)
		return nil
	}

	// 5. Create worktree
	fmt.Printf("Creating worktree for %s...\n", taskID)
	worktreePath, err := worktree.Create(repoPath, branch)
	if err != nil {
		return fmt.Errorf("creating worktree: %w", err)
	}
	fmt.Printf("  Worktree: %s\n", worktreePath)

	// 6. Move task to active
	fmt.Printf("Moving %s to active...\n", taskID)
	if _, err := taskops.MoveToActive(vaultRoot, *target); err != nil {
		return fmt.Errorf("moving task to active: %w", err)
	}

	// 7. Update board
	fmt.Printf("Updating board.md...\n")
	if err := taskops.UpdateBoard(vaultRoot, *target); err != nil {
		return fmt.Errorf("updating board: %w", err)
	}

	// 8. Append log
	fmt.Printf("Appending to log.md...\n")
	if err := taskops.AppendLog(vaultRoot, *target, branch, worktreePath); err != nil {
		return fmt.Errorf("appending to log: %w", err)
	}

	// 9. Spawn terminal
	fmt.Printf("Spawning terminal...\n")
	if err := spawner.Spawn(worktreePath, taskID); err != nil {
		return fmt.Errorf("spawning terminal: %w", err)
	}

	fmt.Printf("\nDone! Task %s is now active.\n", taskID)
	fmt.Printf("  Branch:   %s\n", branch)
	fmt.Printf("  Worktree: %s\n", worktreePath)
	return nil
}
