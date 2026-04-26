//go:build windows

package notify

import (
	"context"
	"strings"
	"testing"

	"vimwhat/internal/config"
)

func TestDetectWindowsPowerShell(t *testing.T) {
	prevGOOS := runtimeGOOS
	prevLookPath := lookPath
	t.Cleanup(func() {
		runtimeGOOS = prevGOOS
		lookPath = prevLookPath
	})

	runtimeGOOS = "windows"
	lookPath = func(name string) (string, error) {
		if name == "powershell.exe" {
			return `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`, nil
		}
		return "", context.DeadlineExceeded
	}

	report := Detect(config.Config{NotificationBackend: string(BackendAuto)})
	if report.Selected != BackendWindowsPowerShell {
		t.Fatalf("Selected = %q, want %q", report.Selected, BackendWindowsPowerShell)
	}
}

func TestSendWindowsNotificationBuildsPowerShellToastCommand(t *testing.T) {
	prevLookPath := lookPath
	prevRunCommand := runCommand
	t.Cleanup(func() {
		lookPath = prevLookPath
		runCommand = prevRunCommand
	})

	lookPath = func(name string) (string, error) {
		if name == "powershell.exe" {
			return `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`, nil
		}
		return "", context.DeadlineExceeded
	}
	var gotName string
	var gotArgs []string
	runCommand = func(_ context.Context, name string, args []string) error {
		gotName = name
		gotArgs = append([]string{}, args...)
		return nil
	}

	if err := sendWindowsNotification(context.Background(), Notification{Title: "Alice", Body: "hello"}); err != nil {
		t.Fatalf("sendWindowsNotification() error = %v", err)
	}
	if gotName != "powershell.exe" {
		t.Fatalf("command = %q", gotName)
	}
	if got := strings.Join(gotArgs, "\x00"); !strings.Contains(got, "ToastNotificationManager") || !strings.Contains(got, "Alice") || !strings.Contains(got, "hello") {
		t.Fatalf("args = %#v", gotArgs)
	}
}
