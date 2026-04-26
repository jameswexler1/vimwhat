//go:build windows

package config

import (
	"os"
	"strings"
)

func platformPrefersCompatEmoji(term string) bool {
	term = strings.ToLower(strings.TrimSpace(term))
	if strings.Contains(term, "kitty") {
		return false
	}
	program := strings.ToLower(strings.TrimSpace(os.Getenv("TERM_PROGRAM")))
	if strings.Contains(program, "wezterm") {
		return false
	}
	return true
}
