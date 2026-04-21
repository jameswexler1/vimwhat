package media

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

type Backend string

const (
	BackendAuto       Backend = "auto"
	BackendSixel      Backend = "sixel"
	BackendUeberzugPP Backend = "ueberzug++"
	BackendChafa      Backend = "chafa"
	BackendExternal   Backend = "external"
	BackendNone       Backend = "none"
)

type Report struct {
	Requested Backend
	Selected  Backend
	Reasons   map[Backend]string
}

var lookPath = exec.LookPath

func Detect(requested string) Report {
	normalized := normalize(requested)
	report := Report{
		Requested: normalized,
		Reasons:   make(map[Backend]string),
	}
	if normalized == BackendNone {
		report.Selected = BackendNone
		return report
	}

	available := detectAvailable()
	order := []Backend{BackendSixel, BackendUeberzugPP, BackendChafa, BackendExternal}

	for _, backend := range order {
		if available[backend] {
			report.Reasons[backend] = "available"
			continue
		}
		report.Reasons[backend] = unavailableReason(backend)
	}

	if normalized != BackendAuto && available[normalized] {
		report.Selected = normalized
		return report
	}

	for _, backend := range order {
		if available[backend] {
			report.Selected = backend
			return report
		}
	}

	report.Selected = BackendNone
	return report
}

func unavailableReason(backend Backend) string {
	switch backend {
	case BackendSixel:
		return "terminal sixel support not detected"
	case BackendUeberzugPP:
		return "ueberzugpp not found in PATH"
	case BackendChafa:
		return "chafa not found in PATH"
	case BackendExternal:
		return "xdg-open not found in PATH"
	default:
		return "not available"
	}
}

func detectAvailable() map[Backend]bool {
	return map[Backend]bool{
		BackendSixel:      supportsSixel(),
		BackendUeberzugPP: hasCommand("ueberzugpp"),
		BackendChafa:      hasCommand("chafa"),
		BackendExternal:   hasCommand("xdg-open"),
	}
}

func supportsSixel() bool {
	if os.Getenv("MAYBEWHATS_FORCE_SIXEL") == "1" {
		return true
	}

	term := strings.ToLower(os.Getenv("TERM"))
	program := strings.ToLower(os.Getenv("TERM_PROGRAM"))

	return strings.Contains(term, "sixel") || strings.Contains(program, "wezterm")
}

func hasCommand(name string) bool {
	_, err := lookPath(name)
	return err == nil
}

func normalize(input string) Backend {
	switch strings.ToLower(strings.TrimSpace(input)) {
	case "", "auto":
		return BackendAuto
	case "sixel":
		return BackendSixel
	case "ueberzug++", "ueberzugpp":
		return BackendUeberzugPP
	case "chafa":
		return BackendChafa
	case "external":
		return BackendExternal
	case "none":
		return BackendNone
	default:
		return BackendAuto
	}
}

func (r Report) Lines() []string {
	lines := []string{
		fmt.Sprintf("requested preview backend: %s", r.Requested),
		fmt.Sprintf("selected preview backend: %s", r.Selected),
	}

	for _, backend := range []Backend{BackendSixel, BackendUeberzugPP, BackendChafa, BackendExternal} {
		lines = append(lines, fmt.Sprintf("%s: %s", backend, r.Reasons[backend]))
	}

	return lines
}
