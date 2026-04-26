//go:build !windows

package config

func platformPrefersCompatEmoji(string) bool {
	return false
}
