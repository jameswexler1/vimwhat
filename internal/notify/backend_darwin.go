//go:build darwin

package notify

import (
	"context"
	"fmt"
)

func detectLinuxDBus() (bool, string) {
	return false, fmt.Sprintf("unsupported on %s", runtimeGOOS)
}

func detectMacOS() (bool, string) {
	if runtimeGOOS != "darwin" {
		return false, fmt.Sprintf("unsupported on %s", runtimeGOOS)
	}
	if _, err := lookPath("osascript"); err != nil {
		return false, "osascript not found in PATH"
	}
	return true, "available via osascript"
}

func detectWindows() (bool, string) {
	return false, fmt.Sprintf("unsupported on %s", runtimeGOOS)
}

func sendLinuxDBus(context.Context, Notification) error {
	return fmt.Errorf("Linux D-Bus notifications are unsupported on %s", runtimeGOOS)
}

func sendMacOSNotification(ctx context.Context, note Notification) error {
	if _, err := lookPath("osascript"); err != nil {
		return fmt.Errorf("osascript not found in PATH")
	}
	title := sanitizeNotificationText(note.Title, 96, "vimwhat")
	body := sanitizeNotificationText(note.Body, 220, "")
	script := []string{
		"-e", "on run argv",
		"-e", "set noteTitle to item 1 of argv",
		"-e", "set noteBody to item 2 of argv",
		"-e", "display notification noteBody with title noteTitle",
		"-e", "end run",
		title,
		body,
	}
	return runCommand(ctx, "osascript", script)
}

func sendWindowsNotification(context.Context, Notification) error {
	return fmt.Errorf("Windows notifications are unsupported on %s", runtimeGOOS)
}
