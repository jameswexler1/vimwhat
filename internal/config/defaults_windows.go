//go:build windows

package config

import (
	"os"
	"strings"
)

func platformDefaultEditor() string {
	for _, key := range []string{"EDITOR", "VISUAL"} {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return "notepad.exe"
}

func platformDefaultFilePickerCommand() string {
	return "powershell.exe -NoLogo -NoProfile -NonInteractive -STA -EncodedCommand " + windowsFilePickerEncodedCommand + " {chooser}"
}

func platformDefaultStickerPickerCommand() string {
	return "powershell.exe -NoLogo -NoProfile -NonInteractive -STA -EncodedCommand " + windowsStickerPickerEncodedCommand + " {chooser} {dir}"
}

func platformDefaultImageViewerCommand() string {
	return ""
}

func platformDefaultVideoPlayerCommand() string {
	return ""
}

func platformDefaultAudioPlayerCommand() string {
	return "rundll32.exe url.dll,FileProtocolHandler {path}"
}

func platformDefaultFileOpenerCommand() string {
	return "rundll32.exe url.dll,FileProtocolHandler {path}"
}

const windowsFilePickerEncodedCommand = "QQBkAGQALQBUAHkAcABlACAALQBBAHMAcwBlAG0AYgBsAHkATgBhAG0AZQAgAFMAeQBzAHQAZQBtAC4AVwBpAG4AZABvAHcAcwAuAEYAbwByAG0AcwA7ACAAJABkAGkAYQBsAG8AZwAgAD0AIABOAGUAdwAtAE8AYgBqAGUAYwB0ACAAUwB5AHMAdABlAG0ALgBXAGkAbgBkAG8AdwBzAC4ARgBvAHIAbQBzAC4ATwBwAGUAbgBGAGkAbABlAEQAaQBhAGwAbwBnADsAIABpAGYAIAAoACQAZABpAGEAbABvAGcALgBTAGgAbwB3AEQAaQBhAGwAbwBnACgAKQAgAC0AZQBxACAAIgBPAEsAIgApACAAewAgAFMAZQB0AC0AQwBvAG4AdABlAG4AdAAgAC0ATABpAHQAZQByAGEAbABQAGEAdABoACAAJABhAHIAZwBzAFsAMABdACAALQBWAGEAbAB1AGUAIAAkAGQAaQBhAGwAbwBnAC4ARgBpAGwAZQBOAGEAbQBlACAALQBOAG8ATgBlAHcAbABpAG4AZQAgAH0A"

const windowsStickerPickerEncodedCommand = "QQBkAGQALQBUAHkAcABlACAALQBBAHMAcwBlAG0AYgBsAHkATgBhAG0AZQAgAFMAeQBzAHQAZQBtAC4AVwBpAG4AZABvAHcAcwAuAEYAbwByAG0AcwA7ACAAJABkAGkAYQBsAG8AZwAgAD0AIABOAGUAdwAtAE8AYgBqAGUAYwB0ACAAUwB5AHMAdABlAG0ALgBXAGkAbgBkAG8AdwBzAC4ARgBvAHIAbQBzAC4ATwBwAGUAbgBGAGkAbABlAEQAaQBhAGwAbwBnADsAIAAkAGQAaQBhAGwAbwBnAC4ASQBuAGkAdABpAGEAbABEAGkAcgBlAGMAdABvAHIAeQAgAD0AIAAkAGEAcgBnAHMAWwAxAF0AOwAgACQAZABpAGEAbABvAGcALgBGAGkAbAB0AGUAcgAgAD0AIAAiAEkAbQBhAGcAZQBzAHwAKgAuAHcAZQBiAHAAOwAqAC4AcABuAGcAOwAqAC4AagBwAGcAOwAqAC4AagBwAGUAZwA7ACoALgBnAGkAZgB8AEEAbABsACAAZgBpAGwAZQBzAHwAKgAuACoAIgA7ACAAaQBmACAAKAAkAGQAaQBhAGwAbwBnAC4AUwBoAG8AdwBEAGkAYQBsAG8AZwAoACkAIAAtAGUAcQAgACIATwBLACIAKQAgAHsAIABTAGUAdAAtAEMAbwBuAHQAZQBuAHQAIAAtAEwAaQB0AGUAcgBhAGwAUABhAHQAaAAgACQAYQByAGcAcwBbADAAXQAgAC0AVgBhAGwAdQBlACAAJABkAGkAYQBsAG8AZwAuAEYAaQBsAGUATgBhAG0AZQAgAC0ATgBvAE4AZQB3AGwAaQBuAGUAIAB9AA=="
