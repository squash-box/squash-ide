package status

import (
	"fmt"
	"os"
	"os/exec"
)

// NotifyInputRequired sends a desktop notification via notify-send so the user
// knows a task pane is blocked awaiting input. Best-effort: notify-send may not
// be installed (e.g. macOS, headless CI), and we don't want that to fail the
// state transition that really matters — the file write. Errors are logged.
func NotifyInputRequired(taskID, message string) {
	cmd := exec.Command("notify-send",
		"-u", "critical",
		fmt.Sprintf("squash-ide: %s needs input", taskID),
		message,
	)
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "squash-ide: notify-send failed: %v\n", err)
	}
}
