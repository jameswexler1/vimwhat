//go:build windows

package media

import (
	"errors"
	"testing"
)

func TestDetectWindowsExternalOpener(t *testing.T) {
	prevLookPath := lookPath
	lookPath = func(name string) (string, error) {
		if name == "rundll32.exe" {
			return `C:\Windows\System32\rundll32.exe`, nil
		}
		return "", errors.New("not found")
	}
	t.Cleanup(func() {
		lookPath = prevLookPath
	})

	report := Detect("auto")
	if report.Selected != BackendExternal {
		t.Fatalf("Selected = %q, want %q", report.Selected, BackendExternal)
	}
	if report.Reasons[BackendUeberzugPP] != "ueberzug++ overlay unsupported on this platform" {
		t.Fatalf("ueberzug++ reason = %q", report.Reasons[BackendUeberzugPP])
	}
}

func TestDetectWindowsNoBackend(t *testing.T) {
	prevLookPath := lookPath
	lookPath = func(name string) (string, error) {
		return "", errors.New("not found")
	}
	t.Cleanup(func() {
		lookPath = prevLookPath
	})

	report := Detect("auto")
	if report.Selected != BackendNone {
		t.Fatalf("Selected = %q, want %q", report.Selected, BackendNone)
	}
	if report.Reasons[BackendExternal] != "rundll32.exe/explorer.exe not found in PATH" {
		t.Fatalf("external reason = %q", report.Reasons[BackendExternal])
	}
}
