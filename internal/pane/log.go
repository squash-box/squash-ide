package pane

import (
	"fmt"
	"io"
	"os"
	"sync"
)

// Logging for the native pane engine.
//
// The codebase has no structured-logging dependency; the established
// convention is best-effort writes to stderr with a short prefix (see
// internal/tmux/tmux.go and internal/status/notify.go). We keep that here and
// add a single debug gate so the noisy lifecycle trace (spawn/close/resize/
// state-change) is silent by default and opt-in via SQUASH_DEBUG, while
// swallowed UX side-effect failures (a transient render/resize glitch that must
// never crash the TUI) always surface at warn level.
//
// logOut and debug are package-level so tests can redirect output to a buffer
// and flip the gate; logMu guards them because the per-pane read goroutine logs
// state transitions concurrently with the manager goroutine.
var (
	logMu  sync.Mutex
	logOut io.Writer = os.Stderr
	debug            = os.Getenv("SQUASH_DEBUG") != ""
)

// debugf writes a lifecycle trace line when the SQUASH_DEBUG gate is enabled.
func debugf(format string, args ...any) {
	logMu.Lock()
	on, out := debug, logOut
	logMu.Unlock()
	if !on {
		return
	}
	fmt.Fprintf(out, "squash-ide debug: pane: "+format+"\n", args...)
}

// warnf reports a swallowed best-effort failure. It is always emitted: the
// operation itself is non-fatal (we do not return the error to the caller), but
// the operator should still see that a render/resize glitch occurred.
func warnf(format string, args ...any) {
	logMu.Lock()
	out := logOut
	logMu.Unlock()
	fmt.Fprintf(out, "warning: pane: "+format+"\n", args...)
}
