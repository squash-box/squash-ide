package dispatch

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// writeClaudeSettings writes .claude/settings.json into the worktree so
// Claude Code fires shell hooks that flip the squash-ide status file at the
// moments the MCP tool cannot reach (notably, tool-permission dialogs that
// pause the model mid-turn).
//
// Hooks wired:
//   - Notification → status input_required (Claude is awaiting user input)
//   - PostToolUse  → status working        (user consented, tool ran)
//   - Stop         → status idle           (Claude finished the turn)
//
// statusBinPath is the path to the squash-ide binary that owns the `status`
// subcommand. The task ID is baked into each hook command string so the
// subprocess doesn't depend on Claude Code inheriting SQUASH_TASK_ID (the
// .mcp.json env block only scopes that var to the MCP server subprocess).
func writeClaudeSettings(worktreePath, taskID, statusBinPath string) error {
	qBin := shellEscape(statusBinPath)
	qID := shellEscape(taskID)
	cmd := func(state, message string) string {
		return fmt.Sprintf("SQUASH_TASK_ID=%s %s status %s %s",
			qID, qBin, state, shellEscape(message))
	}

	settings := map[string]any{
		"hooks": map[string]any{
			"Notification": []any{
				map[string]any{
					"hooks": []any{
						map[string]any{
							"type":    "command",
							"command": cmd("input_required", "Awaiting user input"),
						},
					},
				},
			},
			"PostToolUse": []any{
				map[string]any{
					"matcher": "",
					"hooks": []any{
						map[string]any{
							"type":    "command",
							"command": cmd("working", "Working"),
						},
					},
				},
			},
			"Stop": []any{
				map[string]any{
					"hooks": []any{
						map[string]any{
							"type":    "command",
							"command": cmd("idle", "Turn complete"),
						},
					},
				},
			},
		},
	}

	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("marshalling claude settings: %w", err)
	}

	dir := filepath.Join(worktreePath, ".claude")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}
	target := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(target, append(data, '\n'), 0644); err != nil {
		return fmt.Errorf("writing %s: %w", target, err)
	}
	return nil
}

// shellEscape wraps a string in single quotes for safe inclusion in a shell
// command. Any embedded single quote is split out with the standard
// `'\”` idiom. Simple, no dependency on quoting behaviour of callers.
func shellEscape(s string) string {
	if s == "" {
		return "''"
	}
	safe := true
	for _, r := range s {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '/' || r == '_' || r == '-' || r == '.' || r == '=') {
			safe = false
			break
		}
	}
	if safe {
		return s
	}
	out := "'"
	for _, r := range s {
		if r == '\'' {
			out += `'\''`
		} else {
			out += string(r)
		}
	}
	return out + "'"
}
