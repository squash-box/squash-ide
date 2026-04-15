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

// MoveToArchive moves a task file from active/ to archive/, sets the
// frontmatter status to "done" and adds a "completed" date. If optional
// branch and pr values are provided (non-empty), they are stamped into the
// frontmatter too. Returns the new file path.
func MoveToArchive(vaultRoot string, t task.Task, branch, pr string) (string, error) {
	srcDir := filepath.Join(vaultRoot, "tasks", "active")
	dstDir := filepath.Join(vaultRoot, "tasks", "archive")

	srcPath, err := findTaskFile(srcDir, t.ID)
	if err != nil {
		return "", err
	}

	data, err := os.ReadFile(srcPath)
	if err != nil {
		return "", fmt.Errorf("reading task file: %w", err)
	}

	today := time.Now().Format("2006-01-02")
	content := string(data)
	content = replaceFrontmatterField(content, "status", "done")
	content = addFrontmatterField(content, "completed", today)
	if branch != "" {
		content = addFrontmatterField(content, "branch", branch)
	}
	if pr != "" {
		content = addFrontmatterField(content, "pr", pr)
	}

	dstPath := filepath.Join(dstDir, filepath.Base(srcPath))
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		return "", fmt.Errorf("creating archive dir: %w", err)
	}
	if err := os.WriteFile(dstPath, []byte(content), 0o644); err != nil {
		return "", fmt.Errorf("writing task file: %w", err)
	}
	if err := os.Remove(srcPath); err != nil {
		return "", fmt.Errorf("removing old task file: %w", err)
	}

	return dstPath, nil
}

// MoveToBlocked moves a task file from active/ to blocked/, sets the
// frontmatter status to "blocked" and appends a "## Blocked" section
// containing the given one-line reason. Returns the new file path.
func MoveToBlocked(vaultRoot string, t task.Task, reason string) (string, error) {
	srcDir := filepath.Join(vaultRoot, "tasks", "active")
	dstDir := filepath.Join(vaultRoot, "tasks", "blocked")

	srcPath, err := findTaskFile(srcDir, t.ID)
	if err != nil {
		return "", err
	}

	data, err := os.ReadFile(srcPath)
	if err != nil {
		return "", fmt.Errorf("reading task file: %w", err)
	}

	today := time.Now().Format("2006-01-02")
	content := string(data)
	content = replaceFrontmatterField(content, "status", "blocked")
	content = addFrontmatterField(content, "blocked_at", today)

	// Append a "## Blocked" section to the body with the reason.
	blockNote := fmt.Sprintf("\n\n## Blocked (%s)\n\n%s\n", today, strings.TrimSpace(reason))
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	content += blockNote

	dstPath := filepath.Join(dstDir, filepath.Base(srcPath))
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		return "", fmt.Errorf("creating blocked dir: %w", err)
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
	return mutateBoard(vaultRoot, t.ID, "Active", "", false, t)
}

// UpdateBoardComplete moves a task row from the Active table to the
// Recently Completed table (prepending it so newest sits on top). The
// completion date (today) is appended as a trailing column.
func UpdateBoardComplete(vaultRoot string, t task.Task) error {
	return mutateBoard(vaultRoot, t.ID, "Recently Completed",
		time.Now().Format("2006-01-02"), true, t)
}

// UpdateBoardBlock moves a task row from the Active table to the Blocked
// section in board.md.
func UpdateBoardBlock(vaultRoot string, t task.Task) error {
	return mutateBoard(vaultRoot, t.ID, "Blocked", "", false, t)
}

// mutateBoard removes the row for taskID from wherever it appears in the
// board, then inserts it into the section named destSection. If extraCol
// is non-empty, it is appended as an additional trailing column. When
// prepend is true, the row is placed at the top of the destination table
// (used for Recently Completed so newest is first).
//
// The task parameter is used only as a fallback: if the row for taskID
// cannot be found in the board (e.g. board drifted out of sync), a fresh
// row is synthesized from t.
func mutateBoard(vaultRoot, taskID, destSection, extraCol string, prepend bool, t task.Task) error {
	boardPath := filepath.Join(vaultRoot, "tasks", "board.md")
	data, err := os.ReadFile(boardPath)
	if err != nil {
		return fmt.Errorf("reading board: %w", err)
	}

	content := string(data)

	// Remove existing row anywhere in the board.
	content, removedRow := removeBoardRow(content, taskID)
	if removedRow == "" {
		// Synthesize a row from the task — keeps the operation resilient to
		// drift between board state and file state.
		removedRow = fmt.Sprintf("| [[%s]] | %s | %s | %s |",
			t.ID, t.Project, t.Title, t.Type)
	}

	rowToInsert := removedRow
	if extraCol != "" {
		rowToInsert = strings.TrimRight(removedRow, " ") + " " + extraCol + " |"
	}

	content = insertRowIntoSection(content, destSection, rowToInsert, prepend, extraCol != "")

	// Update last_updated
	content = replaceFrontmatterField(content, "last_updated", time.Now().Format("2006-01-02"))

	return os.WriteFile(boardPath, []byte(content), 0o644)
}

// removeBoardRow removes the first line containing "| [[<taskID>]] |" from
// content and returns the new content plus the removed row (or "" if no
// matching row was found).
func removeBoardRow(content, taskID string) (string, string) {
	marker := fmt.Sprintf("| [[%s]] |", taskID)
	var removed string
	lines := strings.Split(content, "\n")
	out := lines[:0]
	for _, line := range lines {
		if removed == "" && strings.Contains(line, marker) {
			removed = line
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n"), removed
}

// AppendLog adds a spawn entry to wiki/log.md.
func AppendLog(vaultRoot string, t task.Task, branch, worktreePath string) error {
	entry := fmt.Sprintf("## [%s] spawn | %s %s\nBranch: %s | Worktree: %s\n",
		time.Now().Format("2006-01-02"), t.ID, t.Title, branch, worktreePath)
	return prependLogEntry(vaultRoot, entry)
}

// AppendLogComplete adds a completion entry to wiki/log.md.
func AppendLogComplete(vaultRoot string, t task.Task, branch string) error {
	entry := fmt.Sprintf("## [%s] complete | %s %s\nBranch: %s\n",
		time.Now().Format("2006-01-02"), t.ID, t.Title, branch)
	return prependLogEntry(vaultRoot, entry)
}

// AppendLogBlock adds a block entry to wiki/log.md.
func AppendLogBlock(vaultRoot string, t task.Task, reason string) error {
	entry := fmt.Sprintf("## [%s] block | %s %s\nReason: %s\n",
		time.Now().Format("2006-01-02"), t.ID, t.Title, strings.TrimSpace(reason))
	return prependLogEntry(vaultRoot, entry)
}

// prependLogEntry inserts an entry at the top of the entries list in
// wiki/log.md (i.e. right before the first existing `## [` heading).
func prependLogEntry(vaultRoot, entry string) error {
	logPath := filepath.Join(vaultRoot, "wiki", "log.md")
	data, err := os.ReadFile(logPath)
	if err != nil {
		return fmt.Errorf("reading log: %w", err)
	}

	content := string(data)
	idx := strings.Index(content, "\n\n## [")
	if idx >= 0 {
		content = content[:idx+1] + "\n" + entry + content[idx+1:]
	} else {
		if !strings.HasSuffix(content, "\n") {
			content += "\n"
		}
		content += "\n" + entry
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
// If the field is not present, the content is returned unchanged.
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
// If a field with the same name already exists, it is replaced in place.
func addFrontmatterField(content, field, value string) string {
	// If field already exists, replace in place.
	if hasFrontmatterField(content, field) {
		return replaceFrontmatterField(content, field, value)
	}
	lines := strings.Split(content, "\n")
	count := 0
	for i, line := range lines {
		if strings.TrimSpace(line) == "---" {
			count++
			if count == 2 {
				newLine := field + ": " + value
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

// hasFrontmatterField reports whether the frontmatter contains the field.
func hasFrontmatterField(content, field string) bool {
	lines := strings.Split(content, "\n")
	inFront := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "---" {
			if inFront {
				return false
			}
			inFront = true
			continue
		}
		if inFront && strings.HasPrefix(trimmed, field+":") {
			return true
		}
	}
	return false
}

// insertRowIntoSection inserts row into the named section of a board.md-style
// document. If the section currently holds a `_None_` placeholder, it is
// replaced by a fresh table header + the row. If the section already has a
// table, the row is inserted just after the header separator (prepend=true)
// or appended at the end of the rows.
//
// withExtraCol = true means the inserted row has one additional trailing
// column compared to the canonical 4-column Active/Blocked/Backlog headers.
// The header row inserted when replacing `_None_` gets an extra "Completed"
// column in that case — this is the convention in the Personal vault board.
func insertRowIntoSection(content, section, row string, prepend, withExtraCol bool) string {
	lines := strings.Split(content, "\n")
	sectionHeader := "## " + section

	var result []string
	inSection := false
	inserted := false
	sawSeparator := false

	flushInsert := func() {
		if inserted {
			return
		}
		result = append(result, row)
		inserted = true
	}

	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		// Entering section
		if trimmed == sectionHeader {
			// If we were in the destination section without inserting, do it
			// now at the section boundary.
			if inSection && !inserted {
				flushInsert()
			}
			inSection = true
			sawSeparator = false
			result = append(result, line)
			continue
		}

		// Leaving section (next `## ` heading)
		if inSection && strings.HasPrefix(trimmed, "## ") && trimmed != sectionHeader {
			if !inserted {
				// Append at end of section — trim trailing blank lines we
				// just emitted so the table sits flush with the heading
				// above.
				// Trim trailing blank lines in result, insert row, then
				// re-add one blank separator before the next heading.
				trailingBlanks := 0
				for len(result) > 0 && strings.TrimSpace(result[len(result)-1]) == "" {
					result = result[:len(result)-1]
					trailingBlanks++
				}
				result = append(result, row)
				if trailingBlanks == 0 {
					result = append(result, "")
				} else {
					for j := 0; j < trailingBlanks; j++ {
						result = append(result, "")
					}
				}
				inserted = true
			}
			inSection = false
			result = append(result, line)
			continue
		}

		if inSection && !inserted {
			// Replace _None_ with a fresh table
			if trimmed == "_None_" {
				hdr := "| ID | Project | Title | Type |"
				sep := "|----|---------|-------|------|"
				if withExtraCol {
					hdr = "| ID | Project | Title | Type | Completed |"
					sep = "|----|---------|-------|------|-----------|"
				}
				result = append(result, hdr)
				result = append(result, sep)
				result = append(result, row)
				inserted = true
				continue
			}
			// Table separator — insert after it if prepend
			if strings.HasPrefix(trimmed, "|---") {
				result = append(result, line)
				sawSeparator = true
				if prepend {
					result = append(result, row)
					inserted = true
				}
				continue
			}
			// After separator, if prepend was false, we want to append after
			// the last row of the table. Easiest: when we see a blank line
			// directly after table rows, emit the row then the blank.
			if sawSeparator && !prepend && trimmed == "" {
				result = append(result, row)
				result = append(result, line)
				inserted = true
				continue
			}
		}

		result = append(result, line)
	}

	// Reached EOF while still in the destination section without inserting
	// (file didn't end with a newline / next heading).
	if inSection && !inserted {
		result = append(result, row)
	}

	return strings.Join(result, "\n")
}

// insertActiveRow is retained for compatibility: it delegates to
// insertRowIntoSection for the "Active" section without a prepend bias and
// without an extra column.
func insertActiveRow(content, row string) string {
	return insertRowIntoSection(content, "Active", row, false, false)
}
