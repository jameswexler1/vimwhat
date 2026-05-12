//go:build !windows

package ui

import (
	"errors"
	"strings"
	"testing"

	"github.com/xo/terminfo"
)

func TestUnixLastColumnGuardUsesTerminfoXenl(t *testing.T) {
	report := TerminalReport{Platform: "linux", VTProcessing: true, DelayedNewline: true}

	applyUnixLastColumnGuard(&report, "st-256color", func() (*terminfo.Terminfo, error) {
		return &terminfo.Terminfo{Bools: map[int]bool{
			terminfo.AutoRightMargin:  true,
			terminfo.EatNewlineGlitch: true,
		}}, nil
	})

	if !report.LastColumnGuard {
		t.Fatal("LastColumnGuard = false, want enabled for am+xenl terminal")
	}
	if !strings.Contains(report.Detail, "TERM=st-256color") || !strings.Contains(report.Detail, "am+xenl") {
		t.Fatalf("Detail = %q, want terminfo guard context", report.Detail)
	}
}

func TestUnixLastColumnGuardRequiresXenl(t *testing.T) {
	report := TerminalReport{Platform: "linux", VTProcessing: true, DelayedNewline: true}

	applyUnixLastColumnGuard(&report, "xterm-256color", func() (*terminfo.Terminfo, error) {
		return &terminfo.Terminfo{Bools: map[int]bool{
			terminfo.AutoRightMargin: true,
		}}, nil
	})

	if report.LastColumnGuard {
		t.Fatal("LastColumnGuard = true, want disabled without xenl")
	}
	if report.Detail != "" {
		t.Fatalf("Detail = %q, want empty without guard", report.Detail)
	}
}

func TestUnixLastColumnGuardIgnoresTerminfoLoadFailure(t *testing.T) {
	report := TerminalReport{Platform: "linux", VTProcessing: true, DelayedNewline: true}

	applyUnixLastColumnGuard(&report, "unknown", func() (*terminfo.Terminfo, error) {
		return nil, errors.New("missing terminfo")
	})

	if report.LastColumnGuard {
		t.Fatal("LastColumnGuard = true, want disabled when terminfo cannot be loaded")
	}
	if report.Detail != "" {
		t.Fatalf("Detail = %q, want empty on load failure", report.Detail)
	}
}
