//go:build windows

package ui

import (
	"testing"

	"golang.org/x/sys/windows"
)

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

func TestWindowsTUIFPSDefaultsToThirty(t *testing.T) {
	t.Setenv("VIMWHAT_TUI_FPS", "")

	if got := windowsTUIFPS(); got != 30 {
		t.Fatalf("windowsTUIFPS() = %d, want 30", got)
	}
}

func TestWindowsTUIFPSCanBeOverridden(t *testing.T) {
	t.Setenv("VIMWHAT_TUI_FPS", "45")

	if got := windowsTUIFPS(); got != 45 {
		t.Fatalf("windowsTUIFPS() = %d, want 45", got)
	}
}

func TestWindowsOutputModeRequestsVirtualTerminalAndDelayedNewline(t *testing.T) {
	mode := windowsOutputMode(0, true)
	for _, flag := range []uint32{
		windows.ENABLE_PROCESSED_OUTPUT,
		windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING,
		windows.DISABLE_NEWLINE_AUTO_RETURN,
	} {
		if mode&flag == 0 {
			t.Fatalf("windowsOutputMode(0, true) = %#x, missing flag %#x", mode, flag)
		}
	}
}

func TestWindowsOutputModeFallbackOmitsDelayedNewline(t *testing.T) {
	mode := windowsOutputMode(0, false)
	for _, flag := range []uint32{
		windows.ENABLE_PROCESSED_OUTPUT,
		windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING,
	} {
		if mode&flag == 0 {
			t.Fatalf("windowsOutputMode(0, false) = %#x, missing flag %#x", mode, flag)
		}
	}
	if mode&windows.DISABLE_NEWLINE_AUTO_RETURN != 0 {
		t.Fatalf("windowsOutputMode(0, false) = %#x, want delayed newline flag omitted", mode)
	}
}
