package notify

import (
	"context"
	"runtime"
	"strings"
	"testing"

	"vimwhat/internal/config"
)

func TestDetectAutoUsesConfiguredCommandOverride(t *testing.T) {
	prevGOOS := runtimeGOOS
	prevLookPath := lookPath
	prevGetEnv := getEnv
	t.Cleanup(func() {
		runtimeGOOS = prevGOOS
		lookPath = prevLookPath
		getEnv = prevGetEnv
	})

	runtimeGOOS = "linux"
	lookPath = func(name string) (string, error) {
		switch name {
		case "notify-send", "gdbus":
			return "/usr/bin/" + name, nil
		default:
			return "", context.DeadlineExceeded
		}
	}
	getEnv = func(key string) string {
		if key == "DBUS_SESSION_BUS_ADDRESS" {
			return "unix:path=/tmp/fake-bus"
		}
		return ""
	}

	report := Detect(config.Config{
		NotificationBackend: string(BackendAuto),
		NotificationCommand: "notify-send vimwhat",
	})

	if report.Selected != BackendCommand {
		t.Fatalf("Selected = %q, want %q", report.Selected, BackendCommand)
	}
	if got := report.reason(BackendCommand); got != "configured override" {
		t.Fatalf("command reason = %q, want configured override", got)
	}
}

func TestDetectLinuxDBusAvailability(t *testing.T) {
	skipUnlessLinuxBuild(t)
	prevGOOS := runtimeGOOS
	prevLookPath := lookPath
	prevGetEnv := getEnv
	t.Cleanup(func() {
		runtimeGOOS = prevGOOS
		lookPath = prevLookPath
		getEnv = prevGetEnv
	})

	runtimeGOOS = "linux"
	lookPath = func(name string) (string, error) {
		if name == "gdbus" {
			return "/usr/bin/gdbus", nil
		}
		return "", context.DeadlineExceeded
	}
	getEnv = func(key string) string {
		if key == "DBUS_SESSION_BUS_ADDRESS" {
			return "unix:path=/tmp/fake-bus"
		}
		return ""
	}

	report := Detect(config.Config{NotificationBackend: string(BackendAuto)})

	if report.Selected != BackendLinuxDBus {
		t.Fatalf("Selected = %q, want %q", report.Selected, BackendLinuxDBus)
	}
	if got := report.reason(BackendLinuxDBus); got != "available via gdbus" {
		t.Fatalf("linux reason = %q, want available via gdbus", got)
	}
}

func TestDetectLinuxDBusPrefersNotifySend(t *testing.T) {
	skipUnlessLinuxBuild(t)
	prevGOOS := runtimeGOOS
	prevLookPath := lookPath
	prevGetEnv := getEnv
	t.Cleanup(func() {
		runtimeGOOS = prevGOOS
		lookPath = prevLookPath
		getEnv = prevGetEnv
	})

	runtimeGOOS = "linux"
	lookPath = func(name string) (string, error) {
		switch name {
		case "notify-send", "gdbus", "dbus-send":
			return "/usr/bin/" + name, nil
		default:
			return "", context.DeadlineExceeded
		}
	}
	getEnv = func(key string) string {
		if key == "DBUS_SESSION_BUS_ADDRESS" {
			return "unix:path=/tmp/fake-bus"
		}
		return ""
	}

	report := Detect(config.Config{NotificationBackend: string(BackendAuto)})

	if report.Selected != BackendLinuxDBus {
		t.Fatalf("Selected = %q, want %q", report.Selected, BackendLinuxDBus)
	}
	if got := report.reason(BackendLinuxDBus); got != "available via notify-send" {
		t.Fatalf("linux reason = %q, want available via notify-send", got)
	}
}

func TestSendLinuxDBusFallsBackAcrossHelpers(t *testing.T) {
	skipUnlessLinuxBuild(t)
	prevGOOS := runtimeGOOS
	prevLookPath := lookPath
	prevRunCommand := runCommand
	prevGetEnv := getEnv
	t.Cleanup(func() {
		runtimeGOOS = prevGOOS
		lookPath = prevLookPath
		runCommand = prevRunCommand
		getEnv = prevGetEnv
	})

	runtimeGOOS = "linux"
	lookPath = func(name string) (string, error) {
		switch name {
		case "notify-send", "gdbus", "dbus-send":
			return "/usr/bin/" + name, nil
		default:
			return "", context.DeadlineExceeded
		}
	}
	getEnv = func(key string) string {
		if key == "DBUS_SESSION_BUS_ADDRESS" {
			return "unix:path=/tmp/fake-bus"
		}
		return ""
	}
	var tried []string
	runCommand = func(_ context.Context, name string, _ []string) error {
		tried = append(tried, name)
		if name == "dbus-send" {
			return nil
		}
		return context.DeadlineExceeded
	}

	err := sendLinuxDBus(context.Background(), Notification{Title: "Alice", Body: "hello"})
	if err != nil {
		t.Fatalf("sendLinuxDBus() error = %v", err)
	}
	if got := strings.Join(tried, ","); got != "notify-send,gdbus,dbus-send" {
		t.Fatalf("helpers tried = %q, want notify-send,gdbus,dbus-send", got)
	}
}

func TestSendLinuxDBusIncludesIconPath(t *testing.T) {
	skipUnlessLinuxBuild(t)
	tests := []struct {
		name       string
		helper     string
		wantInArgs string
	}{
		{
			name:       "notify-send",
			helper:     "notify-send",
			wantInArgs: "-i\x00/tmp/avatar.png\x00Alice\x00hello",
		},
		{
			name:       "gdbus",
			helper:     "gdbus",
			wantInArgs: "vimwhat\x000\x00/tmp/avatar.png\x00Alice\x00hello",
		},
		{
			name:       "dbus-send",
			helper:     "dbus-send",
			wantInArgs: "string:vimwhat\x00uint32:0\x00string:/tmp/avatar.png\x00string:Alice\x00string:hello",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			prevGOOS := runtimeGOOS
			prevLookPath := lookPath
			prevRunCommand := runCommand
			prevGetEnv := getEnv
			t.Cleanup(func() {
				runtimeGOOS = prevGOOS
				lookPath = prevLookPath
				runCommand = prevRunCommand
				getEnv = prevGetEnv
			})

			runtimeGOOS = "linux"
			lookPath = func(name string) (string, error) {
				if name == test.helper {
					return "/usr/bin/" + name, nil
				}
				return "", context.DeadlineExceeded
			}
			getEnv = func(key string) string {
				if key == "DBUS_SESSION_BUS_ADDRESS" {
					return "unix:path=/tmp/fake-bus"
				}
				return ""
			}
			var gotArgs []string
			runCommand = func(_ context.Context, _ string, args []string) error {
				gotArgs = append([]string{}, args...)
				return nil
			}

			err := sendLinuxDBus(context.Background(), Notification{
				Title:    "Alice",
				Body:     "hello",
				IconPath: "/tmp/avatar.png",
			})
			if err != nil {
				t.Fatalf("sendLinuxDBus() error = %v", err)
			}
			if got := strings.Join(gotArgs, "\x00"); !strings.Contains(got, test.wantInArgs) {
				t.Fatalf("args = %#v, want sequence %q", gotArgs, test.wantInArgs)
			}
		})
	}
}

func skipUnlessLinuxBuild(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("Linux D-Bus backend is only built on Linux")
	}
}

func TestConfiguredCommandCandidateAppendsTitleAndBody(t *testing.T) {
	prevLookPath := lookPath
	t.Cleanup(func() {
		lookPath = prevLookPath
	})
	lookPath = func(name string) (string, error) {
		return "/usr/bin/" + name, nil
	}

	candidate, err := configuredCommandCandidate("notify-send vimwhat", Notification{
		Title: "Alice",
		Body:  "hello there",
	})
	if err != nil {
		t.Fatalf("configuredCommandCandidate() error = %v", err)
	}
	if candidate.name != "notify-send" {
		t.Fatalf("candidate.name = %q, want notify-send", candidate.name)
	}
	if got := strings.Join(candidate.args, "\x00"); got != "vimwhat\x00Alice\x00hello there" {
		t.Fatalf("candidate.args = %#v", candidate.args)
	}
}

func TestConfiguredCommandCandidateReplacesPlaceholders(t *testing.T) {
	prevLookPath := lookPath
	t.Cleanup(func() {
		lookPath = prevLookPath
	})
	lookPath = func(name string) (string, error) {
		return "/usr/bin/" + name, nil
	}

	candidate, err := configuredCommandCandidate("notify-send --app-name vimwhat {title} {body}", Notification{
		Title: "Project",
		Body:  "ship it",
	})
	if err != nil {
		t.Fatalf("configuredCommandCandidate() error = %v", err)
	}
	if got := strings.Join(candidate.args, "\x00"); got != "--app-name\x00vimwhat\x00Project\x00ship it" {
		t.Fatalf("candidate.args = %#v", candidate.args)
	}
}

func TestConfiguredCommandCandidateReplacesIconPlaceholder(t *testing.T) {
	prevLookPath := lookPath
	t.Cleanup(func() {
		lookPath = prevLookPath
	})
	lookPath = func(name string) (string, error) {
		return "/usr/bin/" + name, nil
	}

	candidate, err := configuredCommandCandidate("notify-send -i {icon}", Notification{
		Title:    "Alice",
		Body:     "hello there",
		IconPath: "/tmp/avatar.png",
	})
	if err != nil {
		t.Fatalf("configuredCommandCandidate() error = %v", err)
	}
	if got := strings.Join(candidate.args, "\x00"); got != "-i\x00/tmp/avatar.png\x00Alice\x00hello there" {
		t.Fatalf("candidate.args = %#v", candidate.args)
	}
}

func TestFormatChatMessagePrefixesGroupSender(t *testing.T) {
	note := FormatChatMessage(MessagePayload{
		ChatTitle: "Project Group",
		ChatKind:  "group",
		Sender:    "Alice",
		Preview:   "image.png",
		IconPath:  " /tmp/group.png ",
	})
	if note.Title != "Project Group" {
		t.Fatalf("Title = %q, want Project Group", note.Title)
	}
	if note.Body != "Alice: image.png" {
		t.Fatalf("Body = %q, want sender-prefixed preview", note.Body)
	}
	if note.IconPath != "/tmp/group.png" {
		t.Fatalf("IconPath = %q, want trimmed path", note.IconPath)
	}
}
