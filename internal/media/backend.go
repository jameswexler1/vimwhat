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
	Requested        Backend
	Selected         Backend
	Reasons          map[Backend]string
	UeberzugPPOutput string
}

var lookPath = exec.LookPath

func Detect(requested string) Report {
	normalized := normalize(requested)
	report := Report{
		Requested:        normalized,
		Reasons:          make(map[Backend]string),
		UeberzugPPOutput: DetectUeberzugPPOutput(),
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
		if !supportsSixel() {
			return "terminal sixel support not detected"
		}
		return "sixel renderer command not found"
	case BackendUeberzugPP:
		if !hasCommand("ueberzugpp") {
			return "ueberzugpp not found in PATH"
		}
		if DetectUeberzugPPOutput() == "" {
			return "DISPLAY/WAYLAND_DISPLAY not set"
		}
		return "ueberzugpp not available"
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
		BackendSixel:      supportsSixel() && (hasCommand("chafa") || hasCommand("img2sixel")),
		BackendUeberzugPP: hasCommand("ueberzugpp") && DetectUeberzugPPOutput() != "",
		BackendChafa:      hasCommand("chafa"),
		BackendExternal:   hasCommand("xdg-open"),
	}
}

func DetectUeberzugPPOutput() string {
	if os.Getenv("DISPLAY") != "" {
		return "x11"
	}
	if os.Getenv("WAYLAND_DISPLAY") != "" {
		return "wayland"
	}
	return ""
}

func supportsSixel() bool {
	if os.Getenv("VIMWHAT_FORCE_SIXEL") == "1" {
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
	adapter := r.UeberzugPPOutput
	if adapter == "" {
		adapter = "none"
	}
	lines = append(lines, fmt.Sprintf("ueberzug++ adapter: %s", adapter))

	for _, backend := range []Backend{BackendSixel, BackendUeberzugPP, BackendChafa, BackendExternal} {
		lines = append(lines, fmt.Sprintf("%s: %s", backend, r.Reasons[backend]))
	}
	chafaRole := "unavailable"
	if r.Reasons[BackendChafa] == "available" {
		if r.Selected == BackendChafa {
			chafaRole = "selected renderer"
		} else {
			chafaRole = "fallback only"
		}
	}
	lines = append(lines, fmt.Sprintf("chafa role: %s", chafaRole))
	lines = append(lines, fmt.Sprintf("preview quality path: %s", r.QualityPath()))

	return lines
}

func (r Report) QualityPath() string {
	switch r.Selected {
	case BackendUeberzugPP:
		adapter := r.UeberzugPPOutput
		if adapter == "" {
			adapter = "unknown"
		}
		return "pixel overlay via ueberzug++ " + adapter
	case BackendSixel:
		return "pixel output via sixel"
	case BackendChafa:
		if r.Requested == BackendChafa && r.Reasons[BackendUeberzugPP] == "available" {
			return "symbol fallback forced by preview_backend=chafa; ueberzug++ is available"
		}
		return "symbol fallback via chafa"
	case BackendExternal:
		return "external opener only"
	case BackendNone:
		return "inline previews disabled"
	default:
		return "unknown"
	}
}
