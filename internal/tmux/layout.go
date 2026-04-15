// Package tmux contains a thin wrapper around the tmux CLI plus the pure
// layout math used to tile spawned panes alongside a fixed-width TUI column.
//
// The layout model: a single tmux window with the squash-ide TUI pinned to a
// fixed-width pane on the left, and N spawned-task panes sharing the
// remaining horizontal space equally on the right. Each adjacent pane pair is
// separated by a 1-column tmux pane border, so for N right-side panes plus
// the TUI we have N borders total (1 between TUI and the first right pane,
// plus N-1 between the right panes).
package tmux

import "fmt"

// Tile computes per-pane widths for n right-side panes, given the total window
// width, the desired fixed TUI width, and a per-pane minimum width.
//
// Returns a slice of length n whose elements sum to (totalCols - tuiWidth - n)
// — i.e. the available horizontal space after subtracting the TUI column and
// the n pane borders. The remainder (when the available width does not divide
// evenly) is distributed one column at a time to the leftmost panes.
//
// If any pane would fall strictly below minWidth, Tile returns an error with
// enough context for the spawner to surface a useful rejection message — the
// caller is expected to refuse the spawn rather than silently squeeze panes.
func Tile(totalCols, tuiWidth, n, minWidth int) ([]int, error) {
	if n < 1 {
		return nil, fmt.Errorf("tmux.Tile: n must be >= 1, got %d", n)
	}
	if tuiWidth < 1 {
		return nil, fmt.Errorf("tmux.Tile: tuiWidth must be >= 1, got %d", tuiWidth)
	}
	if minWidth < 1 {
		return nil, fmt.Errorf("tmux.Tile: minWidth must be >= 1, got %d", minWidth)
	}
	// n borders: one between TUI and the first right pane (+1) and n-1
	// between adjacent right panes.
	avail := totalCols - tuiWidth - n
	if avail < n*minWidth {
		// Either avail itself is non-positive, or each pane would be below
		// the floor. Surface both numbers so the caller can explain.
		return nil, fmt.Errorf(
			"not enough horizontal space to add another pane: total=%d tui=%d panes=%d min=%d (would give %d cols/pane)",
			totalCols, tuiWidth, n, minWidth, perPaneSafe(avail, n),
		)
	}
	per := avail / n
	widths := make([]int, n)
	for i := range widths {
		widths[i] = per
	}
	// Distribute the leftover columns one-per-pane to the leftmost panes,
	// keeping widths within 1 column of each other.
	remainder := avail - per*n
	for i := 0; i < remainder; i++ {
		widths[i]++
	}
	return widths, nil
}

// perPaneSafe is a small helper for the error message — guards against
// divide-by-zero and reports 0 instead of a confusing negative number when
// avail is itself negative.
func perPaneSafe(avail, n int) int {
	if n <= 0 || avail <= 0 {
		return 0
	}
	return avail / n
}
