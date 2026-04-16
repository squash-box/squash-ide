package ui

import "github.com/charmbracelet/lipgloss"

// Color palette — kept centralised so the card layout stays internally
// consistent. Names map roughly to roles, not literal colors, so a future
// theme swap touches one place.
var (
	colorAccent  = lipgloss.Color("170") // app title / cursor accent (pink)
	colorMuted   = lipgloss.Color("243") // dim text (counts, project, footer)
	colorDivider = lipgloss.Color("238") // section dividers
	colorSection = lipgloss.Color("99")  // section bar (purple)
	colorWorking = lipgloss.Color("78")  // green badge bg
	colorIdle    = lipgloss.Color("214") // amber badge bg
	colorNeeds   = lipgloss.Color("204") // pink badge bg
	colorBadgeFg = lipgloss.Color("235") // dark text on light badge bg
	colorOnDark  = lipgloss.Color("229") // light text on dark bg
)

// --- Top bar ----------------------------------------------------------------

var (
	appTitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorAccent)

	countsStyle = lipgloss.NewStyle().
			Foreground(colorMuted)

	dividerStyle = lipgloss.NewStyle().
			Foreground(colorDivider)
)

// --- Section headers --------------------------------------------------------

var (
	sectionBarStyle = lipgloss.NewStyle().
			Foreground(colorSection).
			Bold(true)

	sectionLabelStyle = lipgloss.NewStyle().
				Foreground(colorMuted).
				Bold(true)

	placeholderStyle = lipgloss.NewStyle().
				Foreground(colorMuted).
				Italic(true)
)

// --- Status badges (active sub-states) --------------------------------------

var (
	badgeWorkingStyle = lipgloss.NewStyle().
				Background(colorWorking).
				Foreground(colorBadgeFg).
				Bold(true).
				Padding(0, 1)

	badgeIdleStyle = lipgloss.NewStyle().
			Background(colorIdle).
			Foreground(colorBadgeFg).
			Bold(true).
			Padding(0, 1)

	badgeNeedsStyle = lipgloss.NewStyle().
			Background(colorNeeds).
			Foreground(colorBadgeFg).
			Bold(true).
			Padding(0, 1)
)

// --- Task card --------------------------------------------------------------

var (
	taskIDStyle = lipgloss.NewStyle().
			Bold(true)

	taskTitleStyle = lipgloss.NewStyle()

	projectDimStyle = lipgloss.NewStyle().
			Foreground(colorMuted)

	progressOnStyle = lipgloss.NewStyle().
			Foreground(colorWorking)

	progressOffStyle = lipgloss.NewStyle().
				Foreground(colorDivider)

	// Left accent bar for the selected card.
	cursorBarStyle = lipgloss.NewStyle().
			Foreground(colorAccent).
			Bold(true)
)

// --- Footer / status / dialogs ----------------------------------------------

var (
	helpStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("241")).
			PaddingLeft(1)

	emptyStyle = lipgloss.NewStyle().
			Foreground(colorMuted).
			PaddingLeft(2).
			PaddingTop(1)

	statusBarStyle = lipgloss.NewStyle().
			Foreground(colorMuted).
			PaddingLeft(1)

	statusSuccessStyle = lipgloss.NewStyle().
				Foreground(colorWorking).
				PaddingLeft(1)

	statusErrorStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("196")).
				PaddingLeft(1)

	dispatchingStyle = lipgloss.NewStyle().
				Foreground(colorOnDark).
				PaddingLeft(1).
				Bold(true)

	filterPromptStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("205")).
				Bold(true)

	filterInputStyle = lipgloss.NewStyle().
				Foreground(colorOnDark)

	confirmBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("205")).
			Padding(0, 2).
			Bold(true)

	inputBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorOnDark).
			Padding(0, 2)
)

// --- Detail view ------------------------------------------------------------

var (
	detailTitleStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(colorAccent).
				PaddingLeft(1).
				PaddingBottom(1)

	detailBodyStyle = lipgloss.NewStyle().
			PaddingLeft(2)

	worktreeStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("109")).
			PaddingLeft(2)

	activeIndicatorStyle = lipgloss.NewStyle().
				Foreground(colorWorking).
				Bold(true)
)
