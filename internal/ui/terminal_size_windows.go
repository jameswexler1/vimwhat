//go:build windows

package ui

import (
	"os"

	"golang.org/x/sys/windows"
)

func platformTerminalSizePollingEnabled() bool {
	return true
}

func platformTerminalSize() (int, int, bool) {
	handle := windows.Handle(os.Stdout.Fd())
	var info windows.ConsoleScreenBufferInfo
	if err := windows.GetConsoleScreenBufferInfo(handle, &info); err != nil {
		return 0, 0, false
	}
	width := int(info.Window.Right-info.Window.Left) + 1
	height := int(info.Window.Bottom-info.Window.Top) + 1
	return width, height, width > 0 && height > 0
}
