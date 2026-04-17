# e2e tests

End-to-end tests that exercise the built `squash-ide` and `squash-ide-mcp`
binaries against a fixture vault + ephemeral git repo. They live under a
`//go:build e2e` build tag so `go test ./...` stays hermetic and fast.

## Run locally

```bash
make test-e2e
# or
go test -tags=e2e ./e2e/...
```

`TestMain` builds a fresh binary into a temp dir before each test run.
Tests use [`internal/testutil/vaultfix`](../internal/testutil/vaultfix) to
scaffold the vault tree and [`internal/testutil/gitfix`](../internal/testutil/gitfix)
to initialise a real bare origin + clone.

## What is covered

| Test | Lifecycle |
|------|-----------|
| `TestList_*` | `list` subcommand, JSON output, `--status` filter |
| `TestConfig_ShowsVault` | `config` subcommand |
| `TestSpawn_DryRun` | `spawn --dry-run` (no side effects) |
| `TestSpawn_TaskNotFound` | error-path readability |
| `TestSpawnCompleteLifecycle` | `spawn` → `complete` with real git worktrees |
| `TestBlockLifecycle` | `spawn` → `block --reason` |
| `TestMCP_InitializeThenStatus` | MCP server handshake over stdio |

## Dependencies

- `git` on `PATH` (skipped automatically otherwise)
- A working Go toolchain (to rebuild the binary before running)

## Not covered here

- Real tmux pane layout assertions — those require a real tmux session and
  are gated under a separate build tag not currently wired up in CI.
- TUI snapshot tests (`teatest`) — follow-up work: see [[T-018]].
