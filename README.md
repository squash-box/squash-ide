# squash-ide

Terminal task dispatcher for vault-based agentic workflows.

Reads Obsidian-style task files (with YAML frontmatter) from a vault directory
and provides a TUI dashboard and CLI subcommands. The `spawn` command creates
git worktrees and launches Claude in new terminal windows for task implementation.

## Build

```bash
go build ./cmd/squash-ide
```

## Usage

### TUI dashboard

Launch the interactive terminal UI:

```bash
./squash-ide
```

**Controls:**
- `↑`/`↓` or `j`/`k` — navigate tasks
- `Enter` or `Tab` — open task detail pane
- `Esc` — close detail pane / clear filter
- `/` — filter tasks by ID or title
- `r` — refresh vault data
- `q` or `Ctrl+C` — quit

Tasks are grouped by status (backlog, active, blocked). The status bar shows
the vault path and task counts.

### Spawn a task

Create a git worktree and open a new terminal window running Claude:

```bash
./squash-ide spawn T-008
```

This will:
1. Resolve the task's target repo (from task `repo` field or project entity page)
2. Create a git worktree on branch `feat/T-008-<slug>`
3. Move the task from `backlog/` to `active/` and update frontmatter
4. Update `tasks/board.md` and `wiki/log.md`
5. Spawn `gnome-terminal` running `claude '/implement T-008'` in the worktree

Preview what would happen without executing:

```bash
./squash-ide spawn --dry-run T-008
```

### List tasks (JSON)

Print all tasks from the vault as JSON:

```bash
./squash-ide list
```

Filter by status:

```bash
./squash-ide list --status backlog
```

### Custom vault path

By default, the vault path is `~/GIT/agentic/tasks/personal/`. Override with:

```bash
./squash-ide --vault /path/to/vault spawn T-001
./squash-ide --vault /path/to/vault
```

## Project Layout

```
cmd/squash-ide/main.go         # CLI entry point (cobra + TUI + spawn)
internal/task/task.go           # Task struct
internal/vault/vault.go         # Vault parser + entity page reader
internal/ui/                    # Bubble Tea TUI (model, styles, keys)
internal/slug/slug.go           # Branch-safe slug derivation from titles
internal/worktree/worktree.go   # Git worktree creation (shells out to git)
internal/spawner/spawner.go     # Terminal process spawning (gnome-terminal)
internal/taskops/taskops.go     # Vault file mutations (move task, update board/log)
testdata/                       # Test fixture markdown files
```

## Test

```bash
go test ./...
```
