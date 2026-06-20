# Spike: embedded PTY + VT emulator in Bubble Tea (T-036)

**Throwaway prototype.** It de-risks the [native window-management initiative](../../README.md)
(T-036–T-041) before any production code is written. It spawns a command on a
PTY we own, emulates the child's terminal output into a cell grid with
[`charmbracelet/x/vt`](https://pkg.go.dev/github.com/charmbracelet/x/vt), and
renders that grid inside a lipgloss-bordered Bubble Tea panel beside a stub
"task list" box — mimicking squash-ide's real two-column layout. Keystrokes are
re-encoded and written back to the PTY master so the focused pane is genuinely
interactive.

It exists to answer two questions that gate the whole initiative:

1. **VT fidelity** — can a Go VT emulator faithfully render a full-screen TUI
   (alt-screen, cursor addressing, 256/truecolor, wide glyphs)?
2. **Input round-trip** — can a decoded `tea.KeyMsg` be re-encoded into the raw
   bytes the child expects, so typing works?

It does **not** touch `internal/` or `cmd/`, and lives in its own Go module so a
no-go verdict costs nothing to delete.

---

## Verdict: **GO** ✅

`charmbracelet/x/vt` faithfully emulates a full-screen TUI into a cell grid that
drops straight into a lipgloss-bordered Bubble Tea panel, and Bubble Tea's
decoded keys re-encode cleanly back onto the PTY master. Both load-bearing
unknowns hold. T-037 should proceed with `charmbracelet/x/vt` as the emulator
and the `internal/exec.Runner`-seamed PTY/pane package described in its spec.

The one thing T-037 must plan for is a **Charm-ecosystem version bump** in the
main module — see [Dependency isolation & version skew](#dependency-isolation--version-skew).
That is a known, bounded cost, not a blocker.

### Why `charmbracelet/x/vt` over the `hinshun/vt10x` fallback

`x/vt` was evaluated as the primary candidate and is the clear pick:

- Its `Emulator` is an `io.Writer` (`emu.Write(ptyBytes)`) and exposes
  `Render() string` (a styled grid ready to embed in a `View()`), `CellAt(x,y)`
  (for cell-level rendering with full `Style`/color access later),
  `Resize(w,h)`, `IsAltScreen()`, and `CursorPosition()`. That is exactly the
  lipgloss-cell integration T-037 needs — no glue layer.
- It returns `ultraviolet.Cell{Content, Style, Width}`, so wide-glyph width and
  truecolor/256 styling survive into the render path.
- It is the same Charm ecosystem already in `go.mod` (bubbletea, lipgloss,
  x/ansi, x/cellbuf), so it shares the ANSI parser rather than adding a
  competing one. `hinshun/vt10x` was not needed.

---

## Go/no-go checklist

| Criterion | Status | Evidence |
|---|---|---|
| VT fidelity: cursor addressing | ✅ verified | headless self-test |
| VT fidelity: 256-color | ✅ verified | headless self-test |
| VT fidelity: truecolor | ✅ verified | headless self-test |
| VT fidelity: wide/unicode glyphs (width 2) | ✅ verified | headless self-test |
| VT fidelity: alt-screen (DECSET 1049) | ✅ verified | headless self-test |
| Two-column lipgloss layout renders | ✅ verified | [rendered frame](#rendered-frame) |
| Input round-trip: typed bytes reach child & render | ✅ verified | rendered frame (`typed-into-pane`) + smoke test |
| Exception: child exit → clean EOF, no hang/panic | ✅ verified | headless self-test |
| Exception: binary not on PATH → clear error, exit 1 | ✅ verified | headless self-test |
| `-debug` trace (`pty<` / `key>` / `resize`) | ✅ verified | [captured markers](#debug-trace-markers) |
| Key re-encode: runes / Enter / Ctrl-C → child | ✅ verified | captured `key>` markers + child echo |
| Resize fires `pty.Setsize` + `emu.Resize` | ✅ verified | captured `resize:` markers |
| Main module `go build`/`go test ./...` unaffected | ✅ verified | nested module, 0 spike pkgs in `./...` |
| Interactive vim / htop / claude render & typing | ⏳ **operator** | [checklist below](#operator-checklist-needs-a-real-terminal) |
| Resize *visual reflow* during a live `claude` session | ⏳ **operator** | mechanism verified above; needs eyes on claude |
| Bracketed paste arrives as one paste | ⏳ **operator** | encoder wraps `\e[200~`…`\e[201~`; verify against claude |

The ⏳ rows need a real interactive terminal and a human eye — they cannot be
observed from a headless harness. Everything verifiable without a human is
verified.

---

## Running it

```bash
go run ./spike/nativepty                 # default: spawn `claude`
go run ./spike/nativepty vim /etc/hosts  # or any command + args
go run ./spike/nativepty htop
go run ./spike/nativepty -mouse claude    # enable the click-to-focus probe
go run ./spike/nativepty -debug claude 2>trace.log   # trace; redirect off the alt-screen
go run ./spike/nativepty -selftest        # headless VT-pipeline check, no TTY
```

**Detach key:** `ctrl+\` force-quits the spike. Everything else — including
`Ctrl-C` and `Ctrl-D` — is forwarded to the child so interrupt/EOF can be
tested. When the child exits on its own, the pane shows `process exited (…)` and
any key dismisses the prototype.

> First Go run will fetch a Go 1.25 toolchain automatically (see version note
> below). Subsequent runs are instant.

---

## Headless self-test

`go run ./spike/nativepty -selftest` drives the **real PTY → emulator pipeline**
(no TTY, no Bubble Tea) against short non-interactive children emitting known
escape sequences, then asserts the resulting cell grid. It is the reproducible
evidence behind the VT-fidelity verdict. Current output:

```
PASS cursor addressing: 'HELLO' at row2col3
PASS 256-colour: green channel dominant at row4
PASS truecolour: red channel dominant at row5
PASS wide glyph: '界' present at row6
PASS wide glyph: occupies width 2
PASS alt-screen: IsAltScreen() true after DECSET 1049
PASS alt-screen: content rendered on alt buffer
PASS child-exit path: PTY read reached EOF without hanging
PASS binary-not-found path: start error surfaced cleanly

self-test: 9 passed, 0 failed
```

These are spike observations, not a production test suite — **production test
coverage is T-037's responsibility.**

## Rendered frame

Captured by driving the built binary through a 90×20 PTY against a child that
emits addressed, colored content, typing `typed-into-pane` into it mid-run, and
replaying the spike's output through an emulator to reconstruct the final
screen. This is the spike's *own* `View()`, proving the bordered two-column
layout **and** the input round-trip (the typed text appears in the right pane):

```
╭──────────────────────╮ ╭───────────────────────────────────────────────────────────────╮
│squash-ide            │ │CLAUDE-LIKE TUI                                                │
│(stub task list)      │ │prompt> hello world                                            │
│                      │ │  box: + -- + | ok | + -- +                                    │
│> sh                  │ │typed-into-pane                                                │
│  T-037               │ │                                                               │
│  T-038               │ │                                                               │
│                      │ │                                                               │
╰──────────────────────╯ ╰───────────────────────────────────────────────────────────────╯
 spike:nativepty │ sh -c … │ grid 63x17 │ focus:pty │ last:… │ ctrl+\ quit
```

## Debug trace markers

Running `-debug` against `cat` through a PTY and typing `A`, `b`, Enter, Ctrl-C,
then resizing, the trace shows the full round-trip — keys re-encoded out,
the child's echo coming back in, and resize firing `pty.Setsize`:

```
key>  Ab          -> "Ab"
key>  enter       -> "\r"
key>  ctrl+c      -> "\x03"          # Ctrl-C forwarded to the child, not intercepted
pty<     2 bytes "Ab"                # child echoed the runes
pty<     2 bytes "^C"
pty<     6 bytes "\r\nAb\r\n"
resize: window  90x20 -> pane grid 63x17
resize: window 100x24 -> pane grid 73x21
```

(In normal use redirect with `2>trace.log` so the trace doesn't paint over the
alt-screen.)

---

## Operator checklist (needs a real terminal)

Run these by hand to confirm the visual/interactive observations a headless
harness can't make. Tick them into this list:

- [ ] `go run ./spike/nativepty vim /etc/hosts` — open, move cursor, `:q`;
      status line, tildes, cursor position render with no cell corruption.
- [ ] `go run ./spike/nativepty htop` — colored bars and live refresh update
      without tearing.
- [ ] `go run ./spike/nativepty claude` — TUI renders legibly (prompt, streamed
      output, spinner, boxes); type a prompt + Enter and get a response.
      **Load-bearing.**
- [ ] Resize the host terminal mid-`claude` — reflows to the new width cleanly.
- [ ] Truecolor/256 swatch (htop, a `printf` truecolor line) shows correct
      colors.
- [ ] Paste multi-line text into claude — arrives as one paste (bracketed), not
      line-by-line.
- [ ] Wide/box-drawing glyphs in claude's output align to cell boundaries.
- [ ] Ctrl-D in claude / `:q` in vim — pane shows `process exited (…)`, no hang.

## Input round-trip: coverage & gaps

`keys.go` reverses Bubble Tea's stdin decode. The reverse is exact because
Bubble Tea sets a Ctrl-key's `KeyType` to the literal C0 byte it represents
(`KeyCtrlC == 3`), so the whole Ctrl-A…Ctrl-Z range emits verbatim in one arm.

| Input | Re-encoded as | Notes |
|---|---|---|
| printable runes | UTF-8 bytes | `Alt` → ESC prefix |
| Enter | `\r` (CR) | what a real terminal sends, not `\n` |
| Backspace | `0x7f` (DEL) | |
| Tab / Esc | `\t` / `0x1b` | |
| Ctrl-A … Ctrl-Z, Ctrl-symbols | the C0 byte (`0x01`–`0x1f`) | covers Ctrl-C `0x03`, Ctrl-D `0x04` |
| ↑ ↓ → ← | `\e[A` / `\e[B` / `\e[C` / `\e[D` | |
| Home / End | `\e[H` / `\e[F` | |
| PgUp / PgDn / Delete | `\e[5~` / `\e[6~` / `\e[3~` | |
| bracketed paste | `\e[200~`…text…`\e[201~` | child must have enabled DECSET 2004 |

**Documented gaps** (completeness is T-037's job, not the spike's):

- **Function keys (F1–F12)** and shift/ctrl-modified navigation are unmapped —
  they log `(unmapped, dropped)` under `-debug`.
- **Alt + navigation** uses the simple ESC-prefix Meta convention, not xterm's
  modern modifier encoding (`CSI 1 ; 3 A`).
- **Mouse** is a click-to-focus probe only (gated behind `-mouse`); it is not
  forwarded into the child. Real mouse routing is out of scope (T-040).

---

## Dependency isolation & version skew

This is a **separate, nested Go module** (`spike/nativepty/go.mod`). `go build`
/ `go test ./...` run from the repo root prune at module boundaries, so the
spike's deps never enter the main `squash-ide` module graph — verified: the main
module's `./...` lists **zero** spike packages and its `go.mod` gains none of
`x/vt` / `creack/pty` / `ultraviolet`. A no-go verdict is `rm -rf spike/`.
(Chosen over a `//go:build spike` tag, which would still need the deps declared
in the main `go.mod`.)

**Finding for T-037 — adopting `x/vt` forces a Charm bump.** Resolving a
*consistent* set surfaced a real version-skew trap the main module currently
sidesteps:

- `x/vt` (June 2026) requires `x/ansi v0.11.7` and `ultraviolet`, which use a
  newer `ansi.Style` API (`Italic(bool)`, curly/dotted underlines).
- The main module today pins `x/ansi v0.11.6` + `x/cellbuf v0.0.15`, whose
  `cellbuf` uses the *old* `ansi.Style` API and breaks against `v0.11.7`.
- The only `cellbuf` compatible with `ansi v0.11.7` is the June monorepo
  snapshot, whose `v0.0.0-2026…` pseudo-version semver-sorts **below** the
  tagged `v0.0.x` that lipgloss pulls — so MVS can never select it on its own.

The spike works around this with a single `replace` directive forcing the June
`cellbuf`, and a `go 1.25.0` directive (`ultraviolet`'s floor). **When T-037
adopts `x/vt` into the main module it must bump `x/ansi`→`v0.11.7`,
`x/cellbuf`→the June snapshot, add `ultraviolet`, and raise the module's `go`
directive to `1.25` (CI's Go version too).** Pin against the same
bubbletea/lipgloss the production TUI uses (here `v1.3.10` / `v1.1.0`) so the
emulator is tested against the real render stack and not a skewed one.

## Out of scope (per the task)

Multi-pane layout, scrollback/copy-mode, full mouse routing, production input
completeness, the final engine-toggle flag name, and any change to `internal/`,
`cmd/`, `Makefile`, or CI. Those are T-037–T-041.
