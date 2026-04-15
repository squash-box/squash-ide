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

// Config is the resolved squash-ide configuration.
type Config struct {
	Vault    string   `yaml:"vault"`
	Terminal Terminal `yaml:"terminal"`
	Spawn    Spawn    `yaml:"spawn"`

	// Sources records the provenance of each resolved field.
	// Keys: "vault", "terminal.command", "terminal.args",
	// "spawn.command", "spawn.args".
	Sources map[string]Source `yaml:"-"`

	// Path is the resolved path to the config file, if one was loaded.
	Path string `yaml:"-"`
}

// Overrides bundles CLI flag values that should win over env and file.
// Empty fields are treated as "not provided" and skipped.
type Overrides struct {
	Vault    string
	Terminal string
	SpawnCmd string

	// ConfigPath, when non-empty, overrides the default ~/.config/squash-ide/config.yaml
	// lookup. Useful for tests.
	ConfigPath string
}

// Defaults returns the built-in defaults — used when neither file, env, nor
// flag provides a value.
func Defaults() Config {
	return Config{
		Vault: "~/GIT/agentic/tasks/personal/",
		Terminal: Terminal{
			// Empty = auto-detect (preserves T-007's terminal detection).
			Command: "",
			Args:    []string{"--working-directory={cwd}", "--", "bash", "-c", "{exec}"},
		},
		Spawn: Spawn{
			Command: "claude",
			Args:    []string{"/implement {task_id}"},
		},
		Sources: map[string]Source{
			"vault":            SourceDefault,
			"terminal.command": SourceDefault,
			"terminal.args":    SourceDefault,
			"spawn.command":    SourceDefault,
			"spawn.args":       SourceDefault,
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

	return cfg, nil
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

	var fileCfg Config
	if err := yaml.Unmarshal(data, &fileCfg); err != nil {
		return fmt.Errorf("parsing config %s: %w", path, err)
	}

	cfg.Path = path
	if fileCfg.Vault != "" {
		cfg.Vault = fileCfg.Vault
		cfg.Sources["vault"] = SourceFile
	}
	if fileCfg.Terminal.Command != "" {
		cfg.Terminal.Command = fileCfg.Terminal.Command
		cfg.Sources["terminal.command"] = SourceFile
	}
	if fileCfg.Terminal.Args != nil {
		cfg.Terminal.Args = fileCfg.Terminal.Args
		cfg.Sources["terminal.args"] = SourceFile
	}
	if fileCfg.Spawn.Command != "" {
		cfg.Spawn.Command = fileCfg.Spawn.Command
		cfg.Sources["spawn.command"] = SourceFile
	}
	if fileCfg.Spawn.Args != nil {
		cfg.Spawn.Args = fileCfg.Spawn.Args
		cfg.Sources["spawn.args"] = SourceFile
	}
	return nil
}

// applyEnv overlays SQUASH_* env vars onto cfg.
func applyEnv(cfg *Config) {
	if v := os.Getenv("SQUASH_VAULT"); v != "" {
		cfg.Vault = v
		cfg.Sources["vault"] = SourceEnv
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
	if ov.Terminal != "" {
		cfg.Terminal.Command = ov.Terminal
		cfg.Sources["terminal.command"] = SourceFlag
	}
	if ov.SpawnCmd != "" {
		cfg.Spawn.Command = ov.SpawnCmd
		cfg.Sources["spawn.command"] = SourceFlag
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
	fmt.Fprintf(&b, "terminal.command: %s (from %s)\n", terminalCommandDisplay(c), source(c, "terminal.command"))
	fmt.Fprintf(&b, "terminal.args: %v (from %s)\n", c.Terminal.Args, source(c, "terminal.args"))
	fmt.Fprintf(&b, "spawn.command: %s (from %s)\n", c.Spawn.Command, source(c, "spawn.command"))
	fmt.Fprintf(&b, "spawn.args: %v (from %s)\n", c.Spawn.Args, source(c, "spawn.args"))
	return b.String()
}

func terminalCommandDisplay(c Config) string {
	if c.Terminal.Command == "" {
		return "(auto-detect)"
	}
	return c.Terminal.Command
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
