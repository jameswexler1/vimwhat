//go:build windows

package ui

import (
	"fmt"
	"os"
	"runtime"

	"golang.org/x/sys/windows"
)

func prepareTerminalOutput() (TerminalReport, func() error) {
	handle := windows.Handle(os.Stdout.Fd())
	report := TerminalReport{
		Platform:           runtime.GOOS,
		StdoutConsoleKnown: true,
	}

	var original uint32
	if err := windows.GetConsoleMode(handle, &original); err != nil {
		report.Detail = fmt.Sprintf("stdout console mode unavailable: %v", err)
		return report, func() error { return nil }
	}
	report.StdoutConsole = true

	if err := windows.SetConsoleMode(handle, windowsOutputMode(original, true)); err == nil {
		report.VTProcessing = true
		report.DelayedNewline = true
		report.LastColumnGuard = true
		return report, func() error {
			return windows.SetConsoleMode(handle, original)
		}
	} else {
		report.Detail = fmt.Sprintf("delayed newline mode unavailable: %v", err)
	}

	if err := windows.SetConsoleMode(handle, windowsOutputMode(original, false)); err == nil {
		report.VTProcessing = true
		report.LastColumnGuard = true
		return report, func() error {
			return windows.SetConsoleMode(handle, original)
		}
	} else {
		report.Detail = joinTerminalDetails(report.Detail, fmt.Sprintf("virtual terminal mode unavailable: %v", err))
		report.LastColumnGuard = true
		return report, func() error { return nil }
	}
}

func windowsOutputMode(original uint32, delayedNewline bool) uint32 {
	mode := original | windows.ENABLE_PROCESSED_OUTPUT | windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING
	if delayedNewline {
		mode |= windows.DISABLE_NEWLINE_AUTO_RETURN
	}
	return mode
}
