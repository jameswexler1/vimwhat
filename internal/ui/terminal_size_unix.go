//go:build !windows

package ui

func platformTerminalSizePollingEnabled() bool {
	return false
}

func platformTerminalSize() (int, int, bool) {
	return 0, 0, false
}
