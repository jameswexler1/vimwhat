//go:build windows

package media

import "os"

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

func platformBackendOrder() []Backend {
	if os.Getenv("VIMWHAT_FORCE_SIXEL") == "1" {
		return []Backend{BackendSixel, BackendExternal, BackendChafa, BackendUeberzugPP}
	}
	return []Backend{BackendExternal, BackendChafa, BackendUeberzugPP}
}
