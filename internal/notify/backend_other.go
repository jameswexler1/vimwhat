//go:build !linux && !darwin && !windows

package notify

import (
	"context"
	"fmt"
)

func detectLinuxDBus() (bool, string) {
	return false, fmt.Sprintf("unsupported on %s", runtimeGOOS)
}

func detectMacOS() (bool, string) {
	return false, fmt.Sprintf("unsupported on %s", runtimeGOOS)
}

func detectWindows() (bool, string) {
	return false, fmt.Sprintf("unsupported on %s", runtimeGOOS)
}

func sendLinuxDBus(context.Context, Notification) error {
	return fmt.Errorf("Linux D-Bus notifications are unsupported on %s", runtimeGOOS)
}

func sendMacOSNotification(context.Context, Notification) error {
	return fmt.Errorf("macOS notifications are unsupported on %s", runtimeGOOS)
}

func sendWindowsNotification(context.Context, Notification) error {
	return fmt.Errorf("Windows notifications are unsupported on %s", runtimeGOOS)
}
