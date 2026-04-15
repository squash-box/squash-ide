package spawner

import (
	"fmt"
	"os/exec"
	"syscall"

	"github.com/squashbox/squash-ide/internal/config"
)

// terminal describes how to invoke a terminal emulator when auto-detecting.
type terminal struct {
	bin  string
	args func(workdir, execCmd string) []string
}

// terminals is the ordered list of terminal emulators to try when the
// user has not configured a specific terminal.
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

// SpawnWith opens a new terminal window using the configured terminal and
// spawn command. vars is the templating context passed to both terminal.args
// and spawn.args; required keys are at least {cwd}, {task_id}, {worktree},
// {repo}, {branch}. The spawner additionally substitutes {exec} into
// terminal.args as the fully-rendered spawn command string.
//
// If cfg.Terminal.Command is empty, the spawner falls back to the built-in
// auto-detect list (ptyxis → gnome-terminal → x-terminal-emulator) preserving
// the T-007 behavior for users with no config.
//
// The spawned process is detached via Setpgid so it survives if the parent
// exits.
func SpawnWith(cfg config.Config, vars map[string]string) error {
	// Render the spawn command (runs inside the terminal) with templating.
	spawnArgs := config.ExpandAll(cfg.Spawn.Args, vars)
	execCmd := config.BuildExec(cfg.Spawn.Command, spawnArgs)

	// Add {exec} to the templating context for terminal.args substitution.
	termVars := make(map[string]string, len(vars)+1)
	for k, v := range vars {
		termVars[k] = v
	}
	termVars["exec"] = execCmd

	if cfg.Terminal.Command != "" {
		return runConfigured(cfg.Terminal, termVars)
	}
	return runAutoDetect(vars["cwd"], execCmd)
}

func runConfigured(term config.Terminal, vars map[string]string) error {
	binPath, err := exec.LookPath(term.Command)
	if err != nil {
		return fmt.Errorf("terminal %q not found on PATH: %w", term.Command, err)
	}
	args := config.ExpandAll(term.Args, vars)
	cmd := exec.Command(binPath, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("spawning %s: %w", term.Command, err)
	}
	return nil
}

func runAutoDetect(workdir, execCmd string) error {
	for _, t := range terminals {
		binPath, err := exec.LookPath(t.bin)
		if err != nil {
			continue
		}
		cmd := exec.Command(binPath, t.args(workdir, execCmd)...)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		if err := cmd.Start(); err != nil {
			return fmt.Errorf("spawning %s: %w", t.bin, err)
		}
		return nil
	}
	return fmt.Errorf("no supported terminal emulator found (tried: ptyxis, gnome-terminal, x-terminal-emulator); set terminal.command in config to use another")
}
