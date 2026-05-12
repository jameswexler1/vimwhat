//go:build !windows

package ui

import (
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/xo/terminfo"
)

func prepareTerminalOutput() (TerminalReport, func() error) {
	report := TerminalReport{
		Platform:       runtime.GOOS,
		VTProcessing:   true,
		DelayedNewline: true,
	}
	applyUnixLastColumnGuard(&report, os.Getenv("TERM"), terminfo.LoadFromEnv)
	return report, func() error { return nil }
}

func applyUnixLastColumnGuard(report *TerminalReport, term string, load func() (*terminfo.Terminfo, error)) {
	if report == nil || load == nil {
		return
	}
	info, err := load()
	if err != nil {
		return
	}
	if !unixNeedsLastColumnGuard(info) {
		return
	}
	report.LastColumnGuard = true
	detail := "last-column guard enabled by terminfo am+xenl"
	if term = strings.TrimSpace(term); term != "" {
		detail = fmt.Sprintf("last-column guard enabled for TERM=%s by terminfo am+xenl", term)
	}
	report.Detail = joinTerminalDetails(report.Detail, detail)
}

func unixNeedsLastColumnGuard(info *terminfo.Terminfo) bool {
	return unixBoolCap(info, terminfo.AutoRightMargin) && unixBoolCap(info, terminfo.EatNewlineGlitch)
}

func unixBoolCap(info *terminfo.Terminfo, cap int) bool {
	return info != nil && info.Bools != nil && info.Bools[cap]
}
