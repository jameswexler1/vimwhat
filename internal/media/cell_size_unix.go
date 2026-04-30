//go:build !windows

package media

import (
	"os"

	"golang.org/x/sys/unix"
)

func platformTerminalCellPixels() (terminalCellPixels, bool) {
	window, err := unix.IoctlGetWinsize(int(os.Stdout.Fd()), unix.TIOCGWINSZ)
	if err != nil || window == nil || window.Col == 0 || window.Row == 0 || window.Xpixel == 0 || window.Ypixel == 0 {
		return terminalCellPixels{}, false
	}
	cell := terminalCellPixels{
		Width:  int(window.Xpixel) / int(window.Col),
		Height: int(window.Ypixel) / int(window.Row),
	}
	return cell, cell.Width > 0 && cell.Height > 0
}
