//go:build !windows

package ui

import "runtime"

func prepareTerminalOutput() (TerminalReport, func() error) {
	return TerminalReport{
		Platform:       runtime.GOOS,
		VTProcessing:   true,
		DelayedNewline: true,
	}, func() error { return nil }
}
