//go:build windows

package ui

import (
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

func programOptions() []tea.ProgramOption {
	options := []tea.ProgramOption{tea.WithAltScreen()}
	if windowsReportFocusEnabled() {
		options = append(options, tea.WithReportFocus())
	}
	return options
}

func windowsReportFocusEnabled() bool {
	if envFlag("VIMWHAT_DISABLE_REPORT_FOCUS") {
		return false
	}
	if envFlag("VIMWHAT_FORCE_REPORT_FOCUS") {
		return true
	}
	if strings.TrimSpace(os.Getenv("WT_SESSION")) != "" {
		return true
	}
	program := strings.ToLower(strings.TrimSpace(os.Getenv("TERM_PROGRAM")))
	return strings.Contains(program, "wezterm")
}

func envFlag(key string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
