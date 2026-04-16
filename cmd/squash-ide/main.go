package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"
	"github.com/squashbox/squash-ide/internal/config"
	"github.com/squashbox/squash-ide/internal/dispatch"
	"github.com/squashbox/squash-ide/internal/task"
	"github.com/squashbox/squash-ide/internal/tmux"
	"github.com/squashbox/squash-ide/internal/ui"
	"github.com/squashbox/squash-ide/internal/vault"
)

// version is set via -ldflags "-X main.version=vX.Y.Z" at build time.
// The default is "dev" for local builds; the Makefile sets the real tag
// when producing release artifacts.
var version = "dev"

// Flag values, populated by cobra before RunE runs. Empty string = not set.
var (
	flagVault        string
	flagTerminal     string
	flagSpawnCmd     string
	flagNoTmux       bool
	flagInTmux       bool // internal: marks "we already wrapped ourselves in tmux"
	flagTUIWidth     int
	flagMinPaneWidth int
)

func main() {
	rootCmd := &cobra.Command{
		Use:     "squash-ide",
		Short:   "Terminal task dispatcher for vault-based workflows",
		Version: version,
		RunE:    runTUI,
	}
	rootCmd.SetVersionTemplate("squash-ide {{.Version}}\n")

	rootCmd.PersistentFlags().StringVar(&flagVault, "vault", "", "path to the Obsidian vault (overrides config file and env)")
	rootCmd.PersistentFlags().StringVar(&flagTerminal, "terminal", "", "terminal emulator command (overrides config file and env)")
	rootCmd.PersistentFlags().StringVar(&flagSpawnCmd, "spawn-cmd", "", "command to run inside spawned terminal (overrides config file and env)")
	rootCmd.PersistentFlags().BoolVar(&flagNoTmux, "no-tmux", false, "disable tmux tiled-pane mode; spawn each task in its own OS terminal window")
	rootCmd.PersistentFlags().BoolVar(&flagInTmux, "in-tmux", false, "internal: indicates the process is running inside its own bootstrapped tmux session")
	_ = rootCmd.PersistentFlags().MarkHidden("in-tmux")
	rootCmd.PersistentFlags().IntVar(&flagTUIWidth, "tui-width", 0, "fixed width (cols) for the TUI pane in tmux mode (default 60)")
	rootCmd.PersistentFlags().IntVar(&flagMinPaneWidth, "min-pane-width", 0, "minimum width (cols) per spawned tmux pane; spawn is rejected if violated (default 80)")

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

	completeCmd := &cobra.Command{
		Use:   "complete <task-id>",
		Short: "Archive an active task, remove its worktree, update board/log",
		Args:  cobra.ExactArgs(1),
		RunE:  runComplete,
	}

	blockCmd := &cobra.Command{
		Use:   "block <task-id>",
		Short: "Move an active task to blocked with a one-line reason",
		Args:  cobra.ExactArgs(1),
		RunE:  runBlock,
	}
	blockCmd.Flags().String("reason", "", "one-line reason for blocking (required)")

	configCmd := &cobra.Command{
		Use:   "config",
		Short: "Print the resolved config with source annotations",
		RunE:  runConfig,
	}

	placeholderCmd := &cobra.Command{
		Use:    "placeholder",
		Short:  "Internal: render the right-hand placeholder pane in tmux mode",
		Hidden: true,
		RunE:   runPlaceholder,
	}

	rootCmd.AddCommand(listCmd, spawnCmd, completeCmd, blockCmd, configCmd, placeholderCmd)

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

// loadConfig resolves the config, applying CLI flags on top of env and file.
func loadConfig() (config.Config, error) {
	return config.Load(config.Overrides{
		Vault:        flagVault,
		Terminal:     flagTerminal,
		SpawnCmd:     flagSpawnCmd,
		NoTmux:       flagNoTmux,
		TUIWidth:     flagTUIWidth,
		MinPaneWidth: flagMinPaneWidth,
	})
}

func runTUI(cmd *cobra.Command, args []string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	// T-011: if tmux mode is enabled and we're not already inside tmux,
	// re-exec ourselves inside a tmux session. tmux.EnsureSession replaces
	// the current process via syscall.Exec on success — it only returns on
	// error. We pass --in-tmux explicitly so the inner process is an
	// observable signal that we wrapped it (and as a belt-and-suspenders
	// guard against any future bug that misreads $TMUX).
	if cfg.Tmux.Enabled && !flagInTmux && !tmux.InSession() {
		if !tmux.Available() {
			fmt.Fprintln(os.Stderr, "warning: tmux not on PATH; falling back to OS-window spawn (use --no-tmux to silence)")
		} else {
			inner := buildSelfInvocation()
			placeholder := buildPlaceholderInvocation()
			// Replaces this process on success. Only returns on exec
			// failure (or bootstrap setup failure before the exec).
			if err := tmux.EnsureSessionWithPlaceholder(
				cfg.Tmux.SessionName, inner, placeholder, cfg.Tmux.TUIWidth,
			); err != nil {
				return fmt.Errorf("bootstrapping tmux session: %w", err)
			}
		}
	}

	m := ui.New(cfg)
	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err = p.Run()

	// When the TUI exits cleanly inside a session we bootstrapped, tear the
	// whole session down — otherwise the placeholder (or any lingering task
	// panes) keeps the tmux session alive and the user's `q` looks like a
	// no-op. We identify "our" session by name match so that running
	// squash-ide inside the user's own tmux session doesn't nuke their work.
	if cfg.Tmux.Enabled && tmux.InSession() {
		if tmux.CurrentSessionName() == cfg.Tmux.SessionName {
			_ = tmux.KillSession(cfg.Tmux.SessionName)
		}
	}

	return err
}

// buildSelfInvocation reconstructs the command line that the tmux pane should
// run to launch us inside the new session. We re-pass every flag the user gave
// us (so --vault etc. survive the re-exec) and add --in-tmux as the marker.
func buildSelfInvocation() string {
	parts := []string{shellQuote(os.Args[0])}
	if flagVault != "" {
		parts = append(parts, "--vault", shellQuote(flagVault))
	}
	if flagTerminal != "" {
		parts = append(parts, "--terminal", shellQuote(flagTerminal))
	}
	if flagSpawnCmd != "" {
		parts = append(parts, "--spawn-cmd", shellQuote(flagSpawnCmd))
	}
	if flagTUIWidth > 0 {
		parts = append(parts, fmt.Sprintf("--tui-width=%d", flagTUIWidth))
	}
	if flagMinPaneWidth > 0 {
		parts = append(parts, fmt.Sprintf("--min-pane-width=%d", flagMinPaneWidth))
	}
	parts = append(parts, "--in-tmux")
	return strings.Join(parts, " ")
}

// buildPlaceholderInvocation reconstructs the command line that the right
// tmux pane should run to render the placeholder. We forward the same
// layout-relevant flags (TUI width, min pane width, vault — the placeholder
// subcommand loads config and respects them) plus the --in-tmux marker.
func buildPlaceholderInvocation() string {
	parts := []string{shellQuote(os.Args[0]), "placeholder"}
	if flagVault != "" {
		parts = append(parts, "--vault", shellQuote(flagVault))
	}
	if flagTUIWidth > 0 {
		parts = append(parts, fmt.Sprintf("--tui-width=%d", flagTUIWidth))
	}
	if flagMinPaneWidth > 0 {
		parts = append(parts, fmt.Sprintf("--min-pane-width=%d", flagMinPaneWidth))
	}
	parts = append(parts, "--in-tmux")
	return strings.Join(parts, " ")
}

// shellQuote wraps s in single quotes if it contains shell-meaningful chars.
// Mirrors config.shellQuote (kept private there); this is the bare minimum
// needed for argv reconstruction.
func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	safe := true
	for _, r := range s {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '/' || r == '_' || r == '-' || r == '.' || r == '=') {
			safe = false
			break
		}
	}
	if safe {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func runList(cmd *cobra.Command, args []string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	tasks, err := vault.ReadAll(cfg.Vault)
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
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	if err := config.Validate(cfg); err != nil {
		return err
	}

	taskID := args[0]
	dryRun, _ := cmd.Flags().GetBool("dry-run")

	// Load tasks and find the target
	tasks, err := vault.ReadAll(cfg.Vault)
	if err != nil {
		return fmt.Errorf("reading vault: %w", err)
	}

	target := findTask(tasks, taskID)
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
	res, err := dispatch.Run(cfg, *target)
	if err != nil {
		return err
	}

	fmt.Printf("\nDone! Task %s is now active.\n", taskID)
	fmt.Printf("  Branch:   %s\n", res.Branch)
	fmt.Printf("  Worktree: %s\n", res.WorktreePath)
	return nil
}

func runComplete(cmd *cobra.Command, args []string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	taskID := args[0]
	tasks, err := vault.ReadAll(cfg.Vault)
	if err != nil {
		return fmt.Errorf("reading vault: %w", err)
	}
	target := findTask(tasks, taskID)
	if target == nil {
		return fmt.Errorf("task %s not found in vault", taskID)
	}

	fmt.Printf("Completing %s...\n", taskID)
	if err := dispatch.Complete(cfg, *target); err != nil {
		return err
	}
	fmt.Printf("Done. %s archived; worktree removed.\n", taskID)
	return nil
}

func runBlock(cmd *cobra.Command, args []string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	taskID := args[0]
	reason, _ := cmd.Flags().GetString("reason")
	if reason == "" {
		return fmt.Errorf("--reason is required")
	}

	tasks, err := vault.ReadAll(cfg.Vault)
	if err != nil {
		return fmt.Errorf("reading vault: %w", err)
	}
	target := findTask(tasks, taskID)
	if target == nil {
		return fmt.Errorf("task %s not found in vault", taskID)
	}

	fmt.Printf("Blocking %s...\n", taskID)
	if err := dispatch.Block(cfg, *target, reason); err != nil {
		return err
	}
	fmt.Printf("Done. %s moved to blocked/.\n", taskID)
	return nil
}

func runConfig(cmd *cobra.Command, args []string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	fmt.Print(cfg.Format())
	return nil
}

// runPlaceholder renders the right-pane placeholder screen. It's invoked
// by the tmux bootstrap in fresh sessions — see tmux.EnsureSession. Uses
// the resolved tmux config so the slot count matches what the spawner
// will actually allow.
func runPlaceholder(cmd *cobra.Command, args []string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	return ui.RunPlaceholder(cfg.Tmux.TUIWidth, cfg.Tmux.MinPaneWidth)
}

// findTask returns the first task with a matching ID, or nil.
func findTask(tasks []task.Task, id string) *task.Task {
	for i := range tasks {
		if tasks[i].ID == id {
			return &tasks[i]
		}
	}
	return nil
}
