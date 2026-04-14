package spawner

import (
	"fmt"
	"os/exec"
	"syscall"
)

// Spawn opens a new gnome-terminal window in the given working directory,
// running `claude '/implement <taskID>'`. The spawned process is detached
// so it survives if the parent exits.
func Spawn(worktreePath, taskID string) error {
	execArg := fmt.Sprintf("claude '/implement %s'", taskID)

	cmd := exec.Command("gnome-terminal",
		"--working-directory="+worktreePath,
		"--", "bash", "-c", execArg,
	)

	// Detach the child process group so it survives parent exit
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("spawning terminal: %w", err)
	}

	// Don't wait — the terminal runs independently
	return nil
}
