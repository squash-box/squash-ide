# squash-ide

Terminal task dispatcher for vault-based agentic workflows.

Reads Obsidian-style task files (with YAML frontmatter) from a vault directory
and exposes them via CLI subcommands. Designed to drive `/implement`-style work
from a TUI dashboard (coming in future tasks).

## Build

```bash
go build ./cmd/squash-ide
```

## Usage

### List tasks

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
```

## Project Layout

```
cmd/squash-ide/main.go   # CLI entry point (cobra)
internal/task/task.go     # Task struct
internal/vault/vault.go   # Vault parser (frontmatter + directory scanner)
testdata/                 # Test fixture markdown files
```

## Test

```bash
go test ./...
```
