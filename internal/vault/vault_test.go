package vault

import (
	"path/filepath"
	"runtime"
	"testing"
)

func testdataDir() string {
	_, filename, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(filename), "..", "..", "testdata")
}

func TestParse(t *testing.T) {
	content := `---
id: T-099
type: feature
title: Parse test
project: my-project
status: backlog
created: 2026-01-01
priority: high
related:
  - my-project
---

# T-099 — Parse test

Body content here.
`
	task, err := Parse(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if task.ID != "T-099" {
		t.Errorf("ID = %q, want %q", task.ID, "T-099")
	}
	if task.Type != "feature" {
		t.Errorf("Type = %q, want %q", task.Type, "feature")
	}
	if task.Title != "Parse test" {
		t.Errorf("Title = %q, want %q", task.Title, "Parse test")
	}
	if task.Project != "my-project" {
		t.Errorf("Project = %q, want %q", task.Project, "my-project")
	}
	if task.Status != "backlog" {
		t.Errorf("Status = %q, want %q", task.Status, "backlog")
	}
	if task.Created != "2026-01-01" {
		t.Errorf("Created = %q, want %q", task.Created, "2026-01-01")
	}
	if task.Priority != "high" {
		t.Errorf("Priority = %q, want %q", task.Priority, "high")
	}
	if len(task.Related) != 1 || task.Related[0] != "my-project" {
		t.Errorf("Related = %v, want [my-project]", task.Related)
	}
	if task.Body == "" {
		t.Error("Body is empty, expected content")
	}
}

func TestParse_NoRelated(t *testing.T) {
	content := `---
id: T-100
type: bug
title: No related field
project: proj
status: active
created: 2026-02-01
priority: low
---

# Body
`
	task, err := Parse(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if task.ID != "T-100" {
		t.Errorf("ID = %q, want %q", task.ID, "T-100")
	}
	if task.Related != nil {
		t.Errorf("Related = %v, want nil", task.Related)
	}
}

func TestParse_MissingDelimiter(t *testing.T) {
	_, err := Parse("no frontmatter here")
	if err == nil {
		t.Error("expected error for missing frontmatter, got nil")
	}
}

func TestParse_MissingClosingDelimiter(t *testing.T) {
	_, err := Parse("---\nid: T-001\n")
	if err == nil {
		t.Error("expected error for missing closing delimiter, got nil")
	}
}

func TestReadAll(t *testing.T) {
	tasks, err := ReadAll(testdataDir())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(tasks) != 3 {
		t.Fatalf("got %d tasks, want 3", len(tasks))
	}

	ids := map[string]bool{}
	for _, task := range tasks {
		ids[task.ID] = true
	}
	for _, id := range []string{"T-001", "T-002", "T-003"} {
		if !ids[id] {
			t.Errorf("missing task %s", id)
		}
	}
}

func TestReadAll_FilterByStatus(t *testing.T) {
	tasks, err := ReadAll(testdataDir())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var backlog int
	for _, task := range tasks {
		if task.Status == "backlog" {
			backlog++
		}
	}
	if backlog != 1 {
		t.Errorf("got %d backlog tasks, want 1", backlog)
	}
}

func TestReadAll_MissingDir(t *testing.T) {
	tasks, err := ReadAll("/nonexistent/path")
	if err != nil {
		t.Fatalf("unexpected error for missing dir: %v", err)
	}
	if len(tasks) != 0 {
		t.Errorf("got %d tasks, want 0 for missing vault", len(tasks))
	}
}

func TestExpandHome(t *testing.T) {
	expanded := expandHome("~/test/path")
	if expanded == "~/test/path" {
		t.Error("expandHome did not expand ~")
	}
	if expanded == "" {
		t.Error("expandHome returned empty string")
	}

	// Non-tilde path should be unchanged
	plain := expandHome("/absolute/path")
	if plain != "/absolute/path" {
		t.Errorf("expandHome changed non-tilde path: %q", plain)
	}
}
