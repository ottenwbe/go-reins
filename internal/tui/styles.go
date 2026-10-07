package tui

import "charm.land/lipgloss/v2"

// Shared colors for the run and chat views. Lipgloss degrades
// gracefully on terminals without color.
var (
	boldStyle    = lipgloss.NewStyle().Bold(true)
	dimStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	userStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("81"))
	answerStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	toolStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("220"))
	errorStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	spinnerStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("212"))
)
