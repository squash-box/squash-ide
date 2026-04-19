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
// state reported by the MCP server. When sub is nil the task has no live
// status report (either never written, or aged past status.StaleDuration) —
// render as IDLE rather than WORKING, since the Stop hook is the
// authoritative turn-end signal and an absent file means the last signal
// has aged out rather than flipped back to active work.
func activeBadge(t task.Task, sub *status.File) string {
	state := "idle"
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

// renderCard renders a task as either the expanded shape (2 lines for
// backlog, 3 for active) or the compact shape (3 lines for backlog, 4 for
// active). The expanded header packs emoji+id+title onto one row; the
// compact shape stacks them, giving the title the full inner-width budget
// instead of the leftover after the id prefix.
//
// Shapes:
//   - expanded backlog: [header(emoji+id+title), project]
//   - expanded active:  [badge, header, project]
//   - compact  backlog: [id(emoji+#NNN), title, project]
//   - compact  active:  [badge, id, title, project]
//
// The compact shape is exactly one row taller than the expanded shape for
// the same status. The extra row buys legibility in a 20-col sidebar: at
// that width the expanded header's title field is only ~10 cols after the
// emoji+id prefix, so most titles truncate to uselessness. Stacking frees
// the full 17-col inner budget for the title.
//
// When selected, each line gets a left accent bar instead of the usual
// left padding, so the highlight reads as a vertical stripe down the card.
func renderCard(t task.Task, selected bool, width int, sub *status.File, compact bool) []string {
	leftPad := "   "
	if selected {
		leftPad = " " + cursorBarStyle.Render("▍") + " "
	}

	// Inner content width (after left padding). In compact mode the outer
	// width is CompactListWidth (20) and the leftPad eats 3 cols, so innerW
	// can be as low as 17; truncate() clips content to whatever fits.
	innerW := width - lipgloss.Width(leftPad)
	if innerW < 1 {
		innerW = 1
	}

	id := taskIDStyle.Render("#" + strings.TrimPrefix(t.ID, "T-"))
	emoji := typeEmoji(t.Type)

	var lines []string

	// Badge (active only).
	if t.Status == "active" {
		lines = append(lines, leftPad+activeBadge(t, sub))
	}

	if compact {
		// Stacked: id line, title line, project line.
		lines = append(lines, leftPad+emoji+"  "+id)
		lines = append(lines, leftPad+taskTitleStyle.Render(truncate(t.Title, innerW)))
		lines = append(lines, leftPad+projectDimStyle.Render(truncate(t.Project, innerW)))
		return lines
	}

	// Expanded: single header row packs emoji+id+title.
	prefix := emoji + "  " + id + "  "
	titleW := innerW - lipgloss.Width(prefix)
	title := truncate(t.Title, titleW)
	lines = append(lines, leftPad+prefix+taskTitleStyle.Render(title))

	// Project meta line, indented under the title (past emoji + id width).
	proj := projectDimStyle.Render(t.Project)
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
