package vault

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/squashbox/squash-ide/internal/task"
	"gopkg.in/yaml.v3"
)

// StatusDirs are the subdirectories under tasks/ that hold task files.
var StatusDirs = []string{"backlog", "active", "blocked", "archive"}

// ReadAll reads all task files from the vault at the given root path.
// It scans tasks/{backlog,active,blocked,archive}/*.md and parses each file's
// YAML frontmatter into a Task struct.
func ReadAll(root string) ([]task.Task, error) {
	root = expandHome(root)

	var tasks []task.Task
	for _, dir := range StatusDirs {
		dirPath := filepath.Join(root, "tasks", dir)
		entries, err := os.ReadDir(dirPath)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", dirPath, err)
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
				continue
			}
			filePath := filepath.Join(dirPath, entry.Name())
			t, err := ParseFile(filePath)
			if err != nil {
				return nil, fmt.Errorf("parsing %s: %w", filePath, err)
			}
			tasks = append(tasks, t)
		}
	}
	return tasks, nil
}

// ParseFile reads a single markdown file and extracts the YAML frontmatter
// into a Task struct. The body (everything after the closing ---) is stored
// in Task.Body.
func ParseFile(path string) (task.Task, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return task.Task{}, err
	}
	return Parse(string(data))
}

// Parse extracts YAML frontmatter from markdown content and returns a Task.
func Parse(content string) (task.Task, error) {
	frontmatter, body, err := splitFrontmatter(content)
	if err != nil {
		return task.Task{}, err
	}

	var t task.Task
	if err := yaml.Unmarshal([]byte(frontmatter), &t); err != nil {
		return task.Task{}, fmt.Errorf("unmarshaling frontmatter: %w", err)
	}
	t.Body = strings.TrimSpace(body)
	return t, nil
}

// splitFrontmatter splits markdown content at the --- delimiters.
// Returns the frontmatter YAML and the remaining body.
func splitFrontmatter(content string) (string, string, error) {
	const delimiter = "---"

	trimmed := strings.TrimSpace(content)
	if !strings.HasPrefix(trimmed, delimiter) {
		return "", "", fmt.Errorf("missing opening frontmatter delimiter")
	}

	// Skip the opening delimiter line
	rest := trimmed[len(delimiter):]
	rest = strings.TrimLeft(rest, " \t")
	if len(rest) > 0 && rest[0] == '\n' {
		rest = rest[1:]
	} else if len(rest) > 1 && rest[0] == '\r' && rest[1] == '\n' {
		rest = rest[2:]
	}

	// Find the closing delimiter
	idx := strings.Index(rest, "\n"+delimiter)
	if idx < 0 {
		return "", "", fmt.Errorf("missing closing frontmatter delimiter")
	}

	frontmatter := rest[:idx]
	body := rest[idx+1+len(delimiter):]
	// Skip the rest of the delimiter line
	if nl := strings.Index(body, "\n"); nl >= 0 {
		body = body[nl+1:]
	} else {
		body = ""
	}

	return frontmatter, body, nil
}

// expandHome replaces a leading ~ with the user's home directory.
func expandHome(path string) string {
	if !strings.HasPrefix(path, "~") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, path[1:])
}
