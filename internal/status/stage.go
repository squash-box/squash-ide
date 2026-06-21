package status

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Lifecycle-stage IPC (T-053).
//
// Stage is an *orthogonal* dimension to State (status.go). State answers "what
// is the session doing this second" (working|idle|input_required|testing);
// Stage answers "how far through the task is it"
// (planning→implementation→testing→acceptance→pr→ci). The two are deliberately
// kept separate so the new dimension does not force a new arm into the ~10
// activity-state switch sites across the codebase.
//
// Stage data lives in its own file Dir/stage/T-NNN.json, written *only* by the
// MCP path (the spawned Claude session). It is a separate file — not a field on
// the activity file T-NNN.json — because that file is written by two processes
// (the MCP server and the shell hooks); folding stage in would let a frequent
// PostToolUse hook write clobber a concurrently-written stage (cross-process
// read-modify-write). Each writer owns its file; ReadAll is the single merge
// point. This mirrors the separate-marker-file precedent set by notify.go and
// focus.go.
//
// The stage directory is derived from dirRef (Dir/stage) rather than carrying
// its own redirect seam: a SetDirForTesting in any package already relocates
// both the activity and stage files under one temp root, so tests never leak
// stage files into /tmp and the fork-bomb/leak guard in statusTempDir is
// satisfied automatically.

const (
	StagePlanning       = "planning"
	StageImplementation = "implementation"
	StageTesting        = "testing"
	StageAcceptance     = "acceptance"
	StagePR             = "pr"
	StageCI             = "ci"
)

// StageOrder is the canonical monotonic ordering of lifecycle stages. It is the
// single source of truth for deriving the traffic lights: a reported stage
// implies every earlier stage is done. Adding a stage touches only this slice.
var StageOrder = []string{
	StagePlanning, StageImplementation, StageTesting, StageAcceptance, StagePR, StageCI,
}

// ClaudeStages is the subset a spawned session may self-report (planning..
// acceptance). pr and ci are squash-ide-detected via gh, never accepted from
// the MCP stage argument.
var ClaudeStages = StageOrder[:4]

// stageSubdir is the subdirectory of dirRef holding stage files. It is a subdir
// (not a suffix on T-NNN.json) so the "T-*.json" glob in ReadAll never picks
// stage files up — the two file sets stay isolated.
const stageSubdir = "stage"

func stageDirPath() string           { return filepath.Join(dirRef, stageSubdir) }
func stagePath(taskID string) string { return filepath.Join(stageDirPath(), taskID+".json") }

// stageFile is the on-disk stage marker payload. It carries its own timestamp
// so ReadAll can apply the same StaleDuration cutoff to the stage file's mtime
// independently of the activity file.
type stageFile struct {
	TaskID  string `json:"task_id"`
	Stage   string `json:"stage"`
	Updated int64  `json:"updated"`
}

// ValidClaudeStage reports whether s is a stage a spawned session may report.
func ValidClaudeStage(s string) bool {
	for _, v := range ClaudeStages {
		if v == s {
			return true
		}
	}
	return false
}

// WriteStage atomically records the lifecycle stage a spawned session has
// reached. Written only by the MCP path; the shell-hook status writer never
// touches it. Atomic temp-then-rename mirrors Write.
func WriteStage(taskID, stage string) error {
	dir := stageDirPath()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(stageFile{TaskID: taskID, Stage: stage, Updated: time.Now().Unix()})
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, taskID+".tmp")
	target := filepath.Join(dir, taskID+".json")
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, target)
}

// readStage returns the stage reported for taskID, or ("", false) when there is
// no stage file, it is unreadable/corrupt, or it has aged past StaleDuration.
// Used by ReadAll's merge.
func readStage(taskID string) (string, bool) {
	data, err := os.ReadFile(stagePath(taskID))
	if err != nil {
		return "", false
	}
	var sf stageFile
	if err := json.Unmarshal(data, &sf); err != nil {
		return "", false
	}
	if sf.Stage == "" {
		return "", false
	}
	if sf.Updated < time.Now().Add(-StaleDuration).Unix() {
		return "", false // stale — the stage file aged out independently
	}
	return sf.Stage, true
}

// RemoveStage deletes the stage file for a task. It is not an error if the file
// does not exist. Mirrors Remove; callers tearing a task down drop both files.
func RemoveStage(taskID string) error {
	err := os.Remove(stagePath(taskID))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// mergeStages overlays the stage files in stageDirPath() onto result. A stage
// with no (or stale) activity file still surfaces as a stage-only entry — a
// task can be mid-stage yet idle. Factored out of ReadAll for readability.
func mergeStages(result map[string]File) {
	stageEntries, err := filepath.Glob(filepath.Join(stageDirPath(), "T-*.json"))
	if err != nil {
		return
	}
	for _, path := range stageEntries {
		taskID := strings.TrimSuffix(filepath.Base(path), ".json")
		stage, ok := readStage(taskID)
		if !ok {
			continue
		}
		f := result[taskID] // zero File when there is no activity entry
		f.TaskID = taskID
		f.Stage = stage
		result[taskID] = f
	}
}
