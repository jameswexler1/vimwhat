package notify

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"vimwhat/internal/config"
)

type Backend string

const (
	BackendAuto              Backend = "auto"
	BackendNone              Backend = "none"
	BackendCommand           Backend = "command"
	BackendLinuxDBus         Backend = "linux-dbus"
	BackendMacOSAppleScript  Backend = "macos-osascript"
	BackendWindowsPowerShell Backend = "windows-powershell"
)

type Notification struct {
	Title  string
	Body   string
	Chat   string
	Sender string
}

type MessagePayload struct {
	ChatTitle string
	ChatKind  string
	Sender    string
	Preview   string
}

type Report struct {
	Requested Backend
	Selected  Backend
	Command   string
	OS        string
	Reasons   map[Backend]string
}

type Notifier interface {
	Notify(context.Context, Notification) error
	Report() Report
}

type notifier struct {
	report          Report
	commandTemplate string
}

type commandCandidate struct {
	name string
	args []string
}

var (
	runtimeGOOS = runtime.GOOS
	lookPath    = exec.LookPath
	runCommand  = func(ctx context.Context, name string, args []string) error {
		cmd := exec.CommandContext(ctx, name, args...)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			msg := strings.TrimSpace(stderr.String())
			if msg != "" {
				return fmt.Errorf("%s: %w", msg, err)
			}
			return err
		}
		return nil
	}
	statPath = os.Stat
	getEnv   = os.Getenv
)

func New(cfg config.Config) Notifier {
	return notifier{
		report:          Detect(cfg),
		commandTemplate: strings.TrimSpace(cfg.NotificationCommand),
	}
}

func Detect(cfg config.Config) Report {
	return detect(normalizeBackend(cfg.NotificationBackend), strings.TrimSpace(cfg.NotificationCommand))
}

func FormatChatMessage(payload MessagePayload) Notification {
	title := sanitizeNotificationText(payload.ChatTitle, 96, "vimwhat")
	preview := sanitizeNotificationText(payload.Preview, 220, "New message")
	body := preview
	if strings.EqualFold(strings.TrimSpace(payload.ChatKind), "group") {
		sender := sanitizeNotificationText(payload.Sender, 48, "")
		if sender != "" {
			body = sender + ": " + preview
		}
	}
	return Notification{
		Title:  title,
		Body:   body,
		Chat:   sanitizeNotificationText(payload.ChatTitle, 96, ""),
		Sender: sanitizeNotificationText(payload.Sender, 48, ""),
	}
}

func (n notifier) Notify(ctx context.Context, note Notification) error {
	switch n.report.Selected {
	case BackendCommand:
		candidate, err := configuredCommandCandidate(n.commandTemplate, note)
		if err != nil {
			return err
		}
		return runCommand(ctx, candidate.name, candidate.args)
	case BackendLinuxDBus:
		return sendLinuxDBus(ctx, note)
	case BackendMacOSAppleScript:
		return sendMacOSNotification(ctx, note)
	case BackendWindowsPowerShell:
		return sendWindowsNotification(ctx, note)
	case BackendNone, BackendAuto:
		return fmt.Errorf("notifications are disabled")
	default:
		return fmt.Errorf("unsupported notification backend %q", n.report.Selected)
	}
}

func (n notifier) Report() Report {
	return n.report
}

func (r Report) Lines() []string {
	lines := []string{
		fmt.Sprintf("requested notification backend: %s", valueOrDefault(string(r.Requested), string(BackendAuto))),
		fmt.Sprintf("selected notification backend: %s", valueOrDefault(string(r.Selected), string(BackendNone))),
		fmt.Sprintf("notification command: %s", valueOrDefault(strings.TrimSpace(r.Command), "none")),
	}
	for _, backend := range []Backend{BackendCommand, BackendLinuxDBus, BackendMacOSAppleScript, BackendWindowsPowerShell} {
		lines = append(lines, fmt.Sprintf("%s: %s", backend, r.reason(backend)))
	}
	lines = append(lines, fmt.Sprintf("notification delivery path: %s", r.deliveryPath()))
	return lines
}

func (r Report) reason(backend Backend) string {
	if r.Reasons == nil {
		return "unavailable"
	}
	if reason := strings.TrimSpace(r.Reasons[backend]); reason != "" {
		return reason
	}
	return "unavailable"
}

func (r Report) deliveryPath() string {
	switch r.Selected {
	case BackendCommand:
		if strings.TrimSpace(r.Command) == "" {
			return "disabled"
		}
		return "configured command override"
	case BackendLinuxDBus:
		return "session D-Bus desktop notification"
	case BackendMacOSAppleScript:
		return "macOS notification center via osascript"
	case BackendWindowsPowerShell:
		return "Windows toast via PowerShell"
	case BackendNone:
		return "disabled"
	default:
		return "unavailable"
	}
}

func detect(requested Backend, command string) Report {
	report := Report{
		Requested: requested,
		Selected:  BackendNone,
		Command:   command,
		OS:        runtimeGOOS,
		Reasons:   map[Backend]string{},
	}

	commandCandidate, commandReason := detectConfiguredCommand(command)
	report.Reasons[BackendCommand] = commandReason

	linuxAvailable, linuxReason := detectLinuxDBus()
	report.Reasons[BackendLinuxDBus] = linuxReason

	macosAvailable, macosReason := detectMacOS()
	report.Reasons[BackendMacOSAppleScript] = macosReason

	windowsAvailable, windowsReason := detectWindows()
	report.Reasons[BackendWindowsPowerShell] = windowsReason

	switch requested {
	case BackendNone:
		report.Selected = BackendNone
	case BackendCommand:
		if commandCandidate != nil {
			report.Selected = BackendCommand
		}
	case BackendLinuxDBus:
		if linuxAvailable {
			report.Selected = BackendLinuxDBus
		}
	case BackendMacOSAppleScript:
		if macosAvailable {
			report.Selected = BackendMacOSAppleScript
		}
	case BackendWindowsPowerShell:
		if windowsAvailable {
			report.Selected = BackendWindowsPowerShell
		}
	case BackendAuto:
		if commandCandidate != nil {
			report.Selected = BackendCommand
		} else if linuxAvailable {
			report.Selected = BackendLinuxDBus
		} else if macosAvailable {
			report.Selected = BackendMacOSAppleScript
		} else if windowsAvailable {
			report.Selected = BackendWindowsPowerShell
		}
	default:
		report.Selected = BackendNone
	}

	return report
}

func detectConfiguredCommand(template string) (*commandCandidate, string) {
	template = strings.TrimSpace(template)
	if template == "" {
		return nil, "not configured"
	}
	argv, err := splitCommandLine(template)
	if err != nil {
		return nil, fmt.Sprintf("invalid command: %v", err)
	}
	if len(argv) == 0 {
		return nil, "invalid command: empty command"
	}
	if _, err := lookPath(argv[0]); err != nil {
		return nil, fmt.Sprintf("%s not found in PATH", argv[0])
	}
	return &commandCandidate{name: argv[0], args: argv[1:]}, "configured override"
}

func detectLinuxDBus() (bool, string) {
	if runtimeGOOS != "linux" {
		return false, fmt.Sprintf("unsupported on %s", runtimeGOOS)
	}
	if !hasSessionBus() {
		return false, "session D-Bus not detected"
	}
	for _, helper := range []string{"gdbus", "dbus-send", "notify-send"} {
		if _, err := lookPath(helper); err == nil {
			return true, fmt.Sprintf("available via %s", helper)
		}
	}
	return false, "no D-Bus notification helper found in PATH"
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
	if runtimeGOOS != "windows" {
		return false, fmt.Sprintf("unsupported on %s", runtimeGOOS)
	}
	if name := windowsPowerShellCommand(); name != "" {
		return true, fmt.Sprintf("available via %s", name)
	}
	return false, "powershell.exe not found in PATH"
}

func sendLinuxDBus(ctx context.Context, note Notification) error {
	if !hasSessionBus() {
		return fmt.Errorf("session D-Bus not detected")
	}
	title := sanitizeNotificationText(note.Title, 96, "vimwhat")
	body := sanitizeNotificationText(note.Body, 220, "")
	for _, helper := range []string{"gdbus", "dbus-send", "notify-send"} {
		if _, err := lookPath(helper); err != nil {
			continue
		}
		switch helper {
		case "gdbus":
			return runCommand(ctx, helper, []string{
				"call",
				"--session",
				"--dest", "org.freedesktop.Notifications",
				"--object-path", "/org/freedesktop/Notifications",
				"--method", "org.freedesktop.Notifications.Notify",
				"vimwhat",
				"0",
				"",
				title,
				body,
				"[]",
				"{}",
				"-1",
			})
		case "dbus-send":
			return runCommand(ctx, helper, []string{
				"--session",
				"--dest=org.freedesktop.Notifications",
				"--type=method_call",
				"/org/freedesktop/Notifications",
				"org.freedesktop.Notifications.Notify",
				"string:vimwhat",
				"uint32:0",
				"string:",
				"string:" + title,
				"string:" + body,
				"array:string:",
				"dict:string:string:",
				"int32:-1",
			})
		case "notify-send":
			args := []string{title}
			if body != "" {
				args = append(args, body)
			}
			return runCommand(ctx, helper, args)
		}
	}
	return fmt.Errorf("no D-Bus notification helper found")
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

func configuredCommandCandidate(template string, note Notification) (commandCandidate, error) {
	template = strings.TrimSpace(template)
	if template == "" {
		return commandCandidate{}, fmt.Errorf("notification command is empty")
	}
	argv, err := splitCommandLine(template)
	if err != nil {
		return commandCandidate{}, err
	}
	if len(argv) == 0 {
		return commandCandidate{}, fmt.Errorf("notification command is empty")
	}

	title := sanitizeNotificationText(note.Title, 96, "vimwhat")
	body := sanitizeNotificationText(note.Body, 220, "")
	chat := sanitizeNotificationText(note.Chat, 96, "")
	sender := sanitizeNotificationText(note.Sender, 48, "")
	placeholders := map[string]string{
		"{title}":  title,
		"{body}":   body,
		"{chat}":   chat,
		"{sender}": sender,
	}

	hasTitle := false
	hasBody := false
	for i, arg := range argv {
		if strings.Contains(arg, "{title}") {
			hasTitle = true
		}
		if strings.Contains(arg, "{body}") {
			hasBody = true
		}
		for placeholder, value := range placeholders {
			arg = strings.ReplaceAll(arg, placeholder, value)
		}
		argv[i] = arg
	}
	if _, err := lookPath(argv[0]); err != nil {
		return commandCandidate{}, fmt.Errorf("notification command %q not found", argv[0])
	}
	args := append([]string{}, argv[1:]...)
	if !hasTitle && !hasBody {
		args = append(args, title)
		if body != "" {
			args = append(args, body)
		}
	}
	return commandCandidate{name: argv[0], args: args}, nil
}

func windowsPowerShellCommand() string {
	for _, name := range []string{"powershell.exe", "pwsh"} {
		if _, err := lookPath(name); err == nil {
			return name
		}
	}
	return ""
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

func normalizeBackend(value string) Backend {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", string(BackendAuto):
		return BackendAuto
	case string(BackendNone):
		return BackendNone
	case string(BackendCommand):
		return BackendCommand
	case string(BackendLinuxDBus):
		return BackendLinuxDBus
	case string(BackendMacOSAppleScript):
		return BackendMacOSAppleScript
	case string(BackendWindowsPowerShell):
		return BackendWindowsPowerShell
	default:
		return BackendAuto
	}
}

func sanitizeNotificationText(value string, limit int, fallback string) string {
	fields := strings.Fields(strings.TrimSpace(value))
	if len(fields) == 0 {
		value = strings.TrimSpace(fallback)
	} else {
		value = strings.Join(fields, " ")
	}
	if limit <= 0 {
		return value
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	if limit <= 1 {
		return string(runes[:limit])
	}
	return string(runes[:limit-1]) + "…"
}

func splitCommandLine(input string) ([]string, error) {
	var args []string
	var current strings.Builder
	var quote rune
	escaped := false

	flush := func() {
		if current.Len() == 0 {
			return
		}
		args = append(args, current.String())
		current.Reset()
	}

	for _, r := range input {
		if escaped {
			current.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' {
			escaped = true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
			} else {
				current.WriteRune(r)
			}
			continue
		}
		switch r {
		case '\'', '"':
			quote = r
		case ' ', '\t', '\n':
			flush()
		default:
			current.WriteRune(r)
		}
	}
	if escaped {
		current.WriteRune('\\')
	}
	if quote != 0 {
		return nil, fmt.Errorf("unterminated quote in command")
	}
	flush()

	return args, nil
}

func valueOrDefault(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func notifyTimeout() time.Duration {
	return 3 * time.Second
}
