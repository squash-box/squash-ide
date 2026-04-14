# squash-ide

Terminal task dispatcher for vault-based agentic workflows.

Reads Obsidian-style task files (with YAML frontmatter) from a vault directory
and provides a TUI dashboard and CLI subcommands.

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
./squash-ide --vault /path/to/vault list
./squash-ide --vault /path/to/vault
```

## Project Layout

```
cmd/squash-ide/main.go      # CLI entry point (cobra + TUI launcher)
internal/task/task.go        # Task struct
internal/vault/vault.go      # Vault parser (frontmatter + directory scanner)
internal/ui/model.go         # Bubble Tea TUI model (Init/Update/View)
internal/ui/styles.go        # lipgloss styles
internal/ui/keys.go          # Key bindings
testdata/                    # Test fixture markdown files
```

## Test

```bash
go test ./...
```
