package status

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// withTempDir swaps the package-level Dir to a temp path and restores it at
// test end. Status files don't live in the vault, so t.TempDir() is perfect.
func withTempDir(t *testing.T) string {
	t.Helper()
	d := filepath.Join(t.TempDir(), "status")
	orig := dirRef
	dirRef = d
	t.Cleanup(func() { dirRef = orig })
	return d
}

func TestWrite_And_Read_RoundTrip(t *testing.T) {
	withTempDir(t)
	if err := Write("T-001", "working", "building X"); err != nil {
		t.Fatalf("write: %v", err)
	}
	all, err := ReadAll()
	if err != nil {
		t.Fatalf("readAll: %v", err)
	}
	f, ok := all["T-001"]
	if !ok {
		t.Fatalf("missing T-001 in %#v", all)
	}
	if f.State != "working" {
		t.Errorf("state = %q", f.State)
	}
	if f.Message != "building X" {
		t.Errorf("message = %q", f.Message)
	}
}

func TestWrite_AtomicRename(t *testing.T) {
	d := withTempDir(t)
	if err := Write("T-007", "idle", "ok"); err != nil {
		t.Fatalf("write: %v", err)
	}
	// Only the target should exist — no leftover .tmp.
	entries, err := os.ReadDir(d)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" {
			t.Errorf("leftover .tmp file: %s", e.Name())
		}
	}
}

func TestReadAll_SkipsStale(t *testing.T) {
	d := withTempDir(t)
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	// Hand-craft an old file bypassing Write.
	old := File{
		TaskID:  "T-002",
		State:   "idle",
		Message: "stale",
		Updated: time.Now().Add(-2 * StaleDuration).Unix(),
	}
	data, _ := json.Marshal(old)
	if err := os.WriteFile(filepath.Join(d, "T-002.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	// And a fresh one.
	if err := Write("T-003", "working", "fresh"); err != nil {
		t.Fatal(err)
	}

	all, err := ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := all["T-002"]; ok {
		t.Error("stale file should be skipped")
	}
	if _, ok := all["T-003"]; !ok {
		t.Error("fresh file should be present")
	}
}

func TestReadAll_IgnoresInvalidJSON(t *testing.T) {
	d := withTempDir(t)
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "T-999.json"), []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	all, err := ReadAll()
	if err != nil {
		t.Fatalf("readAll: %v", err)
	}
	if _, ok := all["T-999"]; ok {
		t.Error("invalid JSON should be skipped")
	}
}

func TestReadAll_DerivesTaskIDFromFilename(t *testing.T) {
	d := withTempDir(t)
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	// Write a file with no task_id set but a matching filename.
	f := File{State: "working", Message: "m", Updated: time.Now().Unix()}
	data, _ := json.Marshal(f)
	if err := os.WriteFile(filepath.Join(d, "T-123.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	all, err := ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := all["T-123"]; !ok {
		t.Errorf("expected T-123 keyed by filename, got %#v", all)
	}
}

func TestRemove_MissingIsNoOp(t *testing.T) {
	withTempDir(t)
	if err := Remove("T-never-existed"); err != nil {
		t.Errorf("remove missing: %v", err)
	}
}

func TestRemove_ExistingDeletes(t *testing.T) {
	d := withTempDir(t)
	if err := Write("T-010", "idle", "x"); err != nil {
		t.Fatal(err)
	}
	if err := Remove("T-010"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, err := os.Stat(filepath.Join(d, "T-010.json")); !os.IsNotExist(err) {
		t.Errorf("file still present: %v", err)
	}
}

func TestReadAll_EmptyDir(t *testing.T) {
	withTempDir(t)
	all, err := ReadAll()
	if err != nil {
		t.Fatalf("readAll: %v", err)
	}
	if len(all) != 0 {
		t.Errorf("expected empty, got %#v", all)
	}
}
