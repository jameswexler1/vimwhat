//go:build windows

package app

import (
	"os/exec"

	"vimwhat/internal/store"
)

func platformClipboardCommands() [][]string {
	return [][]string{{"clip.exe"}}
}

func platformImagePasteCommands(_ string) []imageClipboardCommand {
	return []imageClipboardCommand{{
		argv: []string{
			"powershell.exe",
			"-NoLogo",
			"-NoProfile",
			"-NonInteractive",
			"-STA",
			"-Command",
			windowsClipboardImagePasteCommand,
		},
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

const windowsClipboardImagePasteCommand = `
Add-Type -AssemblyName System.Windows.Forms
Add-Type -AssemblyName System.Drawing
function Write-ClipboardBytes([byte[]] $bytes) {
    if ($null -eq $bytes -or $bytes.Length -eq 0) { return }
    $stdout = [Console]::OpenStandardOutput()
    $stdout.Write($bytes, 0, $bytes.Length)
    $stdout.Flush()
    exit 0
}
function Write-ClipboardFileDrop([string] $path) {
    if ([string]::IsNullOrWhiteSpace($path)) { return }
    $prefix = 'VIMWHAT_FILEDROP:'
    $encoded = [Convert]::ToBase64String([System.Text.Encoding]::UTF8.GetBytes($path))
    $bytes = [System.Text.Encoding]::UTF8.GetBytes($prefix + $encoded)
    Write-ClipboardBytes $bytes
}
$img = [System.Windows.Forms.Clipboard]::GetImage()
if ($null -ne $img) {
    $ms = New-Object System.IO.MemoryStream
    try {
        $img.Save($ms, [System.Drawing.Imaging.ImageFormat]::Png)
        Write-ClipboardBytes ($ms.ToArray())
    } finally {
        $ms.Dispose()
        $img.Dispose()
    }
}
$data = [System.Windows.Forms.Clipboard]::GetDataObject()
if ($null -ne $data) {
    foreach ($format in @("PNG", "JFIF", "GIF", "image/png")) {
        if (-not $data.GetDataPresent($format)) { continue }
		$value = $data.GetData($format)
		if ($value -is [byte[]]) {
			Write-ClipboardBytes ([byte[]]$value)
		}
        if ($value -is [System.IO.Stream]) {
            $ms = New-Object System.IO.MemoryStream
            try {
                $value.CopyTo($ms)
                Write-ClipboardBytes ($ms.ToArray())
            } finally {
                $ms.Dispose()
            }
        }
    }
}
if ([System.Windows.Forms.Clipboard]::ContainsFileDropList()) {
    $files = [System.Windows.Forms.Clipboard]::GetFileDropList()
    if ($null -ne $files -and $files.Count -gt 0 -and [System.IO.File]::Exists($files[0])) {
        Write-ClipboardFileDrop $files[0]
    }
}
exit 2
`

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

func platformPrepareDetachedCommand(_ *exec.Cmd) {}

const windowsFilePickerEncodedCommand = "QQBkAGQALQBUAHkAcABlACAALQBBAHMAcwBlAG0AYgBsAHkATgBhAG0AZQAgAFMAeQBzAHQAZQBtAC4AVwBpAG4AZABvAHcAcwAuAEYAbwByAG0AcwA7ACAAJABkAGkAYQBsAG8AZwAgAD0AIABOAGUAdwAtAE8AYgBqAGUAYwB0ACAAUwB5AHMAdABlAG0ALgBXAGkAbgBkAG8AdwBzAC4ARgBvAHIAbQBzAC4ATwBwAGUAbgBGAGkAbABlAEQAaQBhAGwAbwBnADsAIABpAGYAIAAoACQAZABpAGEAbABvAGcALgBTAGgAbwB3AEQAaQBhAGwAbwBnACgAKQAgAC0AZQBxACAAIgBPAEsAIgApACAAewAgAFMAZQB0AC0AQwBvAG4AdABlAG4AdAAgAC0ATABpAHQAZQByAGEAbABQAGEAdABoACAAJABhAHIAZwBzAFsAMABdACAALQBWAGEAbAB1AGUAIAAkAGQAaQBhAGwAbwBnAC4ARgBpAGwAZQBOAGEAbQBlACAALQBOAG8ATgBlAHcAbABpAG4AZQAgAH0A"

const windowsStickerPickerEncodedCommand = "QQBkAGQALQBUAHkAcABlACAALQBBAHMAcwBlAG0AYgBsAHkATgBhAG0AZQAgAFMAeQBzAHQAZQBtAC4AVwBpAG4AZABvAHcAcwAuAEYAbwByAG0AcwA7ACAAJABkAGkAYQBsAG8AZwAgAD0AIABOAGUAdwAtAE8AYgBqAGUAYwB0ACAAUwB5AHMAdABlAG0ALgBXAGkAbgBkAG8AdwBzAC4ARgBvAHIAbQBzAC4ATwBwAGUAbgBGAGkAbABlAEQAaQBhAGwAbwBnADsAIAAkAGQAaQBhAGwAbwBnAC4ASQBuAGkAdABpAGEAbABEAGkAcgBlAGMAdABvAHIAeQAgAD0AIAAkAGEAcgBnAHMAWwAxAF0AOwAgACQAZABpAGEAbABvAGcALgBGAGkAbAB0AGUAcgAgAD0AIAAiAEkAbQBhAGcAZQBzAHwAKgAuAHcAZQBiAHAAOwAqAC4AcABuAGcAOwAqAC4AagBwAGcAOwAqAC4AagBwAGUAZwA7ACoALgBnAGkAZgB8AEEAbABsACAAZgBpAGwAZQBzAHwAKgAuACoAIgA7ACAAaQBmACAAKAAkAGQAaQBhAGwAbwBnAC4AUwBoAG8AdwBEAGkAYQBsAG8AZwAoACkAIAAtAGUAcQAgACIATwBLACIAKQAgAHsAIABTAGUAdAAtAEMAbwBuAHQAZQBuAHQAIAAtAEwAaQB0AGUAcgBhAGwAUABhAHQAaAAgACQAYQByAGcAcwBbADAAXQAgAC0AVgBhAGwAdQBlACAAJABkAGkAYQBsAG8AZwAuAEYAaQBsAGUATgBhAG0AZQAgAC0ATgBvAE4AZQB3AGwAaQBuAGUAIAB9AA=="
