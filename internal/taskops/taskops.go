package taskops

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/squashbox/squash-ide/internal/task"
)

// MoveToActive moves a task file from its current status directory to active/,
// updates the frontmatter status to "active" and adds a "started" field.
// Returns the new file path.
func MoveToActive(vaultRoot string, t task.Task) (string, error) {
	srcDir := filepath.Join(vaultRoot, "tasks", t.Status)
	dstDir := filepath.Join(vaultRoot, "tasks", "active")

	// Find the task file in the source directory
	srcPath, err := findTaskFile(srcDir, t.ID)
	if err != nil {
		return "", err
	}

	data, err := os.ReadFile(srcPath)
	if err != nil {
		return "", fmt.Errorf("reading task file: %w", err)
	}

	// Update frontmatter: status → active, add started date
	content := string(data)
	content = replaceFrontmatterField(content, "status", "active")
	content = addFrontmatterField(content, "started", time.Now().Format("2006-01-02"))

	dstPath := filepath.Join(dstDir, filepath.Base(srcPath))
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		return "", fmt.Errorf("creating active dir: %w", err)
	}
	if err := os.WriteFile(dstPath, []byte(content), 0o644); err != nil {
		return "", fmt.Errorf("writing task file: %w", err)
	}
	if err := os.Remove(srcPath); err != nil {
		return "", fmt.Errorf("removing old task file: %w", err)
	}

	return dstPath, nil
}

// UpdateBoard moves a task row from the Backlog table to the Active table
// in tasks/board.md.
func UpdateBoard(vaultRoot string, t task.Task) error {
	boardPath := filepath.Join(vaultRoot, "tasks", "board.md")
	data, err := os.ReadFile(boardPath)
	if err != nil {
		return fmt.Errorf("reading board: %w", err)
	}

	content := string(data)

	// Find and remove the task row from Backlog
	taskRow := fmt.Sprintf("| [[%s]] |", t.ID)
	var removedRow string
	lines := strings.Split(content, "\n")
	var newLines []string
	for _, line := range lines {
		if strings.Contains(line, taskRow) {
			removedRow = line
			continue
		}
		newLines = append(newLines, line)
	}
	if removedRow == "" {
		return fmt.Errorf("task %s not found in board", t.ID)
	}
	content = strings.Join(newLines, "\n")

	// Insert into Active section
	content = insertActiveRow(content, removedRow)

	// Update last_updated
	content = replaceFrontmatterField(content, "last_updated", time.Now().Format("2006-01-02"))

	return os.WriteFile(boardPath, []byte(content), 0o644)
}

// AppendLog adds a spawn entry to wiki/log.md.
func AppendLog(vaultRoot string, t task.Task, branch, worktreePath string) error {
	logPath := filepath.Join(vaultRoot, "wiki", "log.md")
	data, err := os.ReadFile(logPath)
	if err != nil {
		return fmt.Errorf("reading log: %w", err)
	}

	today := time.Now().Format("2006-01-02")
	entry := fmt.Sprintf("## [%s] spawn | %s %s\nBranch: %s | Worktree: %s\n",
		today, t.ID, t.Title, branch, worktreePath)

	content := string(data)
	// Insert after the header block (after the first blank line following frontmatter)
	idx := strings.Index(content, "\n\n## [")
	if idx >= 0 {
		// Insert before the first existing entry
		content = content[:idx+1] + "\n" + entry + content[idx+1:]
	} else {
		// No existing entries — append after the header
		content = content + "\n" + entry
	}

	return os.WriteFile(logPath, []byte(content), 0o644)
}

// findTaskFile locates the .md file for a task ID in a directory.
func findTaskFile(dir, taskID string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("reading directory %s: %w", dir, err)
	}
	prefix := strings.ToLower(taskID)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := strings.ToLower(entry.Name())
		if strings.HasPrefix(name, prefix) && strings.HasSuffix(name, ".md") {
			return filepath.Join(dir, entry.Name()), nil
		}
	}
	return "", fmt.Errorf("task file for %s not found in %s", taskID, dir)
}

// replaceFrontmatterField replaces a YAML field value in the frontmatter.
func replaceFrontmatterField(content, field, value string) string {
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, field+":") {
			lines[i] = field + ": " + value
			break
		}
	}
	return strings.Join(lines, "\n")
}

// addFrontmatterField adds a new field before the closing --- delimiter.
func addFrontmatterField(content, field, value string) string {
	lines := strings.Split(content, "\n")
	// Find the closing --- (second occurrence)
	count := 0
	for i, line := range lines {
		if strings.TrimSpace(line) == "---" {
			count++
			if count == 2 {
				newLine := field + ": " + value
				// Insert before the closing ---
				result := make([]string, 0, len(lines)+1)
				result = append(result, lines[:i]...)
				result = append(result, newLine)
				result = append(result, lines[i:]...)
				return strings.Join(result, "\n")
			}
		}
	}
	return content
}

// insertActiveRow inserts a task row into the Active section of the board.
func insertActiveRow(content, row string) string {
	lines := strings.Split(content, "\n")
	var result []string
	inActive := false
	inserted := false

	for i, line := range lines {
		result = append(result, line)

		if strings.TrimSpace(line) == "## Active" {
			inActive = true
			continue
		}

		if inActive && !inserted {
			// Check if Active section has "_None_" placeholder
			if strings.TrimSpace(line) == "_None_" {
				// Replace _None_ with table header + row
				result[len(result)-1] = "| ID | Project | Title | Type |"
				result = append(result, "|----|---------|-------|------|")
				result = append(result, row)
				inserted = true
				inActive = false
				continue
			}
			// If we hit a table separator row, the next line is where rows go
			if strings.HasPrefix(strings.TrimSpace(line), "|---") {
				// Insert the row after the separator
				_ = i // separator line already added
				result = append(result, row)
				inserted = true
				inActive = false
				continue
			}
		}
	}

	return strings.Join(result, "\n")
}
