//go:build windows

package media

import (
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

type consoleFontInfo struct {
	Font uint32
	Size windows.Coord
}

var (
	kernel32ProcGetCurrentConsoleFont = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetCurrentConsoleFont")
	kernel32ProcGetConsoleFontSize    = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetConsoleFontSize")
)

func platformTerminalCellPixels() (terminalCellPixels, bool) {
	handle := windows.Handle(os.Stdout.Fd())
	var info consoleFontInfo
	ok, _, _ := kernel32ProcGetCurrentConsoleFont.Call(
		uintptr(handle),
		0,
		uintptr(unsafe.Pointer(&info)),
	)
	if ok == 0 {
		return terminalCellPixels{}, false
	}

	if cell, ok := consoleFontSize(handle, info.Font); ok {
		return cell, true
	}
	cell := terminalCellPixels{
		Width:  int(info.Size.X),
		Height: int(info.Size.Y),
	}
	return cell, cell.Width > 0 && cell.Height > 0
}

func consoleFontSize(handle windows.Handle, font uint32) (terminalCellPixels, bool) {
	value, _, _ := kernel32ProcGetConsoleFontSize.Call(uintptr(handle), uintptr(font))
	coord := uint32(value)
	cell := terminalCellPixels{
		Width:  int(int16(coord & 0xffff)),
		Height: int(int16((coord >> 16) & 0xffff)),
	}
	return cell, cell.Width > 0 && cell.Height > 0
}
