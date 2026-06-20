package spawner

import (
	"reflect"
	"testing"

	"github.com/squashbox/squash-ide/internal/config"
)

func TestBuildSpawnCmd(t *testing.T) {
	cfg := config.Defaults() // Spawn.Command="claude", Args=["/implement {task_id}"]
	vars := map[string]string{
		"task_id": "T-039",
		"cwd":     "/work/tree",
	}

	cmd := BuildSpawnCmd(cfg, vars)

	if cmd.Args[0] != "claude" {
		t.Errorf("command = %q, want claude", cmd.Args[0])
	}
	// The single templated arg expands in place and stays one argv entry — the
	// native PTY path must match the shell path's "claude \"/implement T-039\"".
	if got, want := cmd.Args[1:], []string{"/implement T-039"}; !reflect.DeepEqual(got, want) {
		t.Errorf("args = %v, want %v", got, want)
	}
	if cmd.Dir != "/work/tree" {
		t.Errorf("Dir = %q, want /work/tree", cmd.Dir)
	}
}
