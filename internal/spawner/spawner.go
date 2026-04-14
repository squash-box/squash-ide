package spawner

import (
	"fmt"
	"os/exec"
	"syscall"
)

// terminal describes how to invoke a terminal emulator.
type terminal struct {
	bin  string
	args func(workdir, execCmd string) []string
}

// terminals is the ordered list of terminal emulators to try.
var terminals = []terminal{
	{
		bin: "ptyxis",
		args: func(workdir, execCmd string) []string {
			return []string{"-d", workdir, "--", "bash", "-c", execCmd}
		},
	},
	{
		bin: "gnome-terminal",
		args: func(workdir, execCmd string) []string {
			return []string{"--working-directory=" + workdir, "--", "bash", "-c", execCmd}
		},
	},
	{
		bin: "x-terminal-emulator",
		args: func(workdir, execCmd string) []string {
			// Debian alternatives — flag support varies, use -e
			return []string{"-e", "bash -c 'cd " + workdir + " && " + execCmd + "'"}
		},
	},
}

// Spawn opens a new terminal window in the given working directory,
// running `claude '/implement <taskID>'`. The spawned process is detached
// so it survives if the parent exits.
//
// Tries ptyxis → gnome-terminal → x-terminal-emulator, using whichever
// is found first on $PATH.
func Spawn(worktreePath, taskID string) error {
	execArg := fmt.Sprintf("claude '/implement %s'", taskID)

	for _, t := range terminals {
		binPath, err := exec.LookPath(t.bin)
		if err != nil {
			continue
		}

		cmd := exec.Command(binPath, t.args(worktreePath, execArg)...)
		cmd.SysProcAttr = &syscall.SysProcAttr{
			Setpgid: true,
		}

		if err := cmd.Start(); err != nil {
			return fmt.Errorf("spawning %s: %w", t.bin, err)
		}

		// Don't wait — the terminal runs independently
		return nil
	}

	return fmt.Errorf("no supported terminal emulator found (tried: ptyxis, gnome-terminal, x-terminal-emulator)")
}
