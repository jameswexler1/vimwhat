//go:build linux

package notify

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
)

func detectLinuxDBus() (bool, string) {
	if runtimeGOOS != "linux" {
		return false, fmt.Sprintf("unsupported on %s", runtimeGOOS)
	}
	if !hasSessionBus() {
		return false, "session D-Bus not detected"
	}
	for _, helper := range linuxNotificationHelpers() {
		if _, err := lookPath(helper); err == nil {
			return true, fmt.Sprintf("available via %s", helper)
		}
	}
	return false, "no D-Bus notification helper found in PATH"
}

func detectMacOS() (bool, string) {
	return false, fmt.Sprintf("unsupported on %s", runtimeGOOS)
}

func detectWindows() (bool, string) {
	return false, fmt.Sprintf("unsupported on %s", runtimeGOOS)
}

func sendLinuxDBus(ctx context.Context, note Notification) error {
	if !hasSessionBus() {
		return fmt.Errorf("session D-Bus not detected")
	}
	title := sanitizeNotificationText(note.Title, 96, "vimwhat")
	body := sanitizeNotificationText(note.Body, 220, "")
	iconPath := strings.TrimSpace(note.IconPath)
	var failures []string
	for _, helper := range linuxNotificationHelpers() {
		if _, err := lookPath(helper); err != nil {
			continue
		}
		var err error
		switch helper {
		case "gdbus":
			err = runCommand(ctx, helper, []string{
				"call",
				"--session",
				"--dest", "org.freedesktop.Notifications",
				"--object-path", "/org/freedesktop/Notifications",
				"--method", "org.freedesktop.Notifications.Notify",
				"vimwhat",
				"0",
				iconPath,
				title,
				body,
				"[]",
				"{}",
				"-1",
			})
		case "dbus-send":
			err = runCommand(ctx, helper, []string{
				"--session",
				"--dest=org.freedesktop.Notifications",
				"--type=method_call",
				"/org/freedesktop/Notifications",
				"org.freedesktop.Notifications.Notify",
				"string:vimwhat",
				"uint32:0",
				"string:" + iconPath,
				"string:" + title,
				"string:" + body,
				"array:string:",
				"dict:string:string:",
				"int32:-1",
			})
		case "notify-send":
			args := []string{}
			if iconPath != "" {
				args = append(args, "-i", iconPath)
			}
			args = append(args, title)
			if body != "" {
				args = append(args, body)
			}
			err = runCommand(ctx, helper, args)
		default:
			continue
		}
		if err == nil {
			return nil
		}
		failures = append(failures, fmt.Sprintf("%s: %v", helper, err))
	}
	if len(failures) > 0 {
		return fmt.Errorf("notification delivery failed: %s", strings.Join(failures, "; "))
	}
	return fmt.Errorf("no D-Bus notification helper found")
}

func sendMacOSNotification(context.Context, Notification) error {
	return fmt.Errorf("macOS notifications are unsupported on %s", runtimeGOOS)
}

func sendWindowsNotification(context.Context, Notification) error {
	return fmt.Errorf("Windows notifications are unsupported on %s", runtimeGOOS)
}

func linuxNotificationHelpers() []string {
	return []string{"notify-send", "gdbus", "dbus-send"}
}

func hasSessionBus() bool {
	if strings.TrimSpace(getEnv("DBUS_SESSION_BUS_ADDRESS")) != "" {
		return true
	}
	runtimeDir := strings.TrimSpace(getEnv("XDG_RUNTIME_DIR"))
	if runtimeDir == "" {
		return false
	}
	info, err := statPath(filepath.Join(runtimeDir, "bus"))
	return err == nil && !info.IsDir()
}
