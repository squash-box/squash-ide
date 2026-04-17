# squash-ide

Terminal task dispatcher for vault-based agentic workflows.

Reads Obsidian-style task files (with YAML frontmatter) from a vault directory
and provides a TUI dashboard plus CLI subcommands. The `spawn` command creates
git worktrees and launches Claude in tmux panes (or fresh OS terminal windows
with `--no-tmux`); `complete` and `block` close the loop when work is done.

## Install

**Quickest path** (requires Go 1.24+):

```bash
git clone https://github.com/squash-box/squash-ide.git
cd squash-ide
make install
```

This builds `bin/squash-ide` and copies it to `~/.local/bin/squash-ide`. Make
sure `~/.local/bin` is on your `$PATH`.

Alternatives:

```bash
./scripts/install.sh                  # same as `make install`
make install PREFIX=/usr/local        # system-wide install
VERSION=v0.1.0 make install           # stamp a specific version into the binary
```

Plain `go build ./cmd/squash-ide` also works — it drops `squash-ide` in the
current directory.

## Usage

### TUI dashboard (default: tmux tiled panes)

Launch the interactive terminal UI:

```bash
squash-ide
```

By default this **bootstraps a tmux session** named `squash-ide` and runs the
TUI in the leftmost pane (60 cols wide). Each task you spawn opens as a new
pane to the right; existing right-side panes re-tile to share the available
horizontal space equally. The TUI pane stays pinned at its configured width.
Designed for ultra-wide monitors — one terminal window, many task panes.

**Controls:**

| Key         | Action                                      |
|-------------|---------------------------------------------|
| `↑` / `k`   | move up                                     |
| `↓` / `j`   | move down                                   |
| `Enter`     | spawn selected backlog task                 |
| `c`         | complete selected active task               |
| `b`         | block selected active task (prompts reason) |
| `Tab`       | open task detail pane                       |
| `Esc`       | close detail / cancel dialog / clear filter |
| `/`         | filter tasks by ID or title                 |
| `r`         | refresh vault                               |
| `q` / `Ctrl+C` | quit the TUI (the tmux session keeps running) |

Tasks are grouped by status (backlog, active, blocked). Active tasks are
marked with a `●` indicator and their detail pane shows the resolved worktree
path. The status bar shows the vault path and task counts.

Spawned panes are normal tmux panes — close one with `Ctrl+B x` (default tmux
prefix) and the rest re-tile on next spawn. Detach the whole session with
`Ctrl+B d`; reattach by re-running `squash-ide`.

If `tmux` is not on `$PATH`, squash-ide prints a warning and falls back to
the `--no-tmux` behaviour below.

### Spawn a task (CLI)

Create a git worktree and dispatch Claude into a new pane (or window):

```bash
squash-ide spawn T-008
```

This will:
1. Resolve the task's target repo (from the task `repo` field, or the project
   entity page's `repo` field).
2. Create a git worktree on branch `feat/T-008-<slug>` off `origin/main`.
3. Move the task from `backlog/` to `active/` and update frontmatter.
4. Update `tasks/board.md` and `wiki/log.md`.
5. Open `claude '/implement T-008'` either as a new tmux pane (default, when
   the calling shell is inside a tmux session) or as a fresh OS terminal
   window (auto-detected: ptyxis → gnome-terminal → x-terminal-emulator).

Preview what would happen without executing:

```bash
squash-ide spawn --dry-run T-008
```

### Complete an active task

```bash
squash-ide complete T-008
```

Removes the worktree, moves the task from `active/` to `archive/` (stamping
`status: done` and `completed: <today>`), and records a "complete" entry in
`wiki/log.md` plus a row under "Recently Completed" on the board.

### Block an active task

```bash
squash-ide block T-008 --reason "waiting on upstream fix"
```

Moves the task from `active/` to `blocked/` (appending a `## Blocked` section
with the reason to the task body), updates the board, and records a "block"
entry in the log. The worktree is **not** removed — unblocking is a manual
step: move the task back to `active/` and resume work in the existing
worktree.

### Escape hatch — disable tmux

To go back to the v1 "one OS window per spawn" workflow (handy on systems
without tmux, or if your window manager already tiles for you):

```bash
squash-ide --no-tmux
squash-ide --no-tmux spawn T-008
```

You can also disable tmux permanently in your config file:

```yaml
# ~/.config/squash-ide/config.yaml
tmux:
  enabled: false
```

### Tuning pane widths

```bash
squash-ide --tui-width 50 --min-pane-width 100
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

### List tasks (JSON)

```bash
squash-ide list
squash-ide list --status backlog
```

### Config

```bash
squash-ide config
```

Prints the resolved configuration with the provenance of each field
(default, config file, env var, or flag).

Configuration is resolved in the order **default → config file → env vars →
CLI flags**, with later sources overriding earlier ones.

Default config path: `$XDG_CONFIG_HOME/squash-ide/config.yaml` (usually
`~/.config/squash-ide/config.yaml`).

```yaml
vault: ~/GIT/agentic/tasks/personal
terminal:
  command: ""      # empty = auto-detect ptyxis → gnome-terminal → x-terminal-emulator
  args: ["--working-directory={cwd}", "--", "bash", "-c", "{exec}"]
spawn:
  command: claude
  args: ["/implement {task_id}"]
tmux:
  enabled: true
  session_name: squash-ide
  tui_width: 60
  min_pane_width: 80
```

Environment variables (override file):

| Var                | Effect                                 |
|--------------------|----------------------------------------|
| `SQUASH_VAULT`     | vault directory                        |
| `SQUASH_TERMINAL`  | terminal emulator command              |
| `SQUASH_SPAWN_CMD` | command to run inside spawned terminal |

CLI flags (override env):

| Flag                | Effect                              |
|---------------------|-------------------------------------|
| `--vault`           | vault directory                     |
| `--terminal`        | terminal emulator command           |
| `--spawn-cmd`       | command to run inside spawned terminal |
| `--no-tmux`         | disable tmux tiled-pane mode        |
| `--tui-width`       | fixed width (cols) for the TUI pane in tmux mode |
| `--min-pane-width`  | minimum width per spawned tmux pane |

## Project Layout

```
cmd/squash-ide/main.go           CLI entry point (cobra + TUI launcher + tmux bootstrap)
internal/task/task.go            Task struct
internal/vault/vault.go          Vault parser + entity page reader
internal/ui/                     Bubble Tea TUI (model, keys, styles)
internal/slug/slug.go            Branch-safe slug derivation from titles
internal/worktree/worktree.go    Git worktree create/remove wrapper
internal/spawner/spawner.go      Dispatches to tmux pane or OS terminal window
internal/tmux/                   tmux CLI wrapper + pane-width tiling math
internal/taskops/taskops.go      Vault file mutations (move task, update board/log)
internal/dispatch/dispatch.go    Orchestration: Run / Complete / Block
internal/config/                 Config loading + templating
Makefile                         build / install / test / dist
scripts/install.sh               Standalone installer
testdata/                        Test fixture markdown files
```

## Development

```bash
make build         # ./bin/squash-ide + ./bin/squash-ide-mcp
make test          # unit + e2e (race detector + coverage)
make test-unit     # fast path, no external binaries
make test-e2e      # e2e suite (needs git on PATH)
make cover         # per-function coverage from coverage.out
make vet           # go vet ./...
make fmt           # gofmt -w .
make clean         # rm -rf bin dist coverage.*
```

## Testing

The test suite is layered:

- **Unit tests** — each package has a `*_test.go` covering its exported
  surface. External processes are stubbed via `internal/exec.Runner` +
  `internal/testutil/fakerunner`; vault and git are built on disk via
  `internal/testutil/vaultfix` and `internal/testutil/gitfix`.
- **End-to-end tests** live under `e2e/` behind a `//go:build e2e` build
  tag. They build the real binaries, drive them through `os/exec` against
  a fixture vault + real git repo, and assert on vault state, board, log,
  worktree layout, and the MCP JSON-RPC handshake.

Run both layers via `make test`. CI (`.github/workflows/ci.yml`) runs
lint, unit, and e2e as parallel jobs and enforces a coverage floor on
every PR — see the `coverage gate` step for the current threshold.

TUI snapshot tests are not yet wired up (follow-up); tmux pane-layout
tests that need a real tmux session are gated behind `-tags=e2e_tmux`
and are not run in CI (`make test-e2e-tmux` to run locally).

## Troubleshooting

**`vault path ... does not exist`** — the resolved vault path isn't a
directory. Check `squash-ide config` to see which source (default, file, env,
flag) is providing the path and fix whichever layer is wrong.

**`task ... not found in vault`** — the task ID isn't present in
`tasks/{backlog,active,blocked,archive}/`. Verify the task file exists and
the file's YAML `id` field matches what you passed. `squash-ide list` prints
every task the reader can see.

**`entity page ... has no repo field`** — `spawn`, `complete`, and the TUI
worktree-path display resolve the repo from the task's `repo` field, falling
back to `wiki/entities/<project>.md`'s `repo` field. Set one of them.

**`terminal ... not found on PATH`** — the configured terminal emulator isn't
on `$PATH`. Either install it or leave `terminal.command` empty to let the
spawner auto-detect one (`ptyxis`, `gnome-terminal`, `x-terminal-emulator`).

**`warning: tmux not on PATH; falling back to OS-window spawn`** — either
install tmux or pass `--no-tmux` (or set `tmux.enabled: false` in the config
file) to silence the warning.

**A stale worktree lingers after `complete`** — if the worktree directory
contained uncommitted files, `git worktree remove` falls back to `--force`.
If that also fails (rare), remove manually: `git worktree remove --force
<path>` then `git branch -D <branch>`.

### Recovering from a wedged worktree

`squash-ide` owns the worktree lifecycle end-to-end: `spawn` creates,
`complete` / `deactivate` remove. If an agent (e.g. the `/implement` skill
in an earlier version) aborts partway through, you can end up with a
directory at the canonical worktree path that git does not know about.
`squash-ide spawn T-NNN` refuses to blindly overwrite this — it returns a
structured error pointing you at one of two recovery subcommands:

```bash
# The directory is a real worktree whose git bookkeeping is out of sync —
# re-register it so the next `spawn` adopts it.
squash-ide worktree adopt T-NNN

# The directory is junk (or you've decided the work is lost) — wipe it
# and delete the local branch so a fresh `spawn` can proceed.
squash-ide worktree clean T-NNN

# On an *active* task, `clean` refuses by default. If you really want to
# abandon the task (sends it back to backlog, tears down its tmux pane),
# pass --deactivate:
squash-ide worktree clean T-NNN --deactivate
```

`adopt` runs `git worktree repair` internally and is safe when the directory
has a `.git` reference pointing back at the main repo. It refuses a
non-git directory outright — in that case use `clean`.

If `spawn` reports `worktree path is registered on branch X, expected Y`,
a different task previously claimed the same path (unusual unless you
renamed a task title). Run `squash-ide worktree clean T-NNN` to discard
and re-spawn.

## Release

`v0.1.0` is the first tagged release. To cut a new one:

```bash
git tag -a v0.1.0 -m "v0.1.0"
git push origin v0.1.0
make dist VERSION=v0.1.0   # produces dist/squash-ide-v0.1.0-<os>-<arch>.tar.gz
```
