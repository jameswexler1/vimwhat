//go:build windows

package media

import (
	"strings"
	"unicode"
)

func platformSafeSaveFileName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "media"
	}
	var out strings.Builder
	for _, r := range name {
		switch {
		case r < 32, strings.ContainsRune(`<>:"/\|?*`, r):
			out.WriteByte('_')
		default:
			out.WriteRune(r)
		}
	}
	cleaned := strings.TrimRightFunc(strings.TrimSpace(out.String()), func(r rune) bool {
		return r == '.' || unicode.IsSpace(r)
	})
	if cleaned == "" || isReservedWindowsFilename(cleaned) {
		return "media"
	}
	return cleaned
}

func isReservedWindowsFilename(name string) bool {
	base := strings.TrimSpace(name)
	if idx := strings.IndexByte(base, '.'); idx >= 0 {
		base = base[:idx]
	}
	base = strings.ToUpper(base)
	switch base {
	case "CON", "PRN", "AUX", "NUL":
		return true
	case "COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9":
		return true
	case "LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9":
		return true
	default:
		return false
	}
}
