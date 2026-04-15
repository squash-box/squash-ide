package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// writeConfig writes a YAML config fixture to a fresh temp file and returns
// its path.
func writeConfig(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// clearSquashEnv unsets every SQUASH_* env var for the duration of the test.
func clearSquashEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"SQUASH_VAULT", "SQUASH_TERMINAL", "SQUASH_SPAWN_CMD"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
}

func TestDefaults(t *testing.T) {
	d := Defaults()
	if d.Vault == "" {
		t.Error("default vault should be non-empty")
	}
	if d.Spawn.Command == "" {
		t.Error("default spawn command should be non-empty")
	}
	if d.Terminal.Command != "" {
		t.Errorf("default terminal.command should be empty (auto-detect), got %q", d.Terminal.Command)
	}
	for _, key := range []string{"vault", "terminal.command", "terminal.args", "spawn.command", "spawn.args"} {
		if got, ok := d.Sources[key]; !ok || got != SourceDefault {
			t.Errorf("Sources[%q] = %q, want %q", key, got, SourceDefault)
		}
	}
}

func TestLoad_NoFileNoEnvNoFlags(t *testing.T) {
	clearSquashEnv(t)
	cfg, err := Load(Overrides{ConfigPath: filepath.Join(t.TempDir(), "missing.yaml")})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	d := Defaults()
	if cfg.Vault != d.Vault {
		t.Errorf("vault = %q, want default %q", cfg.Vault, d.Vault)
	}
	if cfg.Sources["vault"] != SourceDefault {
		t.Errorf("source = %q, want default", cfg.Sources["vault"])
	}
}

func TestLoad_FileOnly(t *testing.T) {
	clearSquashEnv(t)
	path := writeConfig(t, `vault: /tmp/my-vault
terminal:
  command: wezterm
  args: ["start", "--cwd={cwd}", "--", "{exec}"]
spawn:
  command: aider
  args: ["--task", "{task_id}"]
`)
	cfg, err := Load(Overrides{ConfigPath: path})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Vault != "/tmp/my-vault" {
		t.Errorf("vault = %q, want /tmp/my-vault", cfg.Vault)
	}
	if cfg.Sources["vault"] != SourceFile {
		t.Errorf("vault source = %q, want file", cfg.Sources["vault"])
	}
	if cfg.Terminal.Command != "wezterm" {
		t.Errorf("terminal.command = %q, want wezterm", cfg.Terminal.Command)
	}
	if cfg.Sources["terminal.command"] != SourceFile {
		t.Errorf("terminal.command source = %q, want file", cfg.Sources["terminal.command"])
	}
	if cfg.Spawn.Command != "aider" {
		t.Errorf("spawn.command = %q, want aider", cfg.Spawn.Command)
	}
	if cfg.Sources["spawn.command"] != SourceFile {
		t.Errorf("spawn.command source = %q, want file", cfg.Sources["spawn.command"])
	}
	if cfg.Path != path {
		t.Errorf("cfg.Path = %q, want %q", cfg.Path, path)
	}
}

func TestLoad_EnvOverridesFile(t *testing.T) {
	clearSquashEnv(t)
	path := writeConfig(t, `vault: /from/file
terminal:
  command: file-term
spawn:
  command: file-cmd
`)
	t.Setenv("SQUASH_VAULT", "/from/env")
	t.Setenv("SQUASH_TERMINAL", "env-term")
	t.Setenv("SQUASH_SPAWN_CMD", "env-cmd")

	cfg, err := Load(Overrides{ConfigPath: path})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Vault != "/from/env" {
		t.Errorf("vault = %q, want env value", cfg.Vault)
	}
	if cfg.Sources["vault"] != SourceEnv {
		t.Errorf("vault source = %q, want env", cfg.Sources["vault"])
	}
	if cfg.Terminal.Command != "env-term" {
		t.Errorf("terminal.command = %q, want env value", cfg.Terminal.Command)
	}
	if cfg.Sources["terminal.command"] != SourceEnv {
		t.Errorf("terminal.command source = %q, want env", cfg.Sources["terminal.command"])
	}
	if cfg.Spawn.Command != "env-cmd" {
		t.Errorf("spawn.command = %q, want env value", cfg.Spawn.Command)
	}
}

func TestLoad_FlagOverridesAll(t *testing.T) {
	clearSquashEnv(t)
	path := writeConfig(t, `vault: /from/file
terminal:
  command: file-term
spawn:
  command: file-cmd
`)
	t.Setenv("SQUASH_VAULT", "/from/env")
	t.Setenv("SQUASH_TERMINAL", "env-term")
	t.Setenv("SQUASH_SPAWN_CMD", "env-cmd")

	cfg, err := Load(Overrides{
		ConfigPath: path,
		Vault:      "/from/flag",
		Terminal:   "flag-term",
		SpawnCmd:   "flag-cmd",
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Vault != "/from/flag" {
		t.Errorf("vault = %q, want flag value", cfg.Vault)
	}
	if cfg.Sources["vault"] != SourceFlag {
		t.Errorf("vault source = %q, want flag", cfg.Sources["vault"])
	}
	if cfg.Terminal.Command != "flag-term" {
		t.Errorf("terminal.command = %q, want flag value", cfg.Terminal.Command)
	}
	if cfg.Sources["terminal.command"] != SourceFlag {
		t.Errorf("terminal.command source = %q, want flag", cfg.Sources["terminal.command"])
	}
	if cfg.Spawn.Command != "flag-cmd" {
		t.Errorf("spawn.command = %q, want flag value", cfg.Spawn.Command)
	}
	if cfg.Sources["spawn.command"] != SourceFlag {
		t.Errorf("spawn.command source = %q, want flag", cfg.Sources["spawn.command"])
	}
}

func TestLoad_PartialFile(t *testing.T) {
	// File only overrides terminal; vault and spawn should stay default.
	clearSquashEnv(t)
	path := writeConfig(t, `terminal:
  command: alacritty
`)
	cfg, err := Load(Overrides{ConfigPath: path})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	d := Defaults()
	if cfg.Vault != d.Vault {
		t.Errorf("vault = %q, want default", cfg.Vault)
	}
	if cfg.Sources["vault"] != SourceDefault {
		t.Errorf("vault source = %q, want default", cfg.Sources["vault"])
	}
	if cfg.Terminal.Command != "alacritty" {
		t.Errorf("terminal.command = %q, want alacritty", cfg.Terminal.Command)
	}
	if cfg.Sources["terminal.command"] != SourceFile {
		t.Errorf("terminal.command source = %q, want file", cfg.Sources["terminal.command"])
	}
	if cfg.Spawn.Command != d.Spawn.Command {
		t.Errorf("spawn.command = %q, want default", cfg.Spawn.Command)
	}
	if cfg.Sources["spawn.command"] != SourceDefault {
		t.Errorf("spawn.command source = %q, want default", cfg.Sources["spawn.command"])
	}
}

func TestLoad_MissingFileIsFine(t *testing.T) {
	clearSquashEnv(t)
	// Point at a path that definitely does not exist.
	missing := filepath.Join(t.TempDir(), "does-not-exist.yaml")
	cfg, err := Load(Overrides{ConfigPath: missing})
	if err != nil {
		t.Fatalf("Load with missing file should not error, got: %v", err)
	}
	if cfg.Vault != Defaults().Vault {
		t.Error("expected defaults when file is missing")
	}
	if cfg.Path != "" {
		t.Errorf("cfg.Path = %q, want empty when file missing", cfg.Path)
	}
}

func TestLoad_InvalidYAML(t *testing.T) {
	clearSquashEnv(t)
	path := writeConfig(t, "vault: [unterminated\n")
	_, err := Load(Overrides{ConfigPath: path})
	if err == nil {
		t.Fatal("expected error for invalid YAML, got nil")
	}
	if !strings.Contains(err.Error(), "parsing config") {
		t.Errorf("error should mention parsing, got: %v", err)
	}
}

func TestLoad_EnvSetButEmpty(t *testing.T) {
	// An empty env var should be treated as unset, not as an override to "".
	clearSquashEnv(t)
	t.Setenv("SQUASH_VAULT", "")
	cfg, err := Load(Overrides{ConfigPath: filepath.Join(t.TempDir(), "none.yaml")})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Sources["vault"] != SourceDefault {
		t.Errorf("empty env should not override; source = %q", cfg.Sources["vault"])
	}
}

func TestValidate_OK(t *testing.T) {
	dir := t.TempDir()
	cfg := Defaults()
	cfg.Vault = dir
	// Empty terminal.command = auto-detect = always OK for validation.
	if err := Validate(cfg); err != nil {
		t.Errorf("Validate: %v", err)
	}
}

func TestValidate_MissingVault(t *testing.T) {
	cfg := Defaults()
	cfg.Vault = "/nope/does/not/exist/anywhere"
	err := Validate(cfg)
	if err == nil {
		t.Fatal("expected error for missing vault")
	}
	if !strings.Contains(err.Error(), "vault") {
		t.Errorf("error should mention vault, got: %v", err)
	}
}

func TestValidate_TerminalNotOnPath(t *testing.T) {
	dir := t.TempDir()
	cfg := Defaults()
	cfg.Vault = dir
	cfg.Terminal.Command = "definitely-not-a-real-terminal-xyz123"
	err := Validate(cfg)
	if err == nil {
		t.Fatal("expected error for missing terminal binary")
	}
	if !strings.Contains(err.Error(), "PATH") {
		t.Errorf("error should mention PATH, got: %v", err)
	}
	if !strings.Contains(err.Error(), "definitely-not-a-real-terminal-xyz123") {
		t.Errorf("error should mention the binary name, got: %v", err)
	}
}

func TestFormat_IncludesSources(t *testing.T) {
	cfg := Defaults()
	cfg.Vault = "/x"
	cfg.Sources["vault"] = SourceFlag
	out := cfg.Format()
	if !strings.Contains(out, "/x") {
		t.Errorf("format should contain value, got: %s", out)
	}
	if !strings.Contains(out, "(from flag)") {
		t.Errorf("format should annotate source, got: %s", out)
	}
	if !strings.Contains(out, "(auto-detect)") {
		t.Errorf("format should show auto-detect for empty terminal, got: %s", out)
	}
}

func TestExpand_Basic(t *testing.T) {
	vars := map[string]string{
		"cwd":      "/w/path",
		"task_id":  "T-123",
		"worktree": "/w/path",
		"repo":     "/r",
		"branch":   "feat/T-123-foo",
	}
	cases := []struct {
		in, want string
	}{
		{"--working-directory={cwd}", "--working-directory=/w/path"},
		{"{task_id}", "T-123"},
		{"feat/{branch}-{task_id}", "feat/feat/T-123-foo-T-123"},
		{"no placeholders", "no placeholders"},
		{"{unknown}", "{unknown}"}, // unknown left as-is
		{"{cwd} and {repo}", "/w/path and /r"},
	}
	for _, c := range cases {
		got := Expand(c.in, vars)
		if got != c.want {
			t.Errorf("Expand(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestExpandAll(t *testing.T) {
	vars := map[string]string{"cwd": "/w", "exec": "claude"}
	in := []string{"-d", "{cwd}", "--", "bash", "-c", "{exec}"}
	want := []string{"-d", "/w", "--", "bash", "-c", "claude"}
	got := ExpandAll(in, vars)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ExpandAll = %v, want %v", got, want)
	}
	// Ensure the input slice was not mutated.
	if in[1] != "{cwd}" {
		t.Error("ExpandAll mutated input slice")
	}
}

func TestBuildExec_Quoting(t *testing.T) {
	cases := []struct {
		name    string
		command string
		args    []string
		want    string
	}{
		{"no args", "claude", nil, "claude"},
		{"single plain arg", "claude", []string{"implement"}, "claude implement"},
		{"arg with space", "claude", []string{"/implement T-009"}, `claude '/implement T-009'`},
		{"arg with single quote", "echo", []string{"it's"}, `echo 'it'\''s'`},
		{"empty arg", "cmd", []string{""}, "cmd ''"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := BuildExec(c.command, c.args)
			if got != c.want {
				t.Errorf("BuildExec = %q, want %q", got, c.want)
			}
		})
	}
}

func TestTmuxDefaults(t *testing.T) {
	d := Defaults()
	if !d.Tmux.Enabled {
		t.Error("tmux.enabled default should be true")
	}
	if d.Tmux.SessionName != "squash-ide" {
		t.Errorf("tmux.session_name default = %q, want squash-ide", d.Tmux.SessionName)
	}
	if d.Tmux.TUIWidth != 60 {
		t.Errorf("tmux.tui_width default = %d, want 60", d.Tmux.TUIWidth)
	}
	if d.Tmux.MinPaneWidth != 80 {
		t.Errorf("tmux.min_pane_width default = %d, want 80", d.Tmux.MinPaneWidth)
	}
	for _, key := range []string{"tmux.enabled", "tmux.session_name", "tmux.tui_width", "tmux.min_pane_width"} {
		if got := d.Sources[key]; got != SourceDefault {
			t.Errorf("Sources[%q] = %q, want default", key, got)
		}
	}
}

func TestLoad_TmuxFromFile(t *testing.T) {
	clearSquashEnv(t)
	path := writeConfig(t, `tmux:
  enabled: false
  session_name: my-session
  tui_width: 50
  min_pane_width: 100
`)
	cfg, err := Load(Overrides{ConfigPath: path})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Tmux.Enabled {
		t.Error("tmux.enabled = true, want false (from file)")
	}
	if cfg.Sources["tmux.enabled"] != SourceFile {
		t.Errorf("tmux.enabled source = %q, want file", cfg.Sources["tmux.enabled"])
	}
	if cfg.Tmux.SessionName != "my-session" {
		t.Errorf("tmux.session_name = %q, want my-session", cfg.Tmux.SessionName)
	}
	if cfg.Tmux.TUIWidth != 50 {
		t.Errorf("tmux.tui_width = %d, want 50", cfg.Tmux.TUIWidth)
	}
	if cfg.Tmux.MinPaneWidth != 100 {
		t.Errorf("tmux.min_pane_width = %d, want 100", cfg.Tmux.MinPaneWidth)
	}
}

func TestLoad_TmuxFlagOverrides(t *testing.T) {
	clearSquashEnv(t)
	path := writeConfig(t, `tmux:
  tui_width: 50
  min_pane_width: 100
`)
	cfg, err := Load(Overrides{
		ConfigPath:   path,
		NoTmux:       true,
		TUIWidth:     40,
		MinPaneWidth: 120,
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Tmux.Enabled {
		t.Error("--no-tmux should set Enabled=false")
	}
	if cfg.Sources["tmux.enabled"] != SourceFlag {
		t.Errorf("tmux.enabled source = %q, want flag", cfg.Sources["tmux.enabled"])
	}
	if cfg.Tmux.TUIWidth != 40 {
		t.Errorf("tmux.tui_width = %d, want 40 (flag)", cfg.Tmux.TUIWidth)
	}
	if cfg.Sources["tmux.tui_width"] != SourceFlag {
		t.Errorf("tmux.tui_width source = %q, want flag", cfg.Sources["tmux.tui_width"])
	}
	if cfg.Tmux.MinPaneWidth != 120 {
		t.Errorf("tmux.min_pane_width = %d, want 120 (flag)", cfg.Tmux.MinPaneWidth)
	}
}

func TestLoad_TmuxOmitInFileKeepsDefault(t *testing.T) {
	clearSquashEnv(t)
	// File mentions other things but not tmux at all.
	path := writeConfig(t, `vault: /tmp/v
spawn:
  command: aider
`)
	cfg, err := Load(Overrides{ConfigPath: path})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	d := Defaults()
	if cfg.Tmux.Enabled != d.Tmux.Enabled {
		t.Errorf("tmux.enabled = %t, want default %t", cfg.Tmux.Enabled, d.Tmux.Enabled)
	}
	if cfg.Sources["tmux.enabled"] != SourceDefault {
		t.Errorf("source = %q, want default", cfg.Sources["tmux.enabled"])
	}
	if cfg.Tmux.TUIWidth != d.Tmux.TUIWidth {
		t.Errorf("tmux.tui_width = %d, want default %d", cfg.Tmux.TUIWidth, d.Tmux.TUIWidth)
	}
}

func TestPrecedence_PerFieldIndependently(t *testing.T) {
	// Vault from flag, terminal from env, spawn from file, terminal.args default.
	clearSquashEnv(t)
	path := writeConfig(t, `spawn:
  command: file-spawn
`)
	t.Setenv("SQUASH_TERMINAL", "env-term")

	cfg, err := Load(Overrides{
		ConfigPath: path,
		Vault:      "/flag-vault",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]Source{
		"vault":            SourceFlag,
		"terminal.command": SourceEnv,
		"terminal.args":    SourceDefault,
		"spawn.command":    SourceFile,
		"spawn.args":       SourceDefault,
	}
	for k, w := range want {
		if cfg.Sources[k] != w {
			t.Errorf("source[%s] = %q, want %q", k, cfg.Sources[k], w)
		}
	}
}
