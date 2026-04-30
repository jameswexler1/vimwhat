package ui

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
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

type lockedTerminalFile struct {
	file *os.File
	mu   sync.Mutex
}

func newLockedTerminalFile(file *os.File) *lockedTerminalFile {
	return &lockedTerminalFile{file: file}
}

func (f *lockedTerminalFile) Read(p []byte) (int, error) {
	return f.file.Read(p)
}

func (f *lockedTerminalFile) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.file.Write(p)
}

func (f *lockedTerminalFile) Close() error {
	return nil
}

func (f *lockedTerminalFile) Fd() uintptr {
	return f.file.Fd()
}
