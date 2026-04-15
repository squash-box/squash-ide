# squash-ide

Terminal task dispatcher for vault-based agentic workflows.

Reads Obsidian-style task files (with YAML frontmatter) from a vault directory
and provides a TUI dashboard and CLI subcommands. The `spawn` command creates
git worktrees and launches Claude in tmux panes (or fresh OS terminal windows
with `--no-tmux`) for task implementation.

## Build

```bash
go build ./cmd/squash-ide
```

## Usage

### TUI dashboard (default: tmux tiled panes)

Launch the interactive terminal UI:

```bash
./squash-ide
```

By default this **bootstraps a tmux session** named `squash-ide` and runs the
TUI in the leftmost pane (60 cols wide). Each task you spawn opens as a new
pane to the right; existing right-side panes re-tile to share the available
horizontal space equally. The TUI pane stays pinned at its configured width.
Designed for ultra-wide monitors — one terminal window, many task panes.

**Controls:**
- `↑`/`↓` or `j`/`k` — navigate tasks
- `Enter` — spawn the selected backlog task in a new pane to the right
- `Tab` — open task detail pane
- `Esc` — close detail pane / clear filter
- `/` — filter tasks by ID or title
- `r` — refresh vault data
- `q` or `Ctrl+C` — quit the TUI (the tmux session keeps running)

Spawned panes are normal tmux panes — close one with `Ctrl+B x` (default tmux
prefix) and the rest re-tile on next spawn. Detach the whole session with
`Ctrl+B d`; reattach by re-running `./squash-ide`.

If `tmux` is not on `$PATH`, squash-ide prints a warning and falls back to
the `--no-tmux` behaviour below.

### Spawn a task (CLI)

Create a git worktree and dispatch Claude into a new pane (or window):

```bash
./squash-ide spawn T-008
```

This will:
1. Resolve the task's target repo (from task `repo` field or project entity page)
2. Create a git worktree on branch `feat/T-008-<slug>`
3. Move the task from `backlog/` to `active/` and update frontmatter
4. Update `tasks/board.md` and `wiki/log.md`
5. Open `claude '/implement T-008'` either as a new tmux pane (default, when
   the calling shell is inside a tmux session) or as a fresh OS terminal
   window (auto-detected: ptyxis → gnome-terminal → x-terminal-emulator)

### Escape hatch — disable tmux

To go back to the v1 "one OS window per spawn" workflow (handy on systems
without tmux, or if your window manager already tiles for you):

```bash
./squash-ide --no-tmux
./squash-ide --no-tmux spawn T-008
```

You can also disable tmux permanently in your config file:

```yaml
# ~/.config/squash-ide/config.yaml
tmux:
  enabled: false
```

### Tuning pane widths

```bash
./squash-ide --tui-width 50 --min-pane-width 100
```

Or in the config file:

```yaml
tmux:
  enabled: true
  session_name: squash-ide
  tui_width: 60         # cols pinned for the TUI on the left
  min_pane_width: 80    # spawn rejected if any pane would fall below this
```

If a new spawn would force any existing pane below `min_pane_width`, the
spawn is rejected with a clear error rather than silently squeezing panes.
Close some panes (`Ctrl+B x`) and try again.

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
cmd/squash-ide/main.go         # CLI entry point (cobra + TUI + spawn + tmux bootstrap)
internal/task/task.go           # Task struct
internal/vault/vault.go         # Vault parser + entity page reader
internal/ui/                    # Bubble Tea TUI (model, styles, keys)
internal/slug/slug.go           # Branch-safe slug derivation from titles
internal/worktree/worktree.go   # Git worktree creation (shells out to git)
internal/spawner/spawner.go     # Dispatches to tmux pane or OS terminal window
internal/tmux/                  # tmux CLI wrapper + pane-width tiling math
internal/taskops/taskops.go     # Vault file mutations (move task, update board/log)
internal/config/                # Config loader (defaults + file + env + flags)
testdata/                       # Test fixture markdown files
```

## Test

```bash
go test ./...
```
