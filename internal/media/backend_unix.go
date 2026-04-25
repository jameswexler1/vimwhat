//go:build !windows

package media

import (
	"os"
	"strings"
)

func currentPlatformCapabilities() platformCapabilities {
	return platformCapabilities{
		Name:                        "unix",
		UeberzugPPOutput:            DetectUeberzugPPOutput(),
		UeberzugPPUnavailableReason: "DISPLAY/WAYLAND_DISPLAY not set",
		SixelSupported:              terminalSupportsSixel(),
		SixelUnsupportedReason:      "terminal sixel support not detected",
		ExternalCommands:            []string{"xdg-open"},
		ExternalUnavailableReason:   "xdg-open not found in PATH",
	}
}

func DetectUeberzugPPOutput() string {
	if os.Getenv("DISPLAY") != "" {
		return "x11"
	}
	if os.Getenv("WAYLAND_DISPLAY") != "" {
		return "wayland"
	}
	return ""
}

func terminalSupportsSixel() bool {
	if os.Getenv("VIMWHAT_FORCE_SIXEL") == "1" {
		return true
	}

	term := strings.ToLower(os.Getenv("TERM"))
	program := strings.ToLower(os.Getenv("TERM_PROGRAM"))

	return strings.Contains(term, "sixel") ||
		strings.Contains(program, "wezterm") ||
		strings.TrimSpace(os.Getenv("WEZTERM_PANE")) != ""
}
