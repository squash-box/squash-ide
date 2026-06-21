// Package config loads squash-ide configuration from a YAML file, environment
// variables, and CLI flag overrides. The precedence, from lowest to highest,
// is: built-in defaults → config file → env vars → CLI flags.
//
// Each resolved field's provenance is recorded in Config.Sources so the
// `squash-ide config` subcommand can show where each value came from.
package config

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Source identifies where a resolved config value came from.
type Source string

const (
	SourceDefault Source = "default"
	SourceFile    Source = "file"
	SourceEnv     Source = "env"
	SourceFlag    Source = "flag"
	SourceDerived Source = "derived from vault"
)

// Terminal describes the terminal emulator to invoke for spawn.
// An empty Command means "auto-detect" — the spawner will try a built-in
// list of known terminals (ptyxis → gnome-terminal → x-terminal-emulator).
type Terminal struct {
	Command string   `yaml:"command"`
	Args    []string `yaml:"args"`
}

// Spawn describes the command to run inside the spawned terminal.
type Spawn struct {
	Command string   `yaml:"command"`
	Args    []string `yaml:"args"`
}

// Engine selects how spawned task processes are rendered alongside the TUI.
//
//   - EngineTmux (the default) bootstraps a tmux session and tiles task panes
//     to the right of the TUI — the shipping v2 workflow (T-011).
//   - EngineNative renders the in-process internal/pane manager (T-037) inside
//     the TUI itself, with no tmux dependency. It is opt-in until the explicit
//     cutover in T-041; defaulting to tmux keeps `main` behaviour unchanged.
const (
	EngineTmux   = "tmux"
	EngineNative = "native"
)

// Layout names for the native engine's pane region (T-040). They select the
// layout Strategy: equal columns, stacked rows, tabs, or the responsive auto-
// mode that reflows columns→stack→tabs as the region shrinks (so a spawn never
// hard-rejects). These literals mirror pane.Layout* / pane.StrategyForName; they
// are duplicated here (rather than importing pane) to keep config free of the
// pane package's PTY dependencies, the same way the status strings are mirrored.
const (
	LayoutColumns    = "columns"
	LayoutStack      = "stack"
	LayoutTabs       = "tabs"
	LayoutResponsive = "responsive"
)

// Progress controls the per-task lifecycle progress strip in the task view
// (T-053). Show toggles the strip entirely; PollCI toggles the throttled `gh`
// network poll that drives the pr/ci lights (disable it to keep squash-ide
// off the network — the four Claude-reported stage lights still render).
// Both default true, so absent config keeps the current-equivalent behaviour.
type Progress struct {
	Show   bool `yaml:"show"`
	PollCI bool `yaml:"poll_ci"`
}

// Tmux controls the v2 single-terminal tiled-pane workflow (T-011).
// When Enabled, squash-ide bootstraps a tmux session and opens spawned tasks
// as new panes to the right of the TUI instead of new OS terminal windows.
type Tmux struct {
	Enabled      bool   `yaml:"enabled"`
	SessionName  string `yaml:"session_name"`
	TUIWidth     int    `yaml:"tui_width"`
	PaneWidth    int    `yaml:"pane_width"`
	MinPaneWidth int    `yaml:"min_pane_width"`
}

// Config is the resolved squash-ide configuration.
type Config struct {
	Vault    string   `yaml:"vault"`
	Engine   string   `yaml:"engine"`
	Terminal Terminal `yaml:"terminal"`
	Spawn    Spawn    `yaml:"spawn"`
	Tmux     Tmux     `yaml:"tmux"`
	Progress Progress `yaml:"progress"`

	// Layout selects the native engine's pane layout strategy (T-040), one of
	// the Layout* constants. Default "responsive". Ignored under engine: tmux.
	Layout string `yaml:"layout"`
	// FocusFollowsInput, when true, auto-surfaces a native pane that enters
	// input_required (the in-TUI dual of the [[T-034]] notification click).
	// Ignored under engine: tmux.
	FocusFollowsInput bool `yaml:"focus_follows_input"`
	// PaneStats, when true (the default), shows a per-pane CPU%/memory readout
	// floated to the right of each native pane header, refreshed every 15s
	// (T-055). The figure is currently Linux-only (read from /proc); off Linux
	// the readout is a silent no-op. Ignored under engine: tmux.
	PaneStats bool `yaml:"pane_stats"`

	// Sources records the provenance of each resolved field.
	// Keys: "vault", "engine", "layout", "focus_follows_input", "pane_stats",
	// "progress.show", "progress.poll_ci",
	// "terminal.command", "terminal.args", "spawn.command", "spawn.args",
	// "tmux.enabled", "tmux.session_name", "tmux.tui_width", "tmux.min_pane_width".
	Sources map[string]Source `yaml:"-"`

	// Path is the resolved path to the config file, if one was loaded.
	Path string `yaml:"-"`
}

// Overrides bundles CLI flag values that should win over env and file.
// Empty fields are treated as "not provided" and skipped.
type Overrides struct {
	Vault    string
	Engine   string
	Layout   string
	Terminal string
	SpawnCmd string

	// FocusFollowsInput is a tri-state flag: nil = not provided (config/env/
	// default wins), non-nil = forced to the pointed-at value.
	FocusFollowsInput *bool

	// PaneStats is the same tri-state flag for the header CPU/mem readout
	// (T-055): nil = not provided, non-nil = forced.
	PaneStats *bool

	// Tmux flag overrides. --no-tmux is presence-only: true forces tmux off,
	// absence (false) is a no-op (config/env still wins).
	NoTmux       bool
	TUIWidth     int // 0 => not provided
	PaneWidth    int // 0 => not provided
	MinPaneWidth int // 0 => not provided

	// ConfigPath, when non-empty, overrides the default ~/.config/squash-ide/config.yaml
	// lookup. Useful for tests.
	ConfigPath string
}

// Defaults returns the built-in defaults — used when neither file, env, nor
// flag provides a value.
func Defaults() Config {
	return Config{
		Vault:             "~/GIT/agentic/tasks/personal/",
		Engine:            EngineTmux,
		Layout:            LayoutResponsive,
		FocusFollowsInput: true,
		PaneStats:         true,
		Terminal: Terminal{
			// Empty = auto-detect (preserves T-007's terminal detection).
			Command: "",
			Args:    []string{"--working-directory={cwd}", "--", "bash", "-c", "{exec}"},
		},
		Spawn: Spawn{
			Command: "claude",
			Args:    []string{"/implement {task_id}"},
		},
		Tmux: Tmux{
			Enabled:      true,
			SessionName:  "squash-ide",
			TUIWidth:     60,
			PaneWidth:    80,
			MinPaneWidth: 80,
		},
		Progress: Progress{
			Show:   true,
			PollCI: true,
		},
		Sources: map[string]Source{
			"vault":               SourceDefault,
			"engine":              SourceDefault,
			"layout":              SourceDefault,
			"focus_follows_input": SourceDefault,
			"pane_stats":          SourceDefault,
			"progress.show":       SourceDefault,
			"progress.poll_ci":    SourceDefault,
			"terminal.command":    SourceDefault,
			"terminal.args":       SourceDefault,
			"spawn.command":       SourceDefault,
			"spawn.args":          SourceDefault,
			"tmux.enabled":        SourceDefault,
			"tmux.session_name":   SourceDefault,
			"tmux.tui_width":      SourceDefault,
			"tmux.pane_width":     SourceDefault,
			"tmux.min_pane_width": SourceDefault,
		},
	}
}

// DefaultConfigPath returns the conventional config path:
// $XDG_CONFIG_HOME/squash-ide/config.yaml (or the OS equivalent).
func DefaultConfigPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolving user config dir: %w", err)
	}
	return filepath.Join(dir, "squash-ide", "config.yaml"), nil
}

// Load resolves config by applying, in order: defaults, config file, env vars,
// and flag overrides. A missing config file is not an error — defaults are
// used instead. A malformed config file IS an error.
func Load(ov Overrides) (Config, error) {
	cfg := Defaults()

	path := ov.ConfigPath
	if path == "" {
		var err error
		path, err = DefaultConfigPath()
		if err != nil {
			return Config{}, err
		}
	}

	if err := applyFile(&cfg, path); err != nil {
		return Config{}, err
	}

	applyEnv(&cfg)
	applyOverrides(&cfg, ov)

	// Validate the resolved engine here (not in Validate) so an unknown value
	// fails config load itself — the earliest, clearest place to reject it.
	if cfg.Engine != EngineTmux && cfg.Engine != EngineNative {
		return Config{}, fmt.Errorf("invalid engine %q (%s): must be %q or %q",
			cfg.Engine, source(cfg, "engine"), EngineTmux, EngineNative)
	}

	// Validate the resolved layout the same way (T-040) — an unknown strategy
	// fails at load with the allowed set, mirroring the engine check.
	if !validLayout(cfg.Layout) {
		return Config{}, fmt.Errorf("invalid layout %q (%s): must be one of %q, %q, %q, %q",
			cfg.Layout, source(cfg, "layout"),
			LayoutColumns, LayoutStack, LayoutTabs, LayoutResponsive)
	}

	// If no layer supplied an explicit tmux.session_name, derive one from the
	// resolved vault so each vault gets its own tmux session and two concurrent
	// squash-ide instances pointing at different vaults don't collide.
	if cfg.Sources["tmux.session_name"] == SourceDefault {
		cfg.Tmux.SessionName = DeriveSessionName(cfg.Vault)
		cfg.Sources["tmux.session_name"] = SourceDerived
	}

	return cfg, nil
}

// DeriveSessionName returns a tmux-safe session name derived from a vault path.
// Example: "~/GIT/agentic/tasks/personal/" → "squash-ide-personal".
// Non-alphanumeric characters (other than `-` and `_`) are replaced with `-`
// because tmux disallows `.` and `:` in session names.
func DeriveSessionName(vaultPath string) string {
	base := filepath.Base(filepath.Clean(ExpandHome(vaultPath)))
	var b strings.Builder
	b.WriteString("squash-ide-")
	for _, r := range base {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	return b.String()
}

// fileTmux mirrors Tmux but uses *bool so we can tell "user omitted the field"
// (nil) from "user wrote enabled: false" (non-nil pointing at false).
type fileTmux struct {
	Enabled      *bool  `yaml:"enabled"`
	SessionName  string `yaml:"session_name"`
	TUIWidth     int    `yaml:"tui_width"`
	PaneWidth    int    `yaml:"pane_width"`
	MinPaneWidth int    `yaml:"min_pane_width"`
}

// fileProgress mirrors Progress with *bool fields so "omitted" (nil) is
// distinguishable from "explicitly false".
type fileProgress struct {
	Show   *bool `yaml:"show"`
	PollCI *bool `yaml:"poll_ci"`
}

// fileConfig is the parse-only shape of the YAML file. It mirrors Config
// but uses pointers / sentinel zeros where needed for presence detection.
type fileConfig struct {
	Vault             string        `yaml:"vault"`
	Engine            string        `yaml:"engine"`
	Layout            string        `yaml:"layout"`
	FocusFollowsInput *bool         `yaml:"focus_follows_input"`
	PaneStats         *bool         `yaml:"pane_stats"`
	Terminal          Terminal      `yaml:"terminal"`
	Spawn             Spawn         `yaml:"spawn"`
	Tmux              *fileTmux     `yaml:"tmux"`
	Progress          *fileProgress `yaml:"progress"`
}

// applyFile reads the YAML config at path (if it exists) and overlays its
// non-zero fields onto cfg. A missing file is silently ignored.
func applyFile(cfg *Config, path string) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading config %s: %w", path, err)
	}

	var fc fileConfig
	if err := yaml.Unmarshal(data, &fc); err != nil {
		return fmt.Errorf("parsing config %s: %w", path, err)
	}

	cfg.Path = path
	if fc.Vault != "" {
		cfg.Vault = fc.Vault
		cfg.Sources["vault"] = SourceFile
	}
	if fc.Engine != "" {
		cfg.Engine = fc.Engine
		cfg.Sources["engine"] = SourceFile
	}
	if fc.Layout != "" {
		cfg.Layout = fc.Layout
		cfg.Sources["layout"] = SourceFile
	}
	if fc.FocusFollowsInput != nil {
		cfg.FocusFollowsInput = *fc.FocusFollowsInput
		cfg.Sources["focus_follows_input"] = SourceFile
	}
	if fc.PaneStats != nil {
		cfg.PaneStats = *fc.PaneStats
		cfg.Sources["pane_stats"] = SourceFile
	}
	if fc.Terminal.Command != "" {
		cfg.Terminal.Command = fc.Terminal.Command
		cfg.Sources["terminal.command"] = SourceFile
	}
	if fc.Terminal.Args != nil {
		cfg.Terminal.Args = fc.Terminal.Args
		cfg.Sources["terminal.args"] = SourceFile
	}
	if fc.Spawn.Command != "" {
		cfg.Spawn.Command = fc.Spawn.Command
		cfg.Sources["spawn.command"] = SourceFile
	}
	if fc.Spawn.Args != nil {
		cfg.Spawn.Args = fc.Spawn.Args
		cfg.Sources["spawn.args"] = SourceFile
	}
	if fc.Tmux != nil {
		if fc.Tmux.Enabled != nil {
			cfg.Tmux.Enabled = *fc.Tmux.Enabled
			cfg.Sources["tmux.enabled"] = SourceFile
		}
		if fc.Tmux.SessionName != "" {
			cfg.Tmux.SessionName = fc.Tmux.SessionName
			cfg.Sources["tmux.session_name"] = SourceFile
		}
		if fc.Tmux.TUIWidth > 0 {
			cfg.Tmux.TUIWidth = fc.Tmux.TUIWidth
			cfg.Sources["tmux.tui_width"] = SourceFile
		}
		if fc.Tmux.PaneWidth > 0 {
			cfg.Tmux.PaneWidth = fc.Tmux.PaneWidth
			cfg.Sources["tmux.pane_width"] = SourceFile
		}
		if fc.Tmux.MinPaneWidth > 0 {
			cfg.Tmux.MinPaneWidth = fc.Tmux.MinPaneWidth
			cfg.Sources["tmux.min_pane_width"] = SourceFile
		}
	}
	if fc.Progress != nil {
		if fc.Progress.Show != nil {
			cfg.Progress.Show = *fc.Progress.Show
			cfg.Sources["progress.show"] = SourceFile
		}
		if fc.Progress.PollCI != nil {
			cfg.Progress.PollCI = *fc.Progress.PollCI
			cfg.Sources["progress.poll_ci"] = SourceFile
		}
	}
	return nil
}

// applyEnv overlays SQUASH_* env vars onto cfg.
func applyEnv(cfg *Config) {
	if v := os.Getenv("SQUASH_VAULT"); v != "" {
		cfg.Vault = v
		cfg.Sources["vault"] = SourceEnv
	}
	if v := os.Getenv("SQUASH_ENGINE"); v != "" {
		cfg.Engine = v
		cfg.Sources["engine"] = SourceEnv
	}
	if v := os.Getenv("SQUASH_LAYOUT"); v != "" {
		cfg.Layout = v
		cfg.Sources["layout"] = SourceEnv
	}
	if v := os.Getenv("SQUASH_FOCUS_FOLLOWS_INPUT"); v != "" {
		cfg.FocusFollowsInput = isTruthy(v)
		cfg.Sources["focus_follows_input"] = SourceEnv
	}
	if v := os.Getenv("SQUASH_PANE_STATS"); v != "" {
		cfg.PaneStats = isTruthy(v)
		cfg.Sources["pane_stats"] = SourceEnv
	}
	if v := os.Getenv("SQUASH_PROGRESS_SHOW"); v != "" {
		cfg.Progress.Show = isTruthy(v)
		cfg.Sources["progress.show"] = SourceEnv
	}
	if v := os.Getenv("SQUASH_PROGRESS_POLL_CI"); v != "" {
		cfg.Progress.PollCI = isTruthy(v)
		cfg.Sources["progress.poll_ci"] = SourceEnv
	}
	if v := os.Getenv("SQUASH_TERMINAL"); v != "" {
		cfg.Terminal.Command = v
		cfg.Sources["terminal.command"] = SourceEnv
	}
	if v := os.Getenv("SQUASH_SPAWN_CMD"); v != "" {
		cfg.Spawn.Command = v
		cfg.Sources["spawn.command"] = SourceEnv
	}
}

// applyOverrides overlays flag values onto cfg (highest precedence).
func applyOverrides(cfg *Config, ov Overrides) {
	if ov.Vault != "" {
		cfg.Vault = ov.Vault
		cfg.Sources["vault"] = SourceFlag
	}
	if ov.Engine != "" {
		cfg.Engine = ov.Engine
		cfg.Sources["engine"] = SourceFlag
	}
	if ov.Layout != "" {
		cfg.Layout = ov.Layout
		cfg.Sources["layout"] = SourceFlag
	}
	if ov.FocusFollowsInput != nil {
		cfg.FocusFollowsInput = *ov.FocusFollowsInput
		cfg.Sources["focus_follows_input"] = SourceFlag
	}
	if ov.PaneStats != nil {
		cfg.PaneStats = *ov.PaneStats
		cfg.Sources["pane_stats"] = SourceFlag
	}
	if ov.Terminal != "" {
		cfg.Terminal.Command = ov.Terminal
		cfg.Sources["terminal.command"] = SourceFlag
	}
	if ov.SpawnCmd != "" {
		cfg.Spawn.Command = ov.SpawnCmd
		cfg.Sources["spawn.command"] = SourceFlag
	}
	// --no-tmux is the only way to disable tmux from the CLI; absence of
	// the flag never re-enables it (config/env still wins).
	if ov.NoTmux {
		cfg.Tmux.Enabled = false
		cfg.Sources["tmux.enabled"] = SourceFlag
	}
	if ov.TUIWidth > 0 {
		cfg.Tmux.TUIWidth = ov.TUIWidth
		cfg.Sources["tmux.tui_width"] = SourceFlag
	}
	if ov.PaneWidth > 0 {
		cfg.Tmux.PaneWidth = ov.PaneWidth
		cfg.Sources["tmux.pane_width"] = SourceFlag
	}
	if ov.MinPaneWidth > 0 {
		cfg.Tmux.MinPaneWidth = ov.MinPaneWidth
		cfg.Sources["tmux.min_pane_width"] = SourceFlag
	}
}

// Validate checks that the resolved config can actually be used:
//   - the vault path (after ~ expansion) exists and is a directory
//   - if terminal.command is set, the binary is on $PATH
//
// An empty terminal.command is fine — auto-detection handles it at spawn time.
func Validate(cfg Config) error {
	vaultPath := ExpandHome(cfg.Vault)
	info, err := os.Stat(vaultPath)
	if err != nil {
		return fmt.Errorf("vault path %q (%s): %w", cfg.Vault, source(cfg, "vault"), err)
	}
	if !info.IsDir() {
		return fmt.Errorf("vault path %q (%s) is not a directory", cfg.Vault, source(cfg, "vault"))
	}
	if cfg.Terminal.Command != "" {
		if _, err := exec.LookPath(cfg.Terminal.Command); err != nil {
			return fmt.Errorf("terminal %q (%s) not found on PATH: %w",
				cfg.Terminal.Command, source(cfg, "terminal.command"), err)
		}
	}
	return nil
}

// Format renders the resolved config as a human-readable listing with source
// annotations, as required by the `config` subcommand.
func (c Config) Format() string {
	var b strings.Builder
	if c.Path != "" {
		fmt.Fprintf(&b, "# config file: %s\n", c.Path)
	}
	fmt.Fprintf(&b, "vault: %s (from %s)\n", c.Vault, source(c, "vault"))
	fmt.Fprintf(&b, "engine: %s (from %s)\n", c.Engine, source(c, "engine"))
	fmt.Fprintf(&b, "layout: %s (from %s)\n", c.Layout, source(c, "layout"))
	fmt.Fprintf(&b, "focus_follows_input: %t (from %s)\n", c.FocusFollowsInput, source(c, "focus_follows_input"))
	fmt.Fprintf(&b, "pane_stats: %t (from %s)\n", c.PaneStats, source(c, "pane_stats"))
	fmt.Fprintf(&b, "terminal.command: %s (from %s)\n", terminalCommandDisplay(c), source(c, "terminal.command"))
	fmt.Fprintf(&b, "terminal.args: %v (from %s)\n", c.Terminal.Args, source(c, "terminal.args"))
	fmt.Fprintf(&b, "spawn.command: %s (from %s)\n", c.Spawn.Command, source(c, "spawn.command"))
	fmt.Fprintf(&b, "spawn.args: %v (from %s)\n", c.Spawn.Args, source(c, "spawn.args"))
	fmt.Fprintf(&b, "tmux.enabled: %t (from %s)\n", c.Tmux.Enabled, source(c, "tmux.enabled"))
	fmt.Fprintf(&b, "tmux.session_name: %s (from %s)\n", c.Tmux.SessionName, source(c, "tmux.session_name"))
	fmt.Fprintf(&b, "tmux.tui_width: %d (from %s)\n", c.Tmux.TUIWidth, source(c, "tmux.tui_width"))
	fmt.Fprintf(&b, "tmux.pane_width: %d (from %s)\n", c.Tmux.PaneWidth, source(c, "tmux.pane_width"))
	fmt.Fprintf(&b, "tmux.min_pane_width: %d (from %s)\n", c.Tmux.MinPaneWidth, source(c, "tmux.min_pane_width"))
	fmt.Fprintf(&b, "progress.show: %t (from %s)\n", c.Progress.Show, source(c, "progress.show"))
	fmt.Fprintf(&b, "progress.poll_ci: %t (from %s)\n", c.Progress.PollCI, source(c, "progress.poll_ci"))
	return b.String()
}

func terminalCommandDisplay(c Config) string {
	if c.Terminal.Command == "" {
		return "(auto-detect)"
	}
	return c.Terminal.Command
}

// validLayout reports whether name is one of the recognised native layouts.
func validLayout(name string) bool {
	switch name {
	case LayoutColumns, LayoutStack, LayoutTabs, LayoutResponsive:
		return true
	default:
		return false
	}
}

// isTruthy parses a boolean-ish env var value. Anything other than the usual
// false spellings is treated as true, so SQUASH_FOCUS_FOLLOWS_INPUT=1/yes/on
// all enable it.
func isTruthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "0", "false", "no", "off":
		return false
	default:
		return true
	}
}

func source(c Config, key string) Source {
	if c.Sources == nil {
		return SourceDefault
	}
	if s, ok := c.Sources[key]; ok {
		return s
	}
	return SourceDefault
}

// ExpandHome replaces a leading ~ in path with the user's home directory.
// It is a thin wrapper to keep config self-contained; internal/vault has
// the same helper for its own use.
func ExpandHome(path string) string {
	if !strings.HasPrefix(path, "~") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, path[1:])
}
