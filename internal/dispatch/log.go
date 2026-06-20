package dispatch

import (
	"fmt"
	"os"
)

// Logging for the dispatch lifecycle.
//
// Same convention as internal/pane/log.go and internal/tmux: best-effort writes
// to stderr with a short prefix, gated behind SQUASH_DEBUG so the routine
// spawn/teardown trace (greppable for who-did-what across the engine branches)
// is off by default.
var debug = os.Getenv("SQUASH_DEBUG") != ""

// infof writes a lifecycle trace line when SQUASH_DEBUG is set.
func infof(format string, args ...any) {
	if !debug {
		return
	}
	fmt.Fprintf(os.Stderr, "squash-ide debug: dispatch: "+format+"\n", args...)
}
