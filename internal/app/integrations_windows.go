//go:build windows

package app

import "vimwhat/internal/store"

func platformClipboardCommands() [][]string {
	return [][]string{{"clip.exe"}}
}

func platformImagePasteCommands(mediaDir string) []imageClipboardCommand {
	target := clipboardImagePath(mediaDir, ".png")
	return []imageClipboardCommand{{
		argv: []string{
			"powershell.exe",
			"-NoLogo",
			"-NoProfile",
			"-NonInteractive",
			"-STA",
			"-Command",
			"Add-Type -AssemblyName System.Windows.Forms; Add-Type -AssemblyName System.Drawing; $img = [System.Windows.Forms.Clipboard]::GetImage(); if ($null -eq $img) { exit 2 }; $img.Save($args[0], [System.Drawing.Imaging.ImageFormat]::Png)",
			target,
		},
		pathMode: true,
		path:     target,
	}}
}

func platformImageCopyCommands(path string, _ string) []imageClipboardCommand {
	return []imageClipboardCommand{{
		argv: []string{
			"powershell.exe",
			"-NoLogo",
			"-NoProfile",
			"-NonInteractive",
			"-STA",
			"-Command",
			"Add-Type -AssemblyName System.Windows.Forms; Add-Type -AssemblyName System.Drawing; $img = [System.Drawing.Image]::FromFile($args[0]); try { [System.Windows.Forms.Clipboard]::SetImage($img) } finally { $img.Dispose() }",
			path,
		},
		pathMode: true,
	}}
}

func platformDefaultFilePickerCommand() string {
	return "powershell.exe -NoLogo -NoProfile -NonInteractive -STA -EncodedCommand " + windowsFilePickerEncodedCommand + " {chooser}"
}

func platformDefaultStickerPickerCommand() string {
	return "powershell.exe -NoLogo -NoProfile -NonInteractive -STA -EncodedCommand " + windowsStickerPickerEncodedCommand + " {chooser} {dir}"
}

func platformDefaultAudioPlayerCommand() string {
	return "rundll32.exe url.dll,FileProtocolHandler {path}"
}

func platformAutoOpenCommands(_ store.MediaMetadata, path string) [][]string {
	return [][]string{
		{"rundll32.exe", "url.dll,FileProtocolHandler", path},
		{"explorer.exe", path},
	}
}

const windowsFilePickerEncodedCommand = "QQBkAGQALQBUAHkAcABlACAALQBBAHMAcwBlAG0AYgBsAHkATgBhAG0AZQAgAFMAeQBzAHQAZQBtAC4AVwBpAG4AZABvAHcAcwAuAEYAbwByAG0AcwA7ACAAJABkAGkAYQBsAG8AZwAgAD0AIABOAGUAdwAtAE8AYgBqAGUAYwB0ACAAUwB5AHMAdABlAG0ALgBXAGkAbgBkAG8AdwBzAC4ARgBvAHIAbQBzAC4ATwBwAGUAbgBGAGkAbABlAEQAaQBhAGwAbwBnADsAIABpAGYAIAAoACQAZABpAGEAbABvAGcALgBTAGgAbwB3AEQAaQBhAGwAbwBnACgAKQAgAC0AZQBxACAAIgBPAEsAIgApACAAewAgAFMAZQB0AC0AQwBvAG4AdABlAG4AdAAgAC0ATABpAHQAZQByAGEAbABQAGEAdABoACAAJABhAHIAZwBzAFsAMABdACAALQBWAGEAbAB1AGUAIAAkAGQAaQBhAGwAbwBnAC4ARgBpAGwAZQBOAGEAbQBlACAALQBOAG8ATgBlAHcAbABpAG4AZQAgAH0A"

const windowsStickerPickerEncodedCommand = "QQBkAGQALQBUAHkAcABlACAALQBBAHMAcwBlAG0AYgBsAHkATgBhAG0AZQAgAFMAeQBzAHQAZQBtAC4AVwBpAG4AZABvAHcAcwAuAEYAbwByAG0AcwA7ACAAJABkAGkAYQBsAG8AZwAgAD0AIABOAGUAdwAtAE8AYgBqAGUAYwB0ACAAUwB5AHMAdABlAG0ALgBXAGkAbgBkAG8AdwBzAC4ARgBvAHIAbQBzAC4ATwBwAGUAbgBGAGkAbABlAEQAaQBhAGwAbwBnADsAIAAkAGQAaQBhAGwAbwBnAC4ASQBuAGkAdABpAGEAbABEAGkAcgBlAGMAdABvAHIAeQAgAD0AIAAkAGEAcgBnAHMAWwAxAF0AOwAgACQAZABpAGEAbABvAGcALgBGAGkAbAB0AGUAcgAgAD0AIAAiAEkAbQBhAGcAZQBzAHwAKgAuAHcAZQBiAHAAOwAqAC4AcABuAGcAOwAqAC4AagBwAGcAOwAqAC4AagBwAGUAZwA7ACoALgBnAGkAZgB8AEEAbABsACAAZgBpAGwAZQBzAHwAKgAuACoAIgA7ACAAaQBmACAAKAAkAGQAaQBhAGwAbwBnAC4AUwBoAG8AdwBEAGkAYQBsAG8AZwAoACkAIAAtAGUAcQAgACIATwBLACIAKQAgAHsAIABTAGUAdAAtAEMAbwBuAHQAZQBuAHQAIAAtAEwAaQB0AGUAcgBhAGwAUABhAHQAaAAgACQAYQByAGcAcwBbADAAXQAgAC0AVgBhAGwAdQBlACAAJABkAGkAYQBsAG8AZwAuAEYAaQBsAGUATgBhAG0AZQAgAC0ATgBvAE4AZQB3AGwAaQBuAGUAIAB9AA=="
