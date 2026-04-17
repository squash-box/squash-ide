package dispatch

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteClaudeSettings_WritesParseableJSON(t *testing.T) {
	wt := t.TempDir()
	if err := writeClaudeSettings(wt, "T-042", "/usr/local/bin/squash-ide"); err != nil {
		t.Fatalf("writeClaudeSettings: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(wt, ".claude", "settings.json"))
	if err != nil {
		t.Fatalf("read settings.json: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, data)
	}
	if _, ok := out["hooks"]; !ok {
		t.Fatalf("missing top-level hooks key: %v", out)
	}
}

func TestWriteClaudeSettings_WiresThreeMatchers(t *testing.T) {
	wt := t.TempDir()
	if err := writeClaudeSettings(wt, "T-001", "/bin/sq"); err != nil {
		t.Fatalf("writeClaudeSettings: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(wt, ".claude", "settings.json"))
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	hooks := out["hooks"].(map[string]any)
	for _, event := range []string{"Notification", "PostToolUse", "Stop"} {
		if _, ok := hooks[event]; !ok {
			t.Errorf("missing %s hook", event)
		}
	}
}

// Each event must drive a specific state transition. Verify by extracting
// the generated command for each matcher and asserting the state token.
func TestWriteClaudeSettings_MapsEventsToStates(t *testing.T) {
	wt := t.TempDir()
	if err := writeClaudeSettings(wt, "T-001", "/bin/sq"); err != nil {
		t.Fatalf("writeClaudeSettings: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(wt, ".claude", "settings.json"))

	want := map[string]string{
		"Notification": "input_required",
		"PostToolUse":  "working",
		"Stop":         "idle",
	}
	for event, state := range want {
		cmd := extractHookCommand(t, data, event)
		if !strings.Contains(cmd, " status "+state+" ") {
			t.Errorf("%s hook command missing 'status %s': %q", event, state, cmd)
		}
	}
}

// The task ID must be baked into every hook command so the subprocess does
// not depend on SQUASH_TASK_ID leaking into Claude Code's env — the .mcp.json
// env block only scopes that variable to the MCP server subprocess.
func TestWriteClaudeSettings_BakesTaskIDIntoCommand(t *testing.T) {
	wt := t.TempDir()
	if err := writeClaudeSettings(wt, "T-077", "/bin/sq"); err != nil {
		t.Fatalf("writeClaudeSettings: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(wt, ".claude", "settings.json"))

	for _, event := range []string{"Notification", "PostToolUse", "Stop"} {
		cmd := extractHookCommand(t, data, event)
		if !strings.Contains(cmd, "SQUASH_TASK_ID=T-077") {
			t.Errorf("%s hook missing task-id env prefix: %q", event, cmd)
		}
	}
}

func TestWriteClaudeSettings_UsesProvidedBinaryPath(t *testing.T) {
	wt := t.TempDir()
	if err := writeClaudeSettings(wt, "T-001", "/opt/custom/squash-ide"); err != nil {
		t.Fatalf("writeClaudeSettings: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(wt, ".claude", "settings.json"))
	cmd := extractHookCommand(t, data, "Notification")
	if !strings.Contains(cmd, "/opt/custom/squash-ide") {
		t.Errorf("hook command does not reference provided binary path: %q", cmd)
	}
}

func TestWriteClaudeSettings_CreatesDirIfAbsent(t *testing.T) {
	wt := t.TempDir()
	// Deliberately no pre-created .claude dir.
	if err := writeClaudeSettings(wt, "T-001", "/bin/sq"); err != nil {
		t.Fatalf("writeClaudeSettings: %v", err)
	}
	info, err := os.Stat(filepath.Join(wt, ".claude"))
	if err != nil {
		t.Fatalf("stat .claude: %v", err)
	}
	if !info.IsDir() {
		t.Error(".claude is not a directory")
	}
}

func TestWriteClaudeSettings_Idempotent(t *testing.T) {
	wt := t.TempDir()
	if err := writeClaudeSettings(wt, "T-001", "/bin/sq"); err != nil {
		t.Fatal(err)
	}
	first, _ := os.ReadFile(filepath.Join(wt, ".claude", "settings.json"))
	if err := writeClaudeSettings(wt, "T-001", "/bin/sq"); err != nil {
		t.Fatal(err)
	}
	second, _ := os.ReadFile(filepath.Join(wt, ".claude", "settings.json"))
	if string(first) != string(second) {
		t.Error("repeated writes should produce identical content")
	}
}

func TestWriteClaudeSettings_TargetNotWritable(t *testing.T) {
	// Non-existent parent path forces MkdirAll to error.
	err := writeClaudeSettings("/definitely/not/a/real/dir/nope", "T-001", "/bin/sq")
	if err == nil {
		t.Fatal("expected err writing to unreachable path")
	}
}

func TestShellEscape(t *testing.T) {
	cases := map[string]string{
		"plain":       "plain",
		"":            "''",
		"has space":   "'has space'",
		`has 'quote'`: `'has '\''quote'\'''`,
		"/abs/path":   "/abs/path",
		"T-001":       "T-001",
	}
	for in, want := range cases {
		if got := shellEscape(in); got != want {
			t.Errorf("shellEscape(%q) = %q, want %q", in, got, want)
		}
	}
}

// extractHookCommand fishes the first hook command for `event` out of the
// marshalled settings JSON. Keeps individual tests terse.
func extractHookCommand(t *testing.T, data []byte, event string) string {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	hooks := out["hooks"].(map[string]any)
	entries, ok := hooks[event].([]any)
	if !ok || len(entries) == 0 {
		t.Fatalf("no entries for %s", event)
	}
	first := entries[0].(map[string]any)
	inner, ok := first["hooks"].([]any)
	if !ok || len(inner) == 0 {
		t.Fatalf("no hook specs for %s", event)
	}
	spec := inner[0].(map[string]any)
	cmd, ok := spec["command"].(string)
	if !ok {
		t.Fatalf("no command for %s", event)
	}
	return cmd
}
