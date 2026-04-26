//go:build windows

package notify

import (
	"context"
	"fmt"
	"strings"
)

func detectLinuxDBus() (bool, string) {
	return false, fmt.Sprintf("unsupported on %s", runtimeGOOS)
}

func detectMacOS() (bool, string) {
	return false, fmt.Sprintf("unsupported on %s", runtimeGOOS)
}

func detectWindows() (bool, string) {
	if runtimeGOOS != "windows" {
		return false, fmt.Sprintf("unsupported on %s", runtimeGOOS)
	}
	if name := windowsPowerShellCommand(); name != "" {
		return true, fmt.Sprintf("available via %s", name)
	}
	return false, "powershell.exe not found in PATH"
}

func sendLinuxDBus(context.Context, Notification) error {
	return fmt.Errorf("Linux D-Bus notifications are unsupported on %s", runtimeGOOS)
}

func sendMacOSNotification(context.Context, Notification) error {
	return fmt.Errorf("macOS notifications are unsupported on %s", runtimeGOOS)
}

func sendWindowsNotification(ctx context.Context, note Notification) error {
	name := windowsPowerShellCommand()
	if name == "" {
		return fmt.Errorf("powershell.exe not found in PATH")
	}
	title := sanitizeNotificationText(note.Title, 96, "vimwhat")
	body := sanitizeNotificationText(note.Body, 220, "")
	script := strings.Join([]string{
		"Add-Type -AssemblyName System.Runtime.WindowsRuntime | Out-Null",
		"[Windows.UI.Notifications.ToastNotificationManager, Windows.UI.Notifications, ContentType=WindowsRuntime] | Out-Null",
		"[Windows.Data.Xml.Dom.XmlDocument, Windows.Data.Xml.Dom.XmlDocument, ContentType=WindowsRuntime] | Out-Null",
		"function Escape([string]$value) { [System.Security.SecurityElement]::Escape($value) }",
		"$title = Escape($args[0])",
		"$body = Escape($args[1])",
		"$xml = New-Object Windows.Data.Xml.Dom.XmlDocument",
		"$xml.LoadXml(\"<toast><visual><binding template='ToastGeneric'><text>$title</text><text>$body</text></binding></visual></toast>\")",
		"$toast = [Windows.UI.Notifications.ToastNotification]::new($xml)",
		"[Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier('vimwhat').Show($toast)",
	}, "; ")
	return runCommand(ctx, name, []string{
		"-NoLogo",
		"-NonInteractive",
		"-NoProfile",
		"-Command",
		script,
		title,
		body,
	})
}

func windowsPowerShellCommand() string {
	for _, name := range []string{"powershell.exe", "pwsh"} {
		if _, err := lookPath(name); err == nil {
			return name
		}
	}
	return ""
}
