//go:build !windows

package ui

import tea "github.com/charmbracelet/bubbletea"

func programOptions() []tea.ProgramOption {
	return []tea.ProgramOption{
		tea.WithAltScreen(),
		tea.WithReportFocus(),
	}
}
