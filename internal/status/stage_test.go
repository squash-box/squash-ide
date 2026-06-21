package status

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestValidClaudeStage(t *testing.T) {
	for _, s := range ClaudeStages {
		if !ValidClaudeStage(s) {
			t.Errorf("expected %q valid", s)
		}
	}
	// pr/ci are squash-ide-detected — never accepted from the MCP arg.
	for _, s := range []string{StagePR, StageCI, "", "bogus", "PLANNING"} {
		if ValidClaudeStage(s) {
			t.Errorf("expected %q invalid", s)
		}
	}
}

func TestWriteStage_ReadAllMerge_RoundTrip(t *testing.T) {
	withTempDir(t)
	if err := Write("T-1", "working", "coding"); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := WriteStage("T-1", StagePlanning); err != nil {
		t.Fatalf("writeStage: %v", err)
	}
	all, err := ReadAll()
	if err != nil {
		t.Fatalf("readAll: %v", err)
	}
	f, ok := all["T-1"]
	if !ok {
		t.Fatalf("missing T-1 in %#v", all)
	}
	if f.Stage != StagePlanning {
		t.Errorf("stage = %q, want %q", f.Stage, StagePlanning)
	}
	if f.State != "working" {
		t.Errorf("state = %q, want working (activity unaffected)", f.State)
	}
}

func TestWriteStage_AtomicNoTmpLeftover(t *testing.T) {
	withTempDir(t)
	if err := WriteStage("T-7", StageTesting); err != nil {
		t.Fatalf("writeStage: %v", err)
	}
	entries, err := os.ReadDir(stageDirPath())
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" {
			t.Errorf("leftover .tmp file: %s", e.Name())
		}
	}
}

func TestReadAll_ActivityFileNoStage_EmptyStage(t *testing.T) {
	withTempDir(t)
	if err := Write("T-2", "idle", "done"); err != nil {
		t.Fatal(err)
	}
	all, _ := ReadAll()
	f, ok := all["T-2"]
	if !ok {
		t.Fatalf("missing T-2")
	}
	if f.Stage != "" {
		t.Errorf("stage = %q, want empty (no stage file)", f.Stage)
	}
	if f.State != "idle" {
		t.Errorf("state = %q, want idle", f.State)
	}
}

func TestReadAll_StageOnlyEntry_Surfaces(t *testing.T) {
	withTempDir(t)
	// A stage file with no activity file: a task can be mid-stage yet idle.
	if err := WriteStage("T-3", StageImplementation); err != nil {
		t.Fatal(err)
	}
	all, _ := ReadAll()
	f, ok := all["T-3"]
	if !ok {
		t.Fatalf("stage-only entry should surface, got %#v", all)
	}
	if f.Stage != StageImplementation {
		t.Errorf("stage = %q", f.Stage)
	}
	if f.State != "" {
		t.Errorf("state = %q, want empty (no activity file)", f.State)
	}
	if f.TaskID != "T-3" {
		t.Errorf("task id = %q", f.TaskID)
	}
}

func TestReadAll_StaleStageDropped(t *testing.T) {
	withTempDir(t)
	if err := os.MkdirAll(stageDirPath(), 0o755); err != nil {
		t.Fatal(err)
	}
	old := stageFile{
		TaskID:  "T-4",
		Stage:   StageAcceptance,
		Updated: time.Now().Add(-2 * StaleDuration).Unix(),
	}
	data, _ := json.Marshal(old)
	if err := os.WriteFile(stagePath("T-4"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	all, _ := ReadAll()
	if _, ok := all["T-4"]; ok {
		t.Error("stale stage-only entry should be dropped")
	}
}

func TestReadAll_GlobIgnoresStageSubdir(t *testing.T) {
	withTempDir(t)
	// Only a stage file exists — assert it is NOT picked up by the activity
	// glob (which would otherwise produce a phantom entry with no stage).
	if err := WriteStage("T-5", StagePlanning); err != nil {
		t.Fatal(err)
	}
	// Directly verify the activity glob never matches the stage subdir.
	matches, _ := filepath.Glob(filepath.Join(dirRef, "T-*.json"))
	if len(matches) != 0 {
		t.Errorf("activity glob picked up stage files: %v", matches)
	}
	// And the merged result has the stage but no spurious activity state.
	all, _ := ReadAll()
	if all["T-5"].State != "" {
		t.Errorf("phantom activity state: %q", all["T-5"].State)
	}
}

func TestRemoveStage(t *testing.T) {
	withTempDir(t)
	if err := RemoveStage("T-never"); err != nil {
		t.Errorf("remove missing stage: %v", err)
	}
	if err := WriteStage("T-6", StageTesting); err != nil {
		t.Fatal(err)
	}
	if err := RemoveStage("T-6"); err != nil {
		t.Fatalf("removeStage: %v", err)
	}
	if _, err := os.Stat(stagePath("T-6")); !os.IsNotExist(err) {
		t.Errorf("stage file still present: %v", err)
	}
}
