// Package status provides file-based IPC for task status reporting.
//
// The MCP server writes per-task status files to /tmp/squash-ide/status/;
// the TUI polls them to update badges and pane borders in real time.
package status

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Dir is the directory where status files are written.
const Dir = "/tmp/squash-ide/status"

// StaleDuration is how long a status file is considered valid. Files older
// than this are ignored by ReadAll (the Claude session likely exited).
const StaleDuration = 5 * time.Minute

// File represents a single task's runtime status on disk.
type File struct {
	TaskID  string `json:"task_id"`
	State   string `json:"state"`   // idle, working, input_required, testing
	Message string `json:"message"` // brief human-readable description
	Updated int64  `json:"updated"` // unix timestamp
}

// Write atomically writes a status file for the given task.
func Write(taskID, state, message string) error {
	if err := os.MkdirAll(Dir, 0755); err != nil {
		return err
	}

	f := File{
		TaskID:  taskID,
		State:   state,
		Message: message,
		Updated: time.Now().Unix(),
	}

	data, err := json.Marshal(f)
	if err != nil {
		return err
	}

	// Write to temp file then rename for atomicity.
	tmp := filepath.Join(Dir, taskID+".tmp")
	target := filepath.Join(Dir, taskID+".json")

	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, target)
}

// ReadAll reads all non-stale status files and returns them keyed by task ID.
func ReadAll() (map[string]File, error) {
	entries, err := filepath.Glob(filepath.Join(Dir, "T-*.json"))
	if err != nil {
		return nil, err
	}

	cutoff := time.Now().Add(-StaleDuration).Unix()
	result := make(map[string]File, len(entries))

	for _, path := range entries {
		data, err := os.ReadFile(path)
		if err != nil {
			continue // file may have been removed between glob and read
		}
		var f File
		if err := json.Unmarshal(data, &f); err != nil {
			continue
		}
		if f.Updated < cutoff {
			continue // stale
		}
		// Derive task ID from filename if not set in JSON.
		if f.TaskID == "" {
			base := filepath.Base(path)
			f.TaskID = strings.TrimSuffix(base, ".json")
		}
		result[f.TaskID] = f
	}
	return result, nil
}

// Remove deletes the status file for a task. It is not an error if the
// file does not exist.
func Remove(taskID string) error {
	err := os.Remove(filepath.Join(Dir, taskID+".json"))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
