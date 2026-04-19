package dispatch

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/squashbox/squash-ide/internal/config"
	"github.com/squashbox/squash-ide/internal/ghx"
	"github.com/squashbox/squash-ide/internal/slug"
	"github.com/squashbox/squash-ide/internal/spawner"
	"github.com/squashbox/squash-ide/internal/status"
	"github.com/squashbox/squash-ide/internal/task"
	"github.com/squashbox/squash-ide/internal/taskops"
	"github.com/squashbox/squash-ide/internal/tmux"
	"github.com/squashbox/squash-ide/internal/vault"
	"github.com/squashbox/squash-ide/internal/worktree"
)

// Result holds the output of a successful dispatch.
type Result struct {
	Branch       string
	WorktreePath string
}

// Run executes the full spawn workflow for a task: create worktree, move task
// to active, update board, append log, and spawn a terminal using the
// configured terminal + spawn command (with templating).
func Run(cfg config.Config, t task.Task) (Result, error) {
	if t.Status != "backlog" {
		return Result{}, fmt.Errorf("task %s has status %q — only backlog tasks can be spawned", t.ID, t.Status)
	}

	vaultRoot := vault.ExpandHome(cfg.Vault)

	repoPath, err := resolveRepo(cfg, t)
	if err != nil {
		return Result{}, err
	}

	branch := BranchFor(t)

	// Pre-flight: verify the window has room for another pane before
	// touching the vault or creating a worktree. Avoids orphaned active
	// tasks when the terminal is too narrow.
	if cfg.Tmux.Enabled && tmux.InSession() {
		tuiPane := tmux.CurrentPaneID()
		totalCols, err := tmux.WindowWidth(tuiPane)
		if err == nil {
			// Count existing task panes (not placeholder/TUI) + 1 for the new one.
			existing, _ := tmux.CountPanesByOption(tuiPane, "@squash-task")
			n := existing + 1
			floor := cfg.Tmux.MinPaneWidth
			if cfg.Tmux.PaneWidth > floor {
				floor = cfg.Tmux.PaneWidth
			}
			avail := totalCols - cfg.Tmux.TUIWidth - n
			if avail < n*floor {
				return Result{}, fmt.Errorf(
					"terminal too narrow for another pane (%d cols, need %d)",
					totalCols, cfg.Tmux.TUIWidth+n*(floor+1))
			}
		}
	}

	// Create worktree. If the path already exists we translate the typed
	// errors from worktree.Create into operator-facing hints naming the
	// exact subcommand to run next. %w is preserved so callers can still
	// errors.Is / errors.As against the sentinels.
	worktreePath, err := worktree.Create(repoPath, branch)
	if err != nil {
		switch {
		case errors.Is(err, worktree.ErrWorktreeOrphan):
			return Result{}, fmt.Errorf(
				"creating worktree: %w — run `squash-ide worktree adopt %s` "+
					"to re-register it, or `squash-ide worktree clean %s` to discard",
				err, t.ID, t.ID)
		case errors.Is(err, worktree.ErrWorktreeNotAGitDir):
			return Result{}, fmt.Errorf(
				"creating worktree: %w — inspect the directory manually, "+
					"then `squash-ide worktree clean %s` to remove it",
				err, t.ID)
		case errors.As(err, new(*worktree.ErrWorktreeBranchMismatch)):
			return Result{}, fmt.Errorf(
				"creating worktree: %w — run `squash-ide worktree clean %s` "+
					"to discard the existing worktree before re-spawning",
				err, t.ID)
		}
		return Result{}, fmt.Errorf("creating worktree: %w", err)
	}

	// Write MCP config so the spawned Claude session can report status.
	if err := writeMCPConfig(worktreePath, t.ID); err != nil {
		return Result{}, fmt.Errorf("writing MCP config: %w", err)
	}

	// Write Claude Code hook settings so permission dialogs flip the badge
	// to input_required synchronously — the model-initiated path can't
	// reach that state because the model turn is paused on the host.
	selfPath, err := os.Executable()
	if err != nil {
		return Result{}, fmt.Errorf("resolving executable path: %w", err)
	}
	if err := writeClaudeSettings(worktreePath, t.ID, selfPath); err != nil {
		return Result{}, fmt.Errorf("writing Claude settings: %w", err)
	}

	// Move task to active
	if _, err := taskops.MoveToActive(vaultRoot, t); err != nil {
		return Result{}, fmt.Errorf("moving task to active: %w", err)
	}

	// Update board
	if err := taskops.UpdateBoard(vaultRoot, t); err != nil {
		return Result{}, fmt.Errorf("updating board: %w", err)
	}

	// Append log
	if err := taskops.AppendLog(vaultRoot, t, branch, worktreePath); err != nil {
		return Result{}, fmt.Errorf("appending to log: %w", err)
	}

	// Spawn terminal
	vars := map[string]string{
		"cwd":      worktreePath,
		"task_id":  t.ID,
		"title":    t.Title,
		"project":  t.Project,
		"worktree": worktreePath,
		"repo":     repoPath,
		"branch":   branch,
	}
	if err := spawner.SpawnWith(cfg, vars); err != nil {
		return Result{}, fmt.Errorf("spawning terminal: %w", err)
	}

	return Result{Branch: branch, WorktreePath: worktreePath}, nil
}

// Complete archives an active task: captures the PR URL via gh, removes the
// worktree and tmux pane, moves the task file from active/ to archive/, and
// updates the board and log. The branch is derived from the task title
// (same rule used by Run).
//
// As of [[T-029]], squash-ide owns task lifecycle end-to-end — `/implement`
// no longer mutates `tasks/active/`, `tasks/archive/`, `tasks/board.md`, or
// `wiki/log.md`. Pressing `c` in the TUI (or running `squash-ide complete`)
// is the only path that completes a task.
func Complete(cfg config.Config, t task.Task) error {
	return CompleteWithPR(cfg, t, "")
}

// CompleteWithPR is Complete with an optional PR URL override. Empty
// `prOverride` falls back to auto-detection via `gh pr list`. The override
// is the CLI escape hatch for environments without `gh` or for the rollout
// case where the PR was created out-of-band.
//
// Two status arms:
//   - active: normal flow — capture URL, archive, update board, log.
//   - done + file in tasks/archive/: rollout-recovery — finish the worktree
//     and pane teardown that legacy `/implement` runs left dangling, log a
//     `complete-after` entry, but do NOT re-archive the task. See [[T-029]]
//     blast-radius notes.
//
// All other statuses are rejected.
func CompleteWithPR(cfg config.Config, t task.Task, prOverride string) error {
	vaultRoot := vault.ExpandHome(cfg.Vault)

	rolloutRecovery := false
	switch t.Status {
	case "active":
		// normal path
	case "done":
		_, dir, err := taskops.FindTaskFile(vaultRoot, t.ID)
		if err != nil || dir != "archive" {
			return fmt.Errorf(
				"task %s has status %q but no file in tasks/archive — refusing "+
					"to complete (frontmatter and file location are out of sync)",
				t.ID, t.Status)
		}
		rolloutRecovery = true
	default:
		return fmt.Errorf("task %s has status %q — only active tasks can be completed", t.ID, t.Status)
	}

	repoPath, err := resolveRepo(cfg, t)
	if err != nil {
		return err
	}

	branch := BranchFor(t)

	// Resolve the PR URL: explicit override wins; otherwise auto-detect via
	// gh. ErrGHMissing and ErrNoPR are degraded states (warn + continue);
	// any other gh failure (e.g. transient HTTP error) is fatal so the
	// operator can retry rather than silently dropping the URL.
	prURL := strings.TrimSpace(prOverride)
	if prURL == "" {
		url, err := ghx.PRURLForBranch(repoPath, branch)
		switch {
		case err == nil:
			prURL = url
		case errors.Is(err, ghx.ErrGHMissing),
			errors.Is(err, ghx.ErrNoPR),
			errors.Is(err, ghx.ErrNonGitHubRemote):
			fmt.Fprintf(os.Stderr, "warning: no PR URL captured for %s: %v\n", t.ID, err)
		default:
			return fmt.Errorf("capturing PR URL: %w", err)
		}
	}

	// Clean up the MCP status file.
	_ = status.Remove(t.ID)

	// Kill the task's tmux pane if running.
	if cfg.Tmux.Enabled && tmux.InSession() {
		tuiPane := tmux.CurrentPaneID()
		if pane, err := tmux.FindPaneByTask(tuiPane, t.ID); err == nil && pane != "" {
			_ = tmux.KillPane(pane)
		}
		if remaining, _ := tmux.CountPanesByOption(tuiPane, "@squash-task"); remaining == 0 {
			if existing, _ := tmux.FindPaneByRole(tuiPane, tmux.RolePlaceholder); existing == "" {
				_ = tmux.SpawnPlaceholder(tuiPane, cfg.Tmux.TUIWidth)
			}
		} else {
			_, _ = tmux.ReTile(tuiPane, cfg.Tmux.TUIWidth, cfg.Tmux.PaneWidth, cfg.Tmux.MinPaneWidth)
		}
	}

	if err := worktree.Remove(repoPath, branch); err != nil {
		return fmt.Errorf("removing worktree: %w", err)
	}

	if rolloutRecovery {
		// Legacy /implement already moved the file and updated the board /
		// log. Skip those mutations; just record the recovery in the log so
		// the operator can see who finished the cleanup.
		if err := taskops.AppendLogCompleteAfter(vaultRoot, t, branch, prURL); err != nil {
			return fmt.Errorf("appending to log: %w", err)
		}
		return nil
	}

	if _, err := taskops.MoveToArchive(vaultRoot, t, branch, prURL); err != nil {
		return fmt.Errorf("archiving task: %w", err)
	}
	if err := taskops.UpdateBoardComplete(vaultRoot, t); err != nil {
		return fmt.Errorf("updating board: %w", err)
	}
	if err := taskops.AppendLogComplete(vaultRoot, t, branch, prURL); err != nil {
		return fmt.Errorf("appending to log: %w", err)
	}
	return nil
}

// Block moves an active task to the blocked/ directory with a one-line
// reason, updates the board and log. The worktree is left in place so work
// can resume after unblocking.
func Block(cfg config.Config, t task.Task, reason string) error {
	if t.Status != "active" {
		return fmt.Errorf("task %s has status %q — only active tasks can be blocked", t.ID, t.Status)
	}
	if strings.TrimSpace(reason) == "" {
		return fmt.Errorf("block reason required")
	}

	vaultRoot := vault.ExpandHome(cfg.Vault)

	if _, err := taskops.MoveToBlocked(vaultRoot, t, reason); err != nil {
		return fmt.Errorf("moving task to blocked: %w", err)
	}
	if err := taskops.UpdateBoardBlock(vaultRoot, t); err != nil {
		return fmt.Errorf("updating board: %w", err)
	}
	if err := taskops.AppendLogBlock(vaultRoot, t, reason); err != nil {
		return fmt.Errorf("appending to log: %w", err)
	}
	return nil
}

// Deactivate moves an active task back to the backlog: removes the worktree
// and branch, moves the task file from active/ to backlog/, and updates the
// board and log. This is the inverse of Run (spawn).
func Deactivate(cfg config.Config, t task.Task) error {
	if t.Status != "active" {
		return fmt.Errorf("task %s has status %q — only active tasks can be deactivated", t.ID, t.Status)
	}

	vaultRoot := vault.ExpandHome(cfg.Vault)

	repoPath, err := resolveRepo(cfg, t)
	if err != nil {
		return err
	}

	branch := BranchFor(t)

	// Clean up the MCP status file.
	_ = status.Remove(t.ID)

	// Kill the task's tmux pane if running. Best-effort — the task may
	// not have a pane (e.g. spawned in OS-window mode or leftover from
	// a previous session).
	if cfg.Tmux.Enabled && tmux.InSession() {
		tuiPane := tmux.CurrentPaneID()
		if pane, err := tmux.FindPaneByTask(tuiPane, t.ID); err == nil && pane != "" {
			_ = tmux.KillPane(pane)
		}
		// Restore placeholder or redistribute space among remaining panes.
		if remaining, _ := tmux.CountPanesByOption(tuiPane, "@squash-task"); remaining == 0 {
			if existing, _ := tmux.FindPaneByRole(tuiPane, tmux.RolePlaceholder); existing == "" {
				_ = tmux.SpawnPlaceholder(tuiPane, cfg.Tmux.TUIWidth)
			}
		} else {
			_, _ = tmux.ReTile(tuiPane, cfg.Tmux.TUIWidth, cfg.Tmux.PaneWidth, cfg.Tmux.MinPaneWidth)
		}
	}

	if err := worktree.Remove(repoPath, branch); err != nil {
		return fmt.Errorf("removing worktree: %w", err)
	}
	if _, err := taskops.MoveToBacklog(vaultRoot, t); err != nil {
		return fmt.Errorf("moving task to backlog: %w", err)
	}
	if err := taskops.UpdateBoardDeactivate(vaultRoot, t); err != nil {
		return fmt.Errorf("updating board: %w", err)
	}
	if err := taskops.AppendLogDeactivate(vaultRoot, t, branch); err != nil {
		return fmt.Errorf("appending to log: %w", err)
	}
	return nil
}

// BranchFor returns the conventional feature-branch name for a task.
func BranchFor(t task.Task) string {
	return fmt.Sprintf("feat/%s-%s", t.ID, slug.FromTitle(t.Title))
}

// WorktreePathFor resolves the repo for a task and returns the expected
// worktree path (regardless of whether the worktree currently exists).
// Useful for the TUI detail view on active tasks.
func WorktreePathFor(cfg config.Config, t task.Task) (string, error) {
	repoPath, err := resolveRepo(cfg, t)
	if err != nil {
		return "", err
	}
	return worktree.Path(repoPath, BranchFor(t)), nil
}

// writeMCPConfig writes a .mcp.json file into the worktree so the spawned
// Claude Code session discovers the squash-ide MCP server and can report
// status via the squash_status tool.
func writeMCPConfig(worktreePath, taskID string) error {
	selfPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolving executable path: %w", err)
	}
	mcpBin := filepath.Join(filepath.Dir(selfPath), "squash-ide-mcp")

	cfg := map[string]any{
		"mcpServers": map[string]any{
			"squash-ide": map[string]any{
				"type":    "stdio",
				"command": mcpBin,
				"args":    []string{},
				"env": map[string]string{
					"SQUASH_TASK_ID": taskID,
				},
			},
		},
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(worktreePath, ".mcp.json"), append(data, '\n'), 0644)
}

// RepoPathFor resolves the absolute repo path for a task using the same
// rules as the internal dispatch flow (task.repo field, else project's
// entity page). Exposed for CLI subcommands that need the repo path
// without running a full dispatch action.
func RepoPathFor(cfg config.Config, t task.Task) (string, error) {
	return resolveRepo(cfg, t)
}

// resolveRepo returns the absolute repo path for a task: the task's repo
// field takes precedence, falling back to the project's entity page.
func resolveRepo(cfg config.Config, t task.Task) (string, error) {
	if t.Repo != "" {
		return vault.ExpandHome(t.Repo), nil
	}
	resolved, err := vault.ReadEntityRepo(cfg.Vault, t.Project)
	if err != nil {
		return "", fmt.Errorf("resolving repo for project %s: %w", t.Project, err)
	}
	return resolved, nil
}
