package ui

import (
	"fmt"
	"runtime"
	"strings"
)

type TerminalReport struct {
	Platform           string
	StdoutConsole      bool
	StdoutConsoleKnown bool
	VTProcessing       bool
	DelayedNewline     bool
	LastColumnGuard    bool
	Detail             string
}

func ProbeTerminalOutput() TerminalReport {
	report, restore := prepareTerminalOutput()
	if restore != nil {
		if err := restore(); err != nil {
			report.Detail = joinTerminalDetails(report.Detail, fmt.Sprintf("restore failed: %v", err))
		}
	}
	return report
}

func (r TerminalReport) Lines() []string {
	platform := strings.TrimSpace(r.Platform)
	if platform == "" {
		platform = runtime.GOOS
	}
	lines := []string{fmt.Sprintf(
		"terminal output: platform=%s stdout_console=%s vt=%s delayed_newline=%s last_column_guard=%s",
		platform,
		terminalConsoleLabel(r),
		terminalYesNo(r.VTProcessing),
		terminalYesNo(r.DelayedNewline),
		terminalYesNo(r.LastColumnGuard),
	)}
	if detail := strings.TrimSpace(r.Detail); detail != "" {
		lines = append(lines, fmt.Sprintf("terminal output detail: %s", detail))
	}
	return lines
}

func terminalConsoleLabel(report TerminalReport) string {
	if !report.StdoutConsoleKnown {
		return "auto"
	}
	return terminalYesNo(report.StdoutConsole)
}

func terminalYesNo(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}

func joinTerminalDetails(values ...string) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			parts = append(parts, value)
		}
	}
	return strings.Join(parts, "; ")
}
