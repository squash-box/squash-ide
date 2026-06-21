package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/squashbox/squash-ide/internal/ghx"
	"github.com/squashbox/squash-ide/internal/status"
	"github.com/squashbox/squash-ide/internal/task"
)

// ghProgress is the live, gh-detected PR/CI state for a task's branch (T-053).
// It is computed by the throttled tickProgress poll (model.go), never persisted,
// and overlaid onto the pr/ci stage lights. The zero value (no PR, no checks)
// renders both grey — the "if applicable" / not-raised-yet path.
type ghProgress struct {
	prRaised bool
	checks   ghx.ChecksState
}

// stageLight is the rendered state of one lifecycle traffic light.
type stageLight int

const (
	lightPending stageLight = iota // ○ grey
	lightActive                    // ◐ amber
	lightDone                      // ● green
	lightFailed                    // ✗ red (ci only)
)

func (l stageLight) glyph() string {
	switch l {
	case lightDone:
		return "●"
	case lightActive:
		return "◐"
	case lightFailed:
		return "✗"
	default:
		return "○"
	}
}

func (l stageLight) style() lipgloss.Style {
	switch l {
	case lightDone:
		return progressOnStyle
	case lightActive:
		return progressActiveStyle
	case lightFailed:
		return progressFailStyle
	default:
		return progressOffStyle
	}
}

// stageIndex returns s's position in status.StageOrder, or -1 if absent.
func stageIndex(s string) int {
	for i, v := range status.StageOrder {
		if v == s {
			return i
		}
	}
	return -1
}

// deriveStageLights maps a reported lifecycle stage plus the gh-detected pr/ci
// state onto one light per status.StageOrder entry. It honours StageOrder's
// monotonic rule — a reported stage implies every earlier stage is done — by
// deriving purely from positions, so adding a stage to StageOrder needs no
// change here (the "single source of truth" contract stage.go documents).
// pr/ci are never taken from the reported stage (Claude can't report them); they
// are overlaid by name from prog. An open PR (prog.prRaised) implies every stage
// up to and including pr is done.
func deriveStageLights(stage string, prog ghProgress) []stageLight {
	lights := make([]stageLight, len(status.StageOrder)) // zero value lightPending

	cur := stageIndex(stage)
	for i := range lights {
		switch {
		case cur >= 0 && i < cur:
			lights[i] = lightDone
		case i == cur:
			lights[i] = lightActive
		}
	}

	if prog.prRaised {
		if prIdx := stageIndex(status.StagePR); prIdx >= 0 {
			for i := 0; i <= prIdx; i++ {
				lights[i] = lightDone
			}
		}
	}

	if ciIdx := stageIndex(status.StageCI); ciIdx >= 0 {
		switch prog.checks {
		case ghx.ChecksPassing:
			lights[ciIdx] = lightDone
		case ghx.ChecksFailing:
			lights[ciIdx] = lightFailed
		default: // pending / none / "" → grey
			lights[ciIdx] = lightPending
		}
	}

	return lights
}

// renderStageStrip renders the lifecycle traffic lights for an active card.
// Unselected → a single compact glyph line (● ● ◐ ○ ○ ○); selected → a vertical
// labelled row per stage, honouring the task's "favour vertical space" goal.
// innerW bounds each line so the strip never overflows a 20-col compact sidebar:
// the label is clipped (the glyph + space prefix is always kept).
func renderStageStrip(stage string, prog ghProgress, selected bool, innerW int) []string {
	lights := deriveStageLights(stage, prog)

	if !selected {
		parts := make([]string, len(lights))
		for i, l := range lights {
			parts[i] = l.style().Render(l.glyph())
		}
		return []string{strings.Join(parts, " ")}
	}

	lines := make([]string, 0, len(lights))
	for i, l := range lights {
		label := truncate(status.StageOrder[i], innerW-2) // glyph + space = 2 cols
		lines = append(lines, l.style().Render(l.glyph())+" "+progressLabelStyle.Render(label))
	}
	return lines
}

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
	// An empty State is treated as idle too: it means "no activity report" —
	// the same as a nil sub. This arises for a stage-only entry (T-053), where
	// ReadAll surfaced a task by its stage file with no live activity file.
	if sub != nil && sub.State != "" {
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
func renderCard(t task.Task, selected bool, width int, sub *status.File, compact bool, prog ghProgress, showStrip bool) []string {
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
		lines = appendStageStrip(lines, t, sub, prog, showStrip, selected, leftPad, innerW)
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

	lines = appendStageStrip(lines, t, sub, prog, showStrip, selected, leftPad, innerW)
	return lines
}

// appendStageStrip appends the lifecycle progress strip to an active card's
// lines when enabled (T-053). The reported stage comes from the merged status
// file (sub.Stage; "" when sub is nil); pr/ci come from prog. Backlog cards and
// the showStrip=false (config-disabled) case append nothing.
func appendStageStrip(lines []string, t task.Task, sub *status.File, prog ghProgress, showStrip, selected bool, leftPad string, innerW int) []string {
	if !showStrip || t.Status != "active" {
		return lines
	}
	stage := ""
	if sub != nil {
		stage = sub.Stage
	}
	for _, sline := range renderStageStrip(stage, prog, selected, innerW) {
		lines = append(lines, leftPad+sline)
	}
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
