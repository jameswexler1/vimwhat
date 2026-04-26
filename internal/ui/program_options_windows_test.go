//go:build windows

package ui

import "testing"

func TestWindowsReportFocusDisabledByDefault(t *testing.T) {
	t.Setenv("VIMWHAT_DISABLE_REPORT_FOCUS", "")
	t.Setenv("VIMWHAT_FORCE_REPORT_FOCUS", "")
	t.Setenv("WT_SESSION", "")
	t.Setenv("TERM_PROGRAM", "")

	if windowsReportFocusEnabled() {
		t.Fatal("windowsReportFocusEnabled() = true, want false without a capable host signal")
	}
}

func TestWindowsReportFocusEnabledInWindowsTerminal(t *testing.T) {
	t.Setenv("VIMWHAT_DISABLE_REPORT_FOCUS", "")
	t.Setenv("VIMWHAT_FORCE_REPORT_FOCUS", "")
	t.Setenv("WT_SESSION", "session")
	t.Setenv("TERM_PROGRAM", "")

	if !windowsReportFocusEnabled() {
		t.Fatal("windowsReportFocusEnabled() = false, want true for Windows Terminal")
	}
}

func TestWindowsReportFocusCanBeForced(t *testing.T) {
	t.Setenv("VIMWHAT_DISABLE_REPORT_FOCUS", "")
	t.Setenv("VIMWHAT_FORCE_REPORT_FOCUS", "1")
	t.Setenv("WT_SESSION", "")
	t.Setenv("TERM_PROGRAM", "")

	if !windowsReportFocusEnabled() {
		t.Fatal("windowsReportFocusEnabled() = false, want true when forced")
	}
}

func TestWindowsReportFocusDisableWins(t *testing.T) {
	t.Setenv("VIMWHAT_DISABLE_REPORT_FOCUS", "1")
	t.Setenv("VIMWHAT_FORCE_REPORT_FOCUS", "1")
	t.Setenv("WT_SESSION", "session")
	t.Setenv("TERM_PROGRAM", "wezterm")

	if windowsReportFocusEnabled() {
		t.Fatal("windowsReportFocusEnabled() = true, want false when disabled")
	}
}
