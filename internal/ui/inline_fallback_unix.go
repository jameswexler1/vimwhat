//go:build !windows

package ui

func platformAllowsInlineFallback() bool {
	return false
}
