//go:build windows

package media

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
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
	t.Setenv("TERM_PROGRAM", "")
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("VIMWHAT_FORCE_SIXEL", "")

	report := Detect("auto")
	if report.Selected != BackendExternal {
		t.Fatalf("Selected = %q, want %q", report.Selected, BackendExternal)
	}
	if report.Reasons[BackendUeberzugPP] != "ueberzug++ overlay unsupported on this platform" {
		t.Fatalf("ueberzug++ reason = %q", report.Reasons[BackendUeberzugPP])
	}
}

func TestDetectWindowsAutoPrefersChafaOverExternal(t *testing.T) {
	prevLookPath := lookPath
	lookPath = func(name string) (string, error) {
		switch name {
		case "rundll32.exe":
			return `C:\Windows\System32\rundll32.exe`, nil
		case "chafa":
			return `C:\Program Files\chafa\chafa.exe`, nil
		default:
			return "", errors.New("not found")
		}
	}
	t.Cleanup(func() {
		lookPath = prevLookPath
	})
	t.Setenv("TERM_PROGRAM", "")
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("VIMWHAT_FORCE_SIXEL", "")

	report := Detect("auto")
	if report.Selected != BackendChafa {
		t.Fatalf("Selected = %q, want %q", report.Selected, BackendChafa)
	}
	if report.Reasons[BackendChafa] != "available" {
		t.Fatalf("chafa reason = %q", report.Reasons[BackendChafa])
	}
}

func TestDetectWindowsExplicitExternalPreservesExternalOpener(t *testing.T) {
	prevLookPath := lookPath
	lookPath = func(name string) (string, error) {
		switch name {
		case "rundll32.exe":
			return `C:\Windows\System32\rundll32.exe`, nil
		case "chafa":
			return `C:\Program Files\chafa\chafa.exe`, nil
		default:
			return "", errors.New("not found")
		}
	}
	t.Cleanup(func() {
		lookPath = prevLookPath
	})
	t.Setenv("TERM_PROGRAM", "")
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("VIMWHAT_FORCE_SIXEL", "")

	report := Detect("external")
	if report.Selected != BackendExternal {
		t.Fatalf("Selected = %q, want %q", report.Selected, BackendExternal)
	}
}

func TestDetectWindowsExplicitChafaStillSelectsChafa(t *testing.T) {
	prevLookPath := lookPath
	lookPath = func(name string) (string, error) {
		switch name {
		case "rundll32.exe":
			return `C:\Windows\System32\rundll32.exe`, nil
		case "chafa":
			return `C:\Program Files\chafa\chafa.exe`, nil
		default:
			return "", errors.New("not found")
		}
	}
	t.Cleanup(func() {
		lookPath = prevLookPath
	})

	report := Detect("chafa")
	if report.Selected != BackendChafa {
		t.Fatalf("Selected = %q, want %q", report.Selected, BackendChafa)
	}
}

func TestDetectWindowsForcedSixelBeatsExternal(t *testing.T) {
	prevLookPath := lookPath
	lookPath = func(name string) (string, error) {
		switch name {
		case "rundll32.exe":
			return `C:\Windows\System32\rundll32.exe`, nil
		case "chafa":
			return `C:\Program Files\chafa\chafa.exe`, nil
		default:
			return "", errors.New("not found")
		}
	}
	t.Cleanup(func() {
		lookPath = prevLookPath
	})
	t.Setenv("VIMWHAT_FORCE_SIXEL", "1")

	report := Detect("auto")
	if report.Selected != BackendSixel {
		t.Fatalf("Selected = %q, want %q", report.Selected, BackendSixel)
	}
}

func TestDetectWindowsAutoPrefersSixelWhenWezTermSupportsSixel(t *testing.T) {
	prevLookPath := lookPath
	lookPath = func(name string) (string, error) {
		switch name {
		case "rundll32.exe":
			return `C:\Windows\System32\rundll32.exe`, nil
		case "img2sixel":
			return `C:\Program Files\libsixel\img2sixel.exe`, nil
		default:
			return "", errors.New("not found")
		}
	}
	t.Cleanup(func() {
		lookPath = prevLookPath
	})
	t.Setenv("VIMWHAT_FORCE_SIXEL", "")
	t.Setenv("TERM_PROGRAM", "wezterm")

	report := Detect("auto")
	if report.Selected != BackendSixel {
		t.Fatalf("Selected = %q, want %q", report.Selected, BackendSixel)
	}
	if report.Reasons[BackendSixel] != "available" {
		t.Fatalf("sixel reason = %q, want available", report.Reasons[BackendSixel])
	}
}

func TestDetectWindowsExplicitSixelSelectsSixel(t *testing.T) {
	prevLookPath := lookPath
	lookPath = func(name string) (string, error) {
		switch name {
		case "rundll32.exe":
			return `C:\Windows\System32\rundll32.exe`, nil
		case "img2sixel":
			return `C:\Program Files\libsixel\img2sixel.exe`, nil
		default:
			return "", errors.New("not found")
		}
	}
	t.Cleanup(func() {
		lookPath = prevLookPath
	})
	t.Setenv("VIMWHAT_FORCE_SIXEL", "")
	t.Setenv("TERM_PROGRAM", "wezterm")

	report := Detect("sixel")
	if report.Selected != BackendSixel {
		t.Fatalf("Selected = %q, want %q", report.Selected, BackendSixel)
	}
}

func TestDetectWindowsAutoSelectsSixelWithoutExternalOpener(t *testing.T) {
	prevLookPath := lookPath
	lookPath = func(name string) (string, error) {
		if name == "img2sixel" {
			return `C:\Program Files\libsixel\img2sixel.exe`, nil
		}
		return "", errors.New("not found")
	}
	t.Cleanup(func() {
		lookPath = prevLookPath
	})
	t.Setenv("VIMWHAT_FORCE_SIXEL", "")
	t.Setenv("TERM_PROGRAM", "wezterm")

	report := Detect("auto")
	if report.Selected != BackendSixel {
		t.Fatalf("Selected = %q, want %q", report.Selected, BackendSixel)
	}
	if report.Reasons[BackendSixel] != "available" {
		t.Fatalf("sixel reason = %q, want available", report.Reasons[BackendSixel])
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

func TestAvatarPreviewBackendAllowsSelectedSixelOnWindows(t *testing.T) {
	backend, ok := AvatarPreviewBackend(Report{Selected: BackendSixel})
	if !ok || backend != BackendSixel {
		t.Fatalf("AvatarPreviewBackend() = %q, %v; want %q, true", backend, ok, BackendSixel)
	}
}

func TestPreviewerReturnsSixelDisplayOnWindows(t *testing.T) {
	imagePath := filepath.Join(t.TempDir(), "photo.jpg")
	if err := os.WriteFile(imagePath, []byte("fake"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	prevLookPath := lookPath
	lookPath = func(name string) (string, error) {
		if name == "img2sixel" {
			return `C:\Program Files\libsixel\img2sixel.exe`, nil
		}
		return "", errors.New("not found")
	}
	t.Cleanup(func() {
		lookPath = prevLookPath
	})

	prevRun := runPreviewCommand
	runPreviewCommand = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		return []byte("\x1bPqfake\x1b\\\n"), nil
	}
	t.Cleanup(func() {
		runPreviewCommand = prevRun
	})

	previewer := NewPreviewer(Report{Selected: BackendSixel}, t.TempDir(), 20, 8)
	preview := previewer.Render(context.Background(), PreviewRequest{
		MessageID: "m-1",
		MIMEType:  "image/jpeg",
		LocalPath: imagePath,
		Width:     10,
		Height:    4,
	})
	if preview.Err != nil {
		t.Fatalf("Render() error = %v", preview.Err)
	}
	if preview.Display != PreviewDisplaySixel || !preview.Ready() {
		t.Fatalf("preview = %+v, want ready sixel display", preview)
	}
}

func TestPreviewerUsesDetectedCellPixelsForImg2SixelOnWindows(t *testing.T) {
	imagePath := filepath.Join(t.TempDir(), "photo.jpg")
	if err := os.WriteFile(imagePath, []byte("fake"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	prevLookPath := lookPath
	lookPath = func(name string) (string, error) {
		if name == "img2sixel" {
			return `C:\Program Files\libsixel\img2sixel.exe`, nil
		}
		return "", errors.New("not found")
	}
	t.Cleanup(func() {
		lookPath = prevLookPath
	})

	prevDetectCell := detectTerminalCellPixels
	detectTerminalCellPixels = func() (terminalCellPixels, bool) {
		return terminalCellPixels{Width: 9, Height: 17}, true
	}
	t.Cleanup(func() {
		detectTerminalCellPixels = prevDetectCell
	})

	var calledName string
	var calledArgs []string
	prevRun := runPreviewCommand
	runPreviewCommand = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		calledName = name
		calledArgs = slices.Clone(args)
		return []byte("\x1bPqfake\x1b\\\n"), nil
	}
	t.Cleanup(func() {
		runPreviewCommand = prevRun
	})

	previewer := NewPreviewer(Report{Selected: BackendSixel}, t.TempDir(), 20, 8)
	preview := previewer.Render(context.Background(), PreviewRequest{
		MessageID: "m-1",
		MIMEType:  "image/jpeg",
		LocalPath: imagePath,
		Width:     10,
		Height:    4,
	})

	if preview.Err != nil {
		t.Fatalf("Render() error = %v", preview.Err)
	}
	if calledName != "img2sixel" {
		t.Fatalf("called command = %q, want img2sixel", calledName)
	}
	if !slices.Contains(calledArgs, "-w") || !slices.Contains(calledArgs, "90") || !slices.Contains(calledArgs, "-h") || !slices.Contains(calledArgs, "68") {
		t.Fatalf("called args = %v, want -w 90 -h 68", calledArgs)
	}
}
