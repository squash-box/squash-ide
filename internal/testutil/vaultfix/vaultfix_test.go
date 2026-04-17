package vaultfix

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNew_ScaffoldsDirs(t *testing.T) {
	v := New(t)
	for _, d := range []string{"tasks/backlog", "tasks/active", "tasks/blocked", "tasks/archive", "wiki", "wiki/entities"} {
		if _, err := os.Stat(filepath.Join(v.Path(), d)); err != nil {
			t.Errorf("missing dir %s: %v", d, err)
		}
	}
}

func TestNew_BoardAndLogSeeded(t *testing.T) {
	v := New(t)
	board := v.ReadBoard()
	if !strings.Contains(board, "# Task Board") {
		t.Errorf("board missing header: %q", board)
	}
	log := v.ReadLog()
	if !strings.Contains(log, "# Activity Log") {
		t.Errorf("log missing header: %q", log)
	}
}

func TestAddBacklog_WritesFile(t *testing.T) {
	v := New(t)
	path := v.AddBacklog("T-001", "Ship the thing")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if !strings.Contains(s, "id: T-001") {
		t.Error("missing id")
	}
	if !strings.Contains(s, "status: backlog") {
		t.Error("missing status")
	}
	if !strings.Contains(s, "title: Ship the thing") {
		t.Error("missing title")
	}
}

func TestAddActive_SetsStatus(t *testing.T) {
	v := New(t)
	path := v.AddActive("T-002", "Active item")
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "status: active") {
		t.Error("expected status: active")
	}
}

func TestAddBlocked_SetsStatus(t *testing.T) {
	v := New(t)
	path := v.AddBlocked("T-003", "Blocked item")
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "status: blocked") {
		t.Error("expected status: blocked")
	}
}

func TestTaskOpts_CustomProject(t *testing.T) {
	v := New(t)
	path := v.AddBacklog("T-004", "Custom", TaskOpts{Project: "myproj"})
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "project: myproj") {
		t.Error("expected project: myproj")
	}
}

func TestTaskOpts_WithRepo(t *testing.T) {
	v := New(t)
	path := v.AddBacklog("T-005", "repo-set", TaskOpts{Repo: "/tmp/repo"})
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "repo: /tmp/repo") {
		t.Error("expected repo field in frontmatter")
	}
}

func TestAddEntity_WritesFile(t *testing.T) {
	v := New(t)
	v.AddEntity("squash-ide", "/home/me/repo")
	path := filepath.Join(v.Path(), "wiki/entities/squash-ide.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "repo: /home/me/repo") {
		t.Errorf("entity missing repo line: %s", string(data))
	}
}
