package app

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
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

type imageClipboardCommand struct {
	argv     []string
	pathMode bool
	path     string
}

func pasteImageFromClipboard(paths config.Paths, commandTemplate string) tea.Cmd {
	return func() tea.Msg {
		attachment, err := readImageFromClipboard(context.Background(), paths, commandTemplate)
		return ui.ClipboardImagePastedMsg{Attachment: attachment, Err: err}
	}
}

func copyImageToClipboard(commandTemplate string, item store.MediaMetadata) tea.Cmd {
	return func() tea.Msg {
		err := writeImageToClipboard(context.Background(), commandTemplate, item)
		return ui.ClipboardImageCopiedMsg{Media: item, Err: err}
	}
}

func readImageFromClipboard(ctx context.Context, paths config.Paths, commandTemplate string) (ui.Attachment, error) {
	if strings.TrimSpace(paths.MediaDir) == "" {
		return ui.Attachment{}, fmt.Errorf("media cache dir is required")
	}
	if err := os.MkdirAll(paths.MediaDir, 0o700); err != nil {
		return ui.Attachment{}, fmt.Errorf("create media cache dir: %w", err)
	}

	commands := imagePasteCommands(commandTemplate, paths.MediaDir)
	if len(commands) == 0 {
		return ui.Attachment{}, fmt.Errorf("no image clipboard paste command found")
	}

	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	var lastErr error
	for _, candidate := range commands {
		if len(candidate.argv) == 0 {
			continue
		}
		if strings.TrimSpace(commandTemplate) == "" {
			if _, err := exec.LookPath(candidate.argv[0]); err != nil {
				lastErr = err
				continue
			}
		}
		var attachment ui.Attachment
		var err error
		if candidate.pathMode {
			attachment, err = readImageFromClipboardPathMode(ctx, candidate)
		} else {
			attachment, err = readImageFromClipboardStdout(ctx, paths.MediaDir, candidate.argv)
		}
		if err == nil {
			return attachment, nil
		}
		lastErr = err
	}
	if lastErr != nil {
		return ui.Attachment{}, lastErr
	}
	return ui.Attachment{}, fmt.Errorf("no image clipboard paste command found")
}

func readImageFromClipboardPathMode(ctx context.Context, candidate imageClipboardCommand) (ui.Attachment, error) {
	target := strings.TrimSpace(candidate.path)
	if target == "" {
		return ui.Attachment{}, fmt.Errorf("clipboard image path is empty")
	}
	if err := runClipboardCommand(ctx, candidate.argv, nil, nil); err != nil {
		_ = os.Remove(target)
		return ui.Attachment{}, err
	}
	return attachmentFromClipboardImagePath(target)
}

func readImageFromClipboardStdout(ctx context.Context, mediaDir string, argv []string) (ui.Attachment, error) {
	var stdout bytes.Buffer
	if err := runClipboardCommand(ctx, argv, nil, &stdout); err != nil {
		return ui.Attachment{}, err
	}
	data := stdout.Bytes()
	mimeType := http.DetectContentType(data)
	if !strings.HasPrefix(strings.ToLower(mimeType), "image/") {
		return ui.Attachment{}, fmt.Errorf("clipboard does not contain an image")
	}
	target := clipboardImagePath(mediaDir, imageExtensionForMIME(mimeType))
	tmp, err := os.CreateTemp(mediaDir, "clipboard-*.tmp")
	if err != nil {
		return ui.Attachment{}, fmt.Errorf("create clipboard image temp file: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return ui.Attachment{}, fmt.Errorf("write clipboard image: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return ui.Attachment{}, fmt.Errorf("close clipboard image: %w", err)
	}
	if err := os.Rename(tmpPath, target); err != nil {
		_ = os.Remove(tmpPath)
		return ui.Attachment{}, fmt.Errorf("store clipboard image: %w", err)
	}
	return attachmentFromClipboardImagePath(target)
}

func attachmentFromClipboardImagePath(path string) (ui.Attachment, error) {
	attachment, err := ui.AttachmentFromPath(path)
	if err != nil {
		return ui.Attachment{}, err
	}
	if media.MediaKind(attachment.MIMEType, attachment.FileName) != media.KindImage {
		_ = os.Remove(path)
		return ui.Attachment{}, fmt.Errorf("clipboard does not contain an image")
	}
	return attachment, nil
}

func writeImageToClipboard(ctx context.Context, commandTemplate string, item store.MediaMetadata) error {
	if media.MediaKind(item.MIMEType, item.FileName) != media.KindImage {
		return fmt.Errorf("focused media is not an image")
	}
	path := strings.TrimSpace(item.LocalPath)
	if path == "" {
		return fmt.Errorf("image is not downloaded")
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat image: %w", err)
	}
	if info.IsDir() {
		return fmt.Errorf("image path is a directory")
	}

	mimeType := imageClipboardMIME(item, path)
	commands := imageCopyCommands(commandTemplate, path, mimeType)
	if len(commands) == 0 {
		return fmt.Errorf("no image clipboard copy command found")
	}

	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	var input []byte
	var lastErr error
	for _, candidate := range commands {
		if len(candidate.argv) == 0 {
			continue
		}
		if strings.TrimSpace(commandTemplate) == "" {
			if _, err := exec.LookPath(candidate.argv[0]); err != nil {
				lastErr = err
				continue
			}
		}
		var stdin io.Reader
		if !candidate.pathMode {
			if input == nil {
				data, err := os.ReadFile(path)
				if err != nil {
					return fmt.Errorf("read image: %w", err)
				}
				input = data
			}
			stdin = bytes.NewReader(input)
		}
		if err := runClipboardCommand(ctx, candidate.argv, stdin, nil); err != nil {
			lastErr = err
			continue
		}
		return nil
	}
	if lastErr != nil {
		return lastErr
	}
	return fmt.Errorf("no image clipboard copy command found")
}

func imagePasteCommands(commandTemplate, mediaDir string) []imageClipboardCommand {
	commandTemplate = strings.TrimSpace(commandTemplate)
	if commandTemplate != "" {
		pathMode := strings.Contains(commandTemplate, "{path}")
		path := ""
		if pathMode {
			path = clipboardImagePath(mediaDir, ".png")
			commandTemplate = strings.ReplaceAll(commandTemplate, "{path}", path)
		}
		argv, err := splitCommandLine(commandTemplate)
		if err != nil || len(argv) == 0 {
			if path != "" {
				_ = os.Remove(path)
			}
			return nil
		}
		return []imageClipboardCommand{{argv: argv, pathMode: pathMode, path: path}}
	}

	var commands []imageClipboardCommand
	if os.Getenv("WAYLAND_DISPLAY") != "" {
		commands = append(commands, imageClipboardCommand{argv: []string{"wl-paste", "--type", "image/png"}})
	}
	if os.Getenv("DISPLAY") != "" {
		commands = append(commands, imageClipboardCommand{argv: []string{"xclip", "-selection", "clipboard", "-t", "image/png", "-o"}})
	}
	if _, err := exec.LookPath("pngpaste"); err == nil {
		target := clipboardImagePath(mediaDir, ".png")
		commands = append(commands, imageClipboardCommand{argv: []string{"pngpaste", target}, pathMode: true, path: target})
	}
	return commands
}

func imageCopyCommands(commandTemplate, path, mimeType string) []imageClipboardCommand {
	commandTemplate = strings.TrimSpace(commandTemplate)
	if commandTemplate != "" {
		pathMode := strings.Contains(commandTemplate, "{path}")
		commandTemplate = strings.ReplaceAll(commandTemplate, "{path}", path)
		commandTemplate = strings.ReplaceAll(commandTemplate, "{mime}", mimeType)
		argv, err := splitCommandLine(commandTemplate)
		if err != nil || len(argv) == 0 {
			return nil
		}
		return []imageClipboardCommand{{argv: argv, pathMode: pathMode}}
	}

	var commands []imageClipboardCommand
	if os.Getenv("WAYLAND_DISPLAY") != "" {
		commands = append(commands, imageClipboardCommand{argv: []string{"wl-copy", "--type", mimeType}})
	}
	if os.Getenv("DISPLAY") != "" {
		commands = append(commands, imageClipboardCommand{argv: []string{"xclip", "-selection", "clipboard", "-t", mimeType}})
	}
	return commands
}

func runClipboardCommand(ctx context.Context, argv []string, stdin io.Reader, stdout io.Writer) error {
	if len(argv) == 0 {
		return fmt.Errorf("clipboard command is empty")
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	if stdin != nil {
		cmd.Stdin = stdin
	}
	if stdout != nil {
		cmd.Stdout = stdout
	}
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

func clipboardImagePath(dir, ext string) string {
	ext = strings.TrimSpace(ext)
	if ext == "" {
		ext = ".png"
	}
	return filepath.Join(dir, fmt.Sprintf("clipboard-%d%s", time.Now().UnixNano(), ext))
}

func imageExtensionForMIME(mimeType string) string {
	mimeType = strings.ToLower(strings.TrimSpace(mimeType))
	switch mimeType {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	}
	if exts, err := mime.ExtensionsByType(mimeType); err == nil && len(exts) > 0 {
		return exts[0]
	}
	return ".png"
}

func imageClipboardMIME(item store.MediaMetadata, path string) string {
	mimeType := strings.ToLower(strings.TrimSpace(item.MIMEType))
	if strings.HasPrefix(mimeType, "image/") {
		return mimeType
	}
	file, err := os.Open(path)
	if err != nil {
		return "image/png"
	}
	defer file.Close()
	var buf [512]byte
	n, err := file.Read(buf[:])
	if err != nil && n == 0 {
		return "image/png"
	}
	if detected := http.DetectContentType(buf[:n]); strings.HasPrefix(strings.ToLower(detected), "image/") {
		return strings.ToLower(detected)
	}
	return "image/png"
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

func liveMediaForOutgoingMessage(messageID string, attachments []ui.Attachment, updatedAt time.Time) []store.MediaMetadata {
	mediaItems := mediaForOutgoingMessage(messageID, attachments, updatedAt)
	for i := range mediaItems {
		if strings.TrimSpace(mediaItems[i].LocalPath) != "" {
			mediaItems[i].DownloadState = "downloaded"
		}
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
