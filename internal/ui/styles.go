package ui

import "github.com/charmbracelet/lipgloss"

var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("170")).
			PaddingLeft(1)

	statusHeaderStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("243")).
				PaddingLeft(1)

	selectedStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("229")).
			Background(lipgloss.Color("57")).
			PaddingLeft(1).
			PaddingRight(1)

	normalStyle = lipgloss.NewStyle().
			PaddingLeft(2)

	detailTitleStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("170")).
				PaddingLeft(1).
				PaddingBottom(1)

	detailBodyStyle = lipgloss.NewStyle().
			PaddingLeft(2)

	statusBarStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("243")).
			PaddingLeft(1)

	filterPromptStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("205")).
				Bold(true)

	filterInputStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("229"))

	helpStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("241")).
			PaddingLeft(1)

	emptyStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("243")).
			PaddingLeft(2).
			PaddingTop(1)

	typeStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("243"))

	projectStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("109"))
)
