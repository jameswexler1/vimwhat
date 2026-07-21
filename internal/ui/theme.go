package ui

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

type theme struct {
	SoftFG               lipgloss.Color
	PrimaryFG            lipgloss.Color
	AccentFG             lipgloss.Color
	WarnFG               lipgloss.Color
	SearchMatchFG        lipgloss.Color
	SearchCurrentMatchFG lipgloss.Color
	SearchCurrentMatchBG lipgloss.Color
	Border               lipgloss.Color
	ActiveBorder         lipgloss.Color
	OutgoingFG           lipgloss.Color
	IncomingLine         lipgloss.Color
	OutgoingLine         lipgloss.Color
	SelectedLine         lipgloss.Color
	FocusedLine          lipgloss.Color
	BarBG                lipgloss.Color
	InsertModeBG         lipgloss.Color
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
		SoftFG:               color(wal.Colors, "color8", fallback.SoftFG),
		PrimaryFG:            color(wal.Special, "foreground", fallback.PrimaryFG),
		AccentFG:             color(wal.Colors, "color4", fallback.AccentFG),
		WarnFG:               color(wal.Colors, "color3", fallback.WarnFG),
		SearchMatchFG:        color(wal.Colors, "color3", fallback.SearchMatchFG),
		SearchCurrentMatchFG: color(wal.Special, "foreground", fallback.SearchCurrentMatchFG),
		SearchCurrentMatchBG: color(wal.Colors, "color4", fallback.SearchCurrentMatchBG),
		Border:               color(wal.Colors, "color8", fallback.Border),
		ActiveBorder:         color(wal.Colors, "color4", fallback.ActiveBorder),
		OutgoingFG:           color(wal.Colors, "color10", fallback.OutgoingFG),
		IncomingLine:         color(wal.Colors, "color8", fallback.IncomingLine),
		OutgoingLine:         color(wal.Colors, "color2", fallback.OutgoingLine),
		SelectedLine:         color(wal.Colors, "color3", fallback.SelectedLine),
		FocusedLine:          color(wal.Colors, "color6", fallback.FocusedLine),
		BarBG:                color(wal.Colors, "color0", fallback.BarBG),
		InsertModeBG:         color(wal.Colors, "color5", fallback.InsertModeBG),
	}
}

func defaultTheme() theme {
	return theme{
		SoftFG:               lipgloss.Color("#9AA5B1"),
		PrimaryFG:            lipgloss.Color("#F5F7FA"),
		AccentFG:             lipgloss.Color("#7ED7C1"),
		WarnFG:               lipgloss.Color("#F4D35E"),
		SearchMatchFG:        lipgloss.Color("#F4D35E"),
		SearchCurrentMatchFG: lipgloss.Color("#F5F7FA"),
		SearchCurrentMatchBG: lipgloss.Color("#7ED7C1"),
		Border:               lipgloss.Color("#2B3A42"),
		ActiveBorder:         lipgloss.Color("#7ED7C1"),
		OutgoingFG:           lipgloss.Color("#C7F9CC"),
		IncomingLine:         lipgloss.Color("#4B6472"),
		OutgoingLine:         lipgloss.Color("#2EA56F"),
		SelectedLine:         lipgloss.Color("#F4D35E"),
		FocusedLine:          lipgloss.Color("#48CAE4"),
		BarBG:                lipgloss.Color("#101418"),
		InsertModeBG:         lipgloss.Color("#FF5C8A"),
	}
}

func barsTransparent() bool {
	switch os.Getenv("VIMWHAT_TRANSPARENT_BARS") {
	case "0", "false", "FALSE", "no", "NO":
		return false
	default:
		return true
	}
}

func contrastingTextColor(background, first, second lipgloss.Color) lipgloss.Color {
	backgroundLuminance, ok := colorLuminance(background)
	if !ok {
		return first
	}
	firstLuminance, firstOK := colorLuminance(first)
	secondLuminance, secondOK := colorLuminance(second)
	switch {
	case !firstOK && !secondOK:
		return first
	case !firstOK:
		return second
	case !secondOK:
		return first
	case contrastRatio(backgroundLuminance, secondLuminance) > contrastRatio(backgroundLuminance, firstLuminance):
		return second
	default:
		return first
	}
}

func colorLuminance(color lipgloss.Color) (float64, bool) {
	rgb, ok := parseHexColor(color)
	if !ok {
		return 0, false
	}
	component := func(channel uint8) float64 {
		value := float64(channel) / 255
		if value <= 0.04045 {
			return value / 12.92
		}
		return math.Pow((value+0.055)/1.055, 2.4)
	}
	red := component(rgb.red)
	green := component(rgb.green)
	blue := component(rgb.blue)
	return 0.2126*red + 0.7152*green + 0.0722*blue, true
}

type themeRGB struct {
	red   uint8
	green uint8
	blue  uint8
}

func parseHexColor(color lipgloss.Color) (themeRGB, bool) {
	value := strings.TrimPrefix(strings.TrimSpace(string(color)), "#")
	if len(value) == 3 {
		value = strings.Repeat(value[0:1], 2) + strings.Repeat(value[1:2], 2) + strings.Repeat(value[2:3], 2)
	}
	if len(value) != 6 {
		return themeRGB{}, false
	}
	rgb, err := strconv.ParseUint(value, 16, 32)
	if err != nil {
		return themeRGB{}, false
	}
	return themeRGB{
		red:   uint8((rgb >> 16) & 0xff),
		green: uint8((rgb >> 8) & 0xff),
		blue:  uint8(rgb & 0xff),
	}, true
}

func blendColors(base, accent lipgloss.Color, accentWeight float64) lipgloss.Color {
	baseRGB, baseOK := parseHexColor(base)
	accentRGB, accentOK := parseHexColor(accent)
	if !baseOK || !accentOK {
		return accent
	}
	accentWeight = min(1, max(0, accentWeight))
	baseWeight := 1 - accentWeight
	blend := func(base, accent uint8) uint8 {
		return uint8(math.Round(float64(base)*baseWeight + float64(accent)*accentWeight))
	}
	return lipgloss.Color(fmt.Sprintf(
		"#%02X%02X%02X",
		blend(baseRGB.red, accentRGB.red),
		blend(baseRGB.green, accentRGB.green),
		blend(baseRGB.blue, accentRGB.blue),
	))
}

func contrastRatio(first, second float64) float64 {
	if first < second {
		first, second = second, first
	}
	return (first + 0.05) / (second + 0.05)
}
