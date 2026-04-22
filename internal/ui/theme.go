package ui

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/charmbracelet/lipgloss"
)

type theme struct {
	SoftFG       lipgloss.Color
	PrimaryFG    lipgloss.Color
	AccentFG     lipgloss.Color
	WarnFG       lipgloss.Color
	Border       lipgloss.Color
	ActiveBorder lipgloss.Color
	OutgoingFG   lipgloss.Color
	IncomingLine lipgloss.Color
	OutgoingLine lipgloss.Color
	SelectedLine lipgloss.Color
	FocusedLine  lipgloss.Color
	BarBG        lipgloss.Color
	InsertModeBG lipgloss.Color
}

var uiTheme = loadTheme()

func loadTheme() theme {
	fallback := defaultTheme()

	home, err := os.UserHomeDir()
	if err != nil {
		return fallback
	}

	path := filepath.Join(home, ".cache", "wal", "colors.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return fallback
	}

	var wal struct {
		Special map[string]string `json:"special"`
		Colors  map[string]string `json:"colors"`
	}
	if err := json.Unmarshal(data, &wal); err != nil {
		return fallback
	}

	color := func(section map[string]string, key string, fallback lipgloss.Color) lipgloss.Color {
		if value := section[key]; value != "" {
			return lipgloss.Color(value)
		}
		return fallback
	}

	return theme{
		SoftFG:       color(wal.Colors, "color8", fallback.SoftFG),
		PrimaryFG:    color(wal.Special, "foreground", fallback.PrimaryFG),
		AccentFG:     color(wal.Colors, "color4", fallback.AccentFG),
		WarnFG:       color(wal.Colors, "color3", fallback.WarnFG),
		Border:       color(wal.Colors, "color8", fallback.Border),
		ActiveBorder: color(wal.Colors, "color4", fallback.ActiveBorder),
		OutgoingFG:   color(wal.Colors, "color10", fallback.OutgoingFG),
		IncomingLine: color(wal.Colors, "color8", fallback.IncomingLine),
		OutgoingLine: color(wal.Colors, "color2", fallback.OutgoingLine),
		SelectedLine: color(wal.Colors, "color3", fallback.SelectedLine),
		FocusedLine:  color(wal.Colors, "color6", fallback.FocusedLine),
		BarBG:        color(wal.Colors, "color0", fallback.BarBG),
		InsertModeBG: color(wal.Colors, "color5", fallback.InsertModeBG),
	}
}

func defaultTheme() theme {
	return theme{
		SoftFG:       lipgloss.Color("#9AA5B1"),
		PrimaryFG:    lipgloss.Color("#F5F7FA"),
		AccentFG:     lipgloss.Color("#7ED7C1"),
		WarnFG:       lipgloss.Color("#F4D35E"),
		Border:       lipgloss.Color("#2B3A42"),
		ActiveBorder: lipgloss.Color("#7ED7C1"),
		OutgoingFG:   lipgloss.Color("#C7F9CC"),
		IncomingLine: lipgloss.Color("#4B6472"),
		OutgoingLine: lipgloss.Color("#2EA56F"),
		SelectedLine: lipgloss.Color("#F4D35E"),
		FocusedLine:  lipgloss.Color("#48CAE4"),
		BarBG:        lipgloss.Color("#101418"),
		InsertModeBG: lipgloss.Color("#FF5C8A"),
	}
}

func barsTransparent() bool {
	switch os.Getenv("MAYBEWHATS_TRANSPARENT_BARS") {
	case "0", "false", "FALSE", "no", "NO":
		return false
	default:
		return true
	}
}
