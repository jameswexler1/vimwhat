//go:build !windows

package media

import "os"

func platformSupportsUeberzugPP() bool {
	return true
}

func platformUeberzugPPOutput() string {
	if os.Getenv("DISPLAY") != "" {
		return "x11"
	}
	if os.Getenv("WAYLAND_DISPLAY") != "" {
		return "wayland"
	}
	return ""
}

func platformExternalOpenerAvailable() bool {
	return hasCommand("xdg-open")
}

func platformExternalOpenerUnavailableReason() string {
	return "xdg-open not found in PATH"
}

func platformBackendOrder() []Backend {
	return []Backend{BackendSixel, BackendUeberzugPP, BackendChafa, BackendExternal}
}
