package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/squashbox/squash-ide/internal/status"
	"github.com/squashbox/squash-ide/internal/task"
)

// typeEmoji maps a task type to a single-character glyph (or short emoji
// sequence) shown on the task header row. Unknown types fall back to a
// neutral bullet.
func typeEmoji(typ string) string {
	switch strings.ToLower(typ) {
	case "feature":
		return "✨"
	case "bug", "fix":
		return "🐛"
	case "chore":
		return "📋"
	case "security":
		return "🔒"
	default:
		return "•"
	}
}

// activeBadge returns the styled status badge shown above the title row for
// an active task. When sub is non-nil the badge reflects the real runtime
// state reported by the MCP server; otherwise it falls back to WORKING.
func activeBadge(t task.Task, sub *status.File) string {
	state := "working"
	if sub != nil {
		state = sub.State
	}
	switch state {
	case "idle":
		return badgeIdleStyle.Render("○ IDLE")
	case "input_required":
		return badgeNeedsStyle.Render("⚠ INPUT REQUIRED")
	case "testing":
		return badgeTestingStyle.Render("⧖ TESTING")
	default:
		return badgeWorkingStyle.Render("● WORKING")
	}
}

// renderTopBar renders the top header line: app name on the left, a dimmed
// counts summary on the right. Width is the usable terminal width.
func renderTopBar(width int, appName, version string, counts map[string]int) string {
	left := appTitleStyle.Render(appName)
	if version != "" {
		left = left + " " + countsStyle.Render(version)
	}

	parts := []string{}
	if c := counts["active"]; c > 0 {
		parts = append(parts, fmt.Sprintf("%d active", c))
	}
	if c := counts["backlog"]; c > 0 {
		parts = append(parts, fmt.Sprintf("%d backlog", c))
	}
	if c := counts["blocked"]; c > 0 {
		parts = append(parts, fmt.Sprintf("%d blocked", c))
	}
	right := countsStyle.Render(strings.Join(parts, " | "))

	gap := width - lipgloss.Width(left) - lipgloss.Width(right) - 2
	if gap < 1 {
		gap = 1
	}
	return " " + left + strings.Repeat(" ", gap) + right + " "
}

// renderDivider renders a thin dimmed horizontal rule across the width.
func renderDivider(width int) string {
	if width < 2 {
		width = 2
	}
	return dividerStyle.Render(strings.Repeat("─", width))
}

// renderSectionHeader renders the "▌ ACTIVE" / "▌ BACKLOG" line.
func renderSectionHeader(label string) string {
	return sectionBarStyle.Render("▌") + " " + sectionLabelStyle.Render(strings.ToUpper(label))
}

// renderPlaceholder renders a dimmed "no tasks here yet" row under a
// section header. Indented to match the card body so the section reads
// as "empty" rather than "misaligned".
func renderPlaceholder(msg string) string {
	return "   " + placeholderStyle.Render(msg)
}

// renderCard renders a task as 2 (backlog) or 3 (active) lines. The first
// line is the badge for active tasks; the next is the type+id+title row;
// the last is the project (and, when available, tmux pane / progress —
// currently elided because the model doesn't track per-task panes yet).
//
// When selected, each line gets a left accent bar instead of the usual
// left padding, so the highlight reads as a vertical stripe down the card.
func renderCard(t task.Task, selected bool, width int, sub *status.File) []string {
	leftPad := "   "
	if selected {
		leftPad = " " + cursorBarStyle.Render("▍") + " "
	}

	// Inner content width (after left padding).
	innerW := width - lipgloss.Width(leftPad)
	if innerW < 20 {
		innerW = 20
	}

	var lines []string

	// L1: badge (active only).
	if t.Status == "active" {
		lines = append(lines, leftPad+activeBadge(t, sub))
	}

	// L2: emoji + #id + title.
	id := taskIDStyle.Render("#" + strings.TrimPrefix(t.ID, "T-"))
	emoji := typeEmoji(t.Type)
	prefix := emoji + "  " + id + "  "
	titleW := innerW - lipgloss.Width(prefix)
	title := truncate(t.Title, titleW)
	lines = append(lines, leftPad+prefix+taskTitleStyle.Render(title))

	// L3: project (dimmed). Tmux pane id + progress bar are placeholders
	// the data model does not yet feed; once spawn tracking lands, append
	// "tmux:N ====" on the right side here.
	proj := projectDimStyle.Render(t.Project)
	// Indent the meta line under the title (past emoji + id width).
	metaIndent := strings.Repeat(" ", lipgloss.Width(emoji)+2)
	lines = append(lines, leftPad+metaIndent+proj)

	return lines
}

// truncate returns s clipped to maxW visual columns, with an ellipsis when
// it overflows. Lipgloss handles east-asian widths for us; here we just
// rune-slice and append "…" when needed.
func truncate(s string, maxW int) string {
	if maxW <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= maxW {
		return s
	}
	runes := []rune(s)
	for len(runes) > 0 {
		candidate := string(runes) + "…"
		if lipgloss.Width(candidate) <= maxW {
			return candidate
		}
		runes = runes[:len(runes)-1]
	}
	return "…"
}
