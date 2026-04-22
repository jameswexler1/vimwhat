package app

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"vimwhat/internal/config"
	"vimwhat/internal/media"
	"vimwhat/internal/store"
	"vimwhat/internal/ui"
)

func copyToClipboard(ctx context.Context, configuredCommand, text string) error {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	configuredCommand = strings.TrimSpace(configuredCommand)

	candidates := clipboardCommands(configuredCommand)
	if len(candidates) == 0 {
		return fmt.Errorf("no clipboard command found")
	}

	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	var lastErr error
	for _, argv := range candidates {
		if len(argv) == 0 {
			continue
		}
		if configuredCommand == "" {
			if _, err := exec.LookPath(argv[0]); err != nil {
				lastErr = err
				continue
			}
		}
		cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
		cmd.Stdin = strings.NewReader(text)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			msg := strings.TrimSpace(stderr.String())
			if msg != "" {
				lastErr = fmt.Errorf("%s: %w", msg, err)
			} else {
				lastErr = err
			}
			continue
		}
		return nil
	}

	if lastErr != nil {
		return lastErr
	}
	return fmt.Errorf("no clipboard command found")
}

func clipboardCommands(configuredCommand string) [][]string {
	configuredCommand = strings.TrimSpace(configuredCommand)
	if configuredCommand != "" {
		argv, err := splitCommandLine(configuredCommand)
		if err != nil || len(argv) == 0 {
			return nil
		}
		return [][]string{argv}
	}

	var commands [][]string
	if os.Getenv("WAYLAND_DISPLAY") != "" {
		commands = append(commands, []string{"wl-copy"})
	}
	if os.Getenv("DISPLAY") != "" {
		commands = append(commands, []string{"xclip", "-selection", "clipboard"})
		commands = append(commands, []string{"xsel", "--clipboard", "--input"})
	}
	commands = append(commands, []string{"pbcopy"}, []string{"termux-clipboard-set"})
	return commands
}

func pickAttachment(commandTemplate string) tea.Cmd {
	chooser, err := os.CreateTemp("", "vimwhat-chooser-*")
	if err != nil {
		return attachmentPickerError(err)
	}
	chooserPath := chooser.Name()
	if err := chooser.Close(); err != nil {
		_ = os.Remove(chooserPath)
		return attachmentPickerError(err)
	}

	commandTemplate = strings.TrimSpace(commandTemplate)
	if commandTemplate == "" {
		commandTemplate = "yazi --chooser-file {chooser}"
	}
	commandTemplate = strings.ReplaceAll(commandTemplate, "{chooser}", chooserPath)
	argv, err := splitCommandLine(commandTemplate)
	if err != nil {
		_ = os.Remove(chooserPath)
		return attachmentPickerError(err)
	}
	if len(argv) == 0 {
		_ = os.Remove(chooserPath)
		return attachmentPickerError(fmt.Errorf("file picker command is empty"))
	}
	if _, err := exec.LookPath(argv[0]); err != nil {
		_ = os.Remove(chooserPath)
		return attachmentPickerError(fmt.Errorf("file picker %q not found", argv[0]))
	}

	cmd := exec.Command(argv[0], argv[1:]...)
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		defer os.Remove(chooserPath)
		if err != nil {
			return ui.AttachmentPickedMsg{Err: err}
		}
		data, err := os.ReadFile(chooserPath)
		if err != nil {
			return ui.AttachmentPickedMsg{Err: err}
		}
		path := firstNonEmptyLine(string(data))
		if path == "" {
			return ui.AttachmentPickedMsg{Cancelled: true}
		}
		attachment, err := ui.AttachmentFromPath(path)
		return ui.AttachmentPickedMsg{Attachment: attachment, Err: err}
	})
}

func openMedia(cfg config.Config, item store.MediaMetadata) tea.Cmd {
	cmd, path, err := mediaOpenCommand(cfg, item)
	if err != nil {
		return func() tea.Msg {
			return ui.MediaOpenFinishedMsg{Path: path, Err: err}
		}
	}
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		return ui.MediaOpenFinishedMsg{Path: path, Err: err}
	})
}

type startedAudioProcess struct {
	cmd *exec.Cmd
}

func (p *startedAudioProcess) Wait() error {
	return p.cmd.Wait()
}

func (p *startedAudioProcess) Stop() error {
	if p == nil || p.cmd == nil || p.cmd.Process == nil {
		return nil
	}
	return p.cmd.Process.Kill()
}

func startAudio(cfg config.Config, item store.MediaMetadata) (ui.AudioProcess, error) {
	cmd, _, err := audioPlayerCommand(cfg, item)
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start audio player: %w", err)
	}
	return &startedAudioProcess{cmd: cmd}, nil
}

func audioPlayerCommand(cfg config.Config, item store.MediaMetadata) (*exec.Cmd, string, error) {
	path := strings.TrimSpace(item.LocalPath)
	if path == "" {
		return nil, "", fmt.Errorf("audio is not downloaded")
	}

	template := strings.TrimSpace(cfg.AudioPlayerCommand)
	if template == "" {
		template = "mpv --no-video --no-terminal --really-quiet {path}"
	}
	hasPathPlaceholder := strings.Contains(template, "{path}")
	template = strings.ReplaceAll(template, "{path}", path)
	argv, err := splitCommandLine(template)
	if err != nil {
		return nil, path, err
	}
	if len(argv) == 0 {
		return nil, path, fmt.Errorf("audio player command is empty")
	}
	if !hasPathPlaceholder {
		argv = append(argv, path)
	}
	if _, err := exec.LookPath(argv[0]); err != nil {
		return nil, path, fmt.Errorf("audio player %q not found", argv[0])
	}
	return exec.Command(argv[0], argv[1:]...), path, nil
}

func mediaOpenCommand(cfg config.Config, item store.MediaMetadata) (*exec.Cmd, string, error) {
	path := strings.TrimSpace(item.LocalPath)
	if path == "" {
		return nil, "", fmt.Errorf("media is not downloaded")
	}

	template, configured := mediaOpenTemplate(cfg, item)
	if !configured {
		argv, err := autoOpenCommand(item, path)
		if err != nil {
			return nil, path, err
		}
		return exec.Command(argv[0], argv[1:]...), path, nil
	}

	hasPathPlaceholder := strings.Contains(template, "{path}")
	template = strings.ReplaceAll(template, "{path}", path)
	argv, err := splitCommandLine(template)
	if err != nil {
		return nil, path, err
	}
	if len(argv) == 0 {
		return nil, path, fmt.Errorf("media opener command is empty")
	}
	if !hasPathPlaceholder {
		argv = append(argv, path)
	}
	if _, err := exec.LookPath(argv[0]); err != nil {
		return nil, path, fmt.Errorf("media opener %q not found", argv[0])
	}
	return exec.Command(argv[0], argv[1:]...), path, nil
}

func mediaOpenTemplate(cfg config.Config, item store.MediaMetadata) (string, bool) {
	kind := media.MediaKind(item.MIMEType, item.FileName)
	switch kind {
	case media.KindImage:
		if strings.TrimSpace(cfg.ImageViewerCommand) != "" {
			return strings.TrimSpace(cfg.ImageViewerCommand), true
		}
	case media.KindVideo:
		if strings.TrimSpace(cfg.VideoPlayerCommand) != "" {
			return strings.TrimSpace(cfg.VideoPlayerCommand), true
		}
	}
	if strings.TrimSpace(cfg.FileOpenerCommand) != "" {
		return strings.TrimSpace(cfg.FileOpenerCommand), true
	}
	return "", false
}

func autoOpenCommand(item store.MediaMetadata, path string) ([]string, error) {
	var candidates []string
	switch media.MediaKind(item.MIMEType, item.FileName) {
	case media.KindImage:
		candidates = []string{"nsxiv", "mpv", "xdg-open"}
	case media.KindVideo:
		candidates = []string{"mpv", "xdg-open", "nsxiv"}
	case media.KindAudio:
		candidates = []string{"mpv", "xdg-open"}
	default:
		candidates = []string{"xdg-open", "nsxiv", "mpv"}
	}
	for _, name := range candidates {
		if _, err := exec.LookPath(name); err == nil {
			return []string{name, path}, nil
		}
	}
	return nil, fmt.Errorf("no media opener found")
}

func attachmentPickerError(err error) tea.Cmd {
	return func() tea.Msg {
		return ui.AttachmentPickedMsg{Err: err}
	}
}

func firstNonEmptyLine(value string) string {
	for _, line := range strings.Split(value, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}

func mediaForOutgoingMessage(messageID string, attachments []ui.Attachment, updatedAt time.Time) []store.MediaMetadata {
	mediaItems := make([]store.MediaMetadata, 0, len(attachments))
	for _, attachment := range attachments {
		mediaItems = append(mediaItems, store.MediaMetadata{
			MessageID:     messageID,
			MIMEType:      attachment.MIMEType,
			FileName:      attachment.FileName,
			SizeBytes:     attachment.SizeBytes,
			LocalPath:     attachment.LocalPath,
			ThumbnailPath: attachment.ThumbnailPath,
			DownloadState: attachment.DownloadState,
			UpdatedAt:     updatedAt,
		})
	}
	return mediaItems
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
