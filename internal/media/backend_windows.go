//go:build windows

package media

func platformSupportsUeberzugPP() bool {
	return false
}

func platformUeberzugPPOutput() string {
	return ""
}

func platformExternalOpenerAvailable() bool {
	return hasCommand("rundll32.exe") || hasCommand("explorer.exe")
}

func platformExternalOpenerUnavailableReason() string {
	return "rundll32.exe/explorer.exe not found in PATH"
}
