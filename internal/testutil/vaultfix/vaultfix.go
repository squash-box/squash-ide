// Package vaultfix builds on-disk Obsidian-style task vaults for tests.
//
// Every test gets an isolated vault under t.TempDir(). The builder creates
// the tasks/{backlog,active,blocked,archive} directory tree, an empty
// tasks/board.md, and wiki/log.md — the minimum the taskops package needs
// to mutate. Callers add individual tasks via the fluent AddX methods.
//
// Example:
//
//	v := vaultfix.New(t)
//	v.AddBacklog("T-001", "Implement frobnicator")
//	v.AddActive("T-002", "Refactor glorp")
//	path := v.Path()
package vaultfix

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Vault wraps a temp-dir root and provides fluent task-file helpers.
type Vault struct {
	t    *testing.T
	root string
}

// New creates a new vault rooted at t.TempDir() with the standard
// directory layout and empty board/log.
func New(t *testing.T) *Vault {
	t.Helper()
	v := &Vault{t: t, root: t.TempDir()}
	v.scaffold()
	return v
}

// Path returns the vault's root directory.
func (v *Vault) Path() string { return v.root }

// TasksDir returns <root>/tasks.
func (v *Vault) TasksDir() string { return filepath.Join(v.root, "tasks") }

// BoardPath returns <root>/tasks/board.md.
func (v *Vault) BoardPath() string { return filepath.Join(v.root, "tasks", "board.md") }

// LogPath returns <root>/wiki/log.md.
func (v *Vault) LogPath() string { return filepath.Join(v.root, "wiki", "log.md") }

// ReadBoard returns the current contents of board.md.
func (v *Vault) ReadBoard() string {
	v.t.Helper()
	b, err := os.ReadFile(v.BoardPath())
	if err != nil {
		v.t.Fatalf("read board: %v", err)
	}
	return string(b)
}

// ReadLog returns the current contents of log.md.
func (v *Vault) ReadLog() string {
	v.t.Helper()
	b, err := os.ReadFile(v.LogPath())
	if err != nil {
		v.t.Fatalf("read log: %v", err)
	}
	return string(b)
}

// TaskOpts customises a task file built by AddBacklog/AddActive/AddBlocked.
type TaskOpts struct {
	Project string
	Type    string
	Repo    string
	Body    string
}

// AddBacklog writes a minimal backlog task file with status=backlog.
func (v *Vault) AddBacklog(id, title string, opts ...TaskOpts) string {
	return v.addTask("backlog", id, title, opts...)
}

// AddActive writes a minimal active task file with status=active.
func (v *Vault) AddActive(id, title string, opts ...TaskOpts) string {
	return v.addTask("active", id, title, opts...)
}

// AddBlocked writes a minimal blocked task file with status=blocked.
func (v *Vault) AddBlocked(id, title string, opts ...TaskOpts) string {
	return v.addTask("blocked", id, title, opts...)
}

// AddEntity writes a wiki entity page for project at wiki/entities/<project>.md
// with the supplied repo path in frontmatter, matching what vault.ReadEntityRepo
// expects.
func (v *Vault) AddEntity(project, repo string) {
	v.t.Helper()
	body := fmt.Sprintf(`---
type: entity
title: %s
repo: %s
status: active
tags: [project, test]
---

# %s

Test entity.
`, project, repo, project)
	path := filepath.Join(v.root, "wiki", "entities", project+".md")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		v.t.Fatalf("write entity: %v", err)
	}
}

func (v *Vault) scaffold() {
	v.t.Helper()
	dirs := []string{
		"tasks/backlog", "tasks/active", "tasks/blocked", "tasks/archive",
		"wiki", "wiki/entities",
	}
	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(v.root, d), 0o755); err != nil {
			v.t.Fatalf("mkdir %s: %v", d, err)
		}
	}

	board := `---
type: board
title: Test Board
last_updated: 2026-04-17
---

# Task Board

## Active

_None_

## Backlog

_None_

## Blocked

_None_

## Recently Completed

_None_
`
	if err := os.WriteFile(v.BoardPath(), []byte(board), 0o644); err != nil {
		v.t.Fatalf("write board: %v", err)
	}

	log := `---
type: log
title: Test Log
---

# Activity Log

`
	if err := os.WriteFile(v.LogPath(), []byte(log), 0o644); err != nil {
		v.t.Fatalf("write log: %v", err)
	}
}

func (v *Vault) addTask(status, id, title string, opts ...TaskOpts) string {
	v.t.Helper()
	var o TaskOpts
	if len(opts) > 0 {
		o = opts[0]
	}
	if o.Project == "" {
		o.Project = "test-proj"
	}
	if o.Type == "" {
		o.Type = "feature"
	}
	if o.Body == "" {
		o.Body = fmt.Sprintf("# %s — %s\n\nBody.\n", id, title)
	}

	fm := []string{
		"---",
		"id: " + id,
		"type: " + o.Type,
		"title: " + title,
		"project: " + o.Project,
		"status: " + status,
		"priority: medium",
		"created: 2026-04-17",
	}
	if o.Repo != "" {
		fm = append(fm, "repo: "+o.Repo)
	}
	fm = append(fm, "---", "", o.Body)

	slug := strings.ToLower(strings.ReplaceAll(title, " ", "-"))
	path := filepath.Join(v.root, "tasks", status, fmt.Sprintf("%s-%s.md", id, slug))
	if err := os.WriteFile(path, []byte(strings.Join(fm, "\n")), 0o644); err != nil {
		v.t.Fatalf("write task %s: %v", id, err)
	}
	return path
}
