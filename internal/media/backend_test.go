package media

import (
	"errors"
	"testing"
)

func TestDetectExplicitAvailableBackend(t *testing.T) {
	prevLookPath := lookPath
	lookPath = func(name string) (string, error) {
		if name == "chafa" {
			return "/usr/bin/chafa", nil
		}
		return "", errors.New("not found")
	}
	t.Cleanup(func() {
		lookPath = prevLookPath
	})

	t.Setenv("TERM", "xterm-256color")
	t.Setenv("TERM_PROGRAM", "")
	t.Setenv("MAYBEWHATS_FORCE_SIXEL", "0")

	report := Detect("chafa")
	if report.Selected != BackendChafa {
		t.Fatalf("Selected = %q, want %q", report.Selected, BackendChafa)
	}
}

func TestDetectFallsBackInPriorityOrder(t *testing.T) {
	prevLookPath := lookPath
	lookPath = func(name string) (string, error) {
		switch name {
		case "ueberzugpp":
			return "/usr/bin/ueberzugpp", nil
		case "xdg-open":
			return "/usr/bin/xdg-open", nil
		default:
			return "", errors.New("not found")
		}
	}
	t.Cleanup(func() {
		lookPath = prevLookPath
	})

	t.Setenv("TERM", "xterm-256color")
	t.Setenv("TERM_PROGRAM", "")
	t.Setenv("MAYBEWHATS_FORCE_SIXEL", "0")

	report := Detect("auto")
	if report.Selected != BackendUeberzugPP {
		t.Fatalf("Selected = %q, want %q", report.Selected, BackendUeberzugPP)
	}
}

func TestDetectReportsNoneWhenNoBackendIsAvailable(t *testing.T) {
	prevLookPath := lookPath
	lookPath = func(name string) (string, error) {
		return "", errors.New("not found")
	}
	t.Cleanup(func() {
		lookPath = prevLookPath
	})

	t.Setenv("TERM", "xterm-256color")
	t.Setenv("TERM_PROGRAM", "")
	t.Setenv("MAYBEWHATS_FORCE_SIXEL", "0")

	report := Detect("auto")
	if report.Selected != BackendNone {
		t.Fatalf("Selected = %q, want %q", report.Selected, BackendNone)
	}
}
