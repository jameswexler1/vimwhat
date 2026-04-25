//go:build windows

package media

import (
	"os"
	"strings"
)

func currentPlatformCapabilities() platformCapabilities {
	sixelSupported := windowsSupportsSixel()
	sixelReason := ""
	if !sixelSupported {
		sixelReason = "Windows terminal sixel support not detected (run in WezTerm or set VIMWHAT_FORCE_SIXEL=1 for another Sixel-capable terminal)"
	}
	return platformCapabilities{
		Name:                        "windows",
		UeberzugPPOutput:            "",
		UeberzugPPUnavailableReason: "ueberzug++ overlays require X11/Wayland; unavailable on native Windows",
		SixelSupported:              sixelSupported,
		SixelUnsupportedReason:      sixelReason,
		ExternalCommands:            []string{"rundll32.exe", "explorer.exe", "powershell.exe", "pwsh.exe"},
		ExternalUnavailableReason:   "Windows opener command not found in PATH",
	}
}

func DetectUeberzugPPOutput() string {
	return ""
}

func windowsSupportsSixel() bool {
	return os.Getenv("VIMWHAT_FORCE_SIXEL") == "1" || windowsWezTermDetected()
}

func windowsWezTermDetected() bool {
	for _, name := range []string{"WEZTERM_PANE", "WEZTERM_EXECUTABLE", "WEZTERM_CONFIG_FILE"} {
		if strings.TrimSpace(os.Getenv(name)) != "" {
			return true
		}
	}

	term := strings.ToLower(os.Getenv("TERM"))
	program := strings.ToLower(os.Getenv("TERM_PROGRAM"))
	return strings.Contains(term, "wezterm") || strings.Contains(program, "wezterm")
}
