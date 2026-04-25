//go:build windows

package media

import "testing"

func TestWindowsPlatformDetectsWezTermSixel(t *testing.T) {
	t.Setenv("VIMWHAT_FORCE_SIXEL", "0")
	t.Setenv("TERM", "")
	t.Setenv("TERM_PROGRAM", "")
	t.Setenv("WEZTERM_EXECUTABLE", "")
	t.Setenv("WEZTERM_CONFIG_FILE", "")
	t.Setenv("WEZTERM_PANE", "1")

	platform := currentPlatformCapabilities()
	if platform.Name != "windows" {
		t.Fatalf("platform name = %q, want windows", platform.Name)
	}
	if !platform.SixelSupported {
		t.Fatalf("SixelSupported = false, want true for WezTerm")
	}
	if platform.UeberzugPPOutput != "" {
		t.Fatalf("UeberzugPPOutput = %q, want empty on native Windows", platform.UeberzugPPOutput)
	}
}

func TestWindowsPlatformCanForceSixel(t *testing.T) {
	t.Setenv("VIMWHAT_FORCE_SIXEL", "1")
	t.Setenv("TERM", "")
	t.Setenv("TERM_PROGRAM", "")
	t.Setenv("WEZTERM_EXECUTABLE", "")
	t.Setenv("WEZTERM_CONFIG_FILE", "")
	t.Setenv("WEZTERM_PANE", "")

	platform := currentPlatformCapabilities()
	if !platform.SixelSupported {
		t.Fatalf("SixelSupported = false, want true when forced")
	}
}
