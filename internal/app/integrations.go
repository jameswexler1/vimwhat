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

	"vimwhat/internal/commandline"
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

	return platformClipboardCommands()
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
		}
		argv, err := splitCommandLine(commandTemplate)
		if err != nil || len(argv) == 0 {
			if path != "" {
				_ = os.Remove(path)
			}
			return nil
		}
		replaceArgPlaceholder(argv, "{path}", path)
		return []imageClipboardCommand{{argv: argv, pathMode: pathMode, path: path}}
	}

	return platformImagePasteCommands(mediaDir)
}

func imageCopyCommands(commandTemplate, path, mimeType string) []imageClipboardCommand {
	commandTemplate = strings.TrimSpace(commandTemplate)
	if commandTemplate != "" {
		pathMode := strings.Contains(commandTemplate, "{path}")
		argv, err := splitCommandLine(commandTemplate)
		if err != nil || len(argv) == 0 {
			return nil
		}
		replaceArgPlaceholder(argv, "{path}", path)
		replaceArgPlaceholder(argv, "{mime}", mimeType)
		return []imageClipboardCommand{{argv: argv, pathMode: pathMode}}
	}

	return platformImageCopyCommands(path, mimeType)
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
		commandTemplate = platformDefaultFilePickerCommand()
	}
	argv, err := splitCommandLine(commandTemplate)
	if err != nil {
		_ = os.Remove(chooserPath)
		return attachmentPickerError(err)
	}
	if len(argv) == 0 {
		_ = os.Remove(chooserPath)
		return attachmentPickerError(fmt.Errorf("file picker command is empty"))
	}
	replaceArgPlaceholder(argv, "{chooser}", chooserPath)
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

func pickSticker(paths config.Paths, cfg config.Config, db *store.Store, preload func(context.Context) error) tea.Cmd {
	return func() tea.Msg {
		var preloadErr error
		preloaded := preload != nil
		if preload != nil {
			preloadCtx, cancel := context.WithTimeout(context.Background(), stickerSyncTimeout)
			preloadErr = preload(preloadCtx)
			cancel()
		}
		cmd, err := stickerPickerCommand(paths, cfg, db)
		if err != nil {
			if preloadErr != nil {
				return ui.StickerPickedMsg{Err: fmt.Errorf("sticker sync failed: %w", preloadErr)}
			}
			if preloaded && strings.Contains(err.Error(), "no cached recent stickers") {
				return ui.StickerPickedMsg{Err: fmt.Errorf("no WhatsApp sticker favorites available after sync")}
			}
			return ui.StickerPickedMsg{Err: err}
		}
		return cmd()
	}
}

func stickerPickerCommand(paths config.Paths, cfg config.Config, db *store.Store) (tea.Cmd, error) {
	stickers, err := stickerPickerCandidates(context.Background(), db, 96)
	if err != nil {
		return nil, err
	}
	if len(stickers) == 0 {
		return nil, fmt.Errorf("no cached recent stickers available")
	}
	pickerDir, pickerFiles, stickersByPath, err := prepareStickerPickerFiles(paths, stickers)
	if err != nil {
		return nil, err
	}
	chooserPath, err := createStickerChooserFile(paths)
	if err != nil {
		_ = os.RemoveAll(pickerDir)
		return nil, err
	}

	commandTemplate := strings.TrimSpace(cfg.StickerPickerCommand)
	if commandTemplate == "" {
		commandTemplate = platformDefaultStickerPickerCommand()
	}
	argv, err := splitCommandLine(commandTemplate)
	if err != nil {
		_ = os.RemoveAll(pickerDir)
		_ = os.Remove(chooserPath)
		return nil, err
	}
	argv = expandStickerPickerArgs(argv, chooserPath, pickerDir, pickerFiles)
	if len(argv) == 0 {
		_ = os.RemoveAll(pickerDir)
		_ = os.Remove(chooserPath)
		return nil, fmt.Errorf("sticker picker command is empty")
	}
	if _, err := exec.LookPath(argv[0]); err != nil {
		_ = os.RemoveAll(pickerDir)
		_ = os.Remove(chooserPath)
		return nil, fmt.Errorf("sticker picker %q not found", argv[0])
	}

	var stdout bytes.Buffer
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdout = &stdout
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		defer os.RemoveAll(pickerDir)
		defer os.Remove(chooserPath)
		if err != nil {
			return ui.StickerPickedMsg{Err: err}
		}
		selected := firstNonEmptyLine(stdout.String())
		if data, readErr := os.ReadFile(chooserPath); readErr == nil {
			if fromChooser := firstNonEmptyLine(string(data)); fromChooser != "" {
				selected = fromChooser
			}
		}
		if selected == "" {
			return ui.StickerPickedMsg{Cancelled: true}
		}
		sticker, ok := selectedStickerForPath(selected, stickersByPath)
		if !ok {
			return ui.StickerPickedMsg{Err: fmt.Errorf("selected sticker is not from the picker set")}
		}
		return ui.StickerPickedMsg{Sticker: sticker}
	}), nil
}

func stickerPickerCandidates(ctx context.Context, db *store.Store, limit int) ([]store.RecentSticker, error) {
	if db == nil {
		return nil, fmt.Errorf("store is required")
	}
	stickers, err := db.ListRecentStickers(ctx, limit)
	if err != nil {
		return nil, err
	}
	out := make([]store.RecentSticker, 0, len(stickers))
	for _, sticker := range stickers {
		if !stickerPickerUsable(sticker) {
			continue
		}
		out = append(out, sticker)
	}
	return out, nil
}

func stickerPickerUsable(sticker store.RecentSticker) bool {
	if sticker.IsLottie || strings.EqualFold(filepath.Ext(sticker.FileName), ".tgs") {
		return false
	}
	if !mediaPathAvailable(sticker.LocalPath) {
		return false
	}
	mimeType := strings.ToLower(strings.TrimSpace(sticker.MIMEType))
	fileName := strings.ToLower(strings.TrimSpace(sticker.FileName))
	localPath := strings.ToLower(strings.TrimSpace(sticker.LocalPath))
	return mimeType == "image/webp" || strings.HasSuffix(fileName, ".webp") || strings.HasSuffix(localPath, ".webp")
}

func prepareStickerPickerFiles(paths config.Paths, stickers []store.RecentSticker) (string, []string, map[string]store.RecentSticker, error) {
	root := paths.TransientDir
	if strings.TrimSpace(root) == "" {
		root = os.TempDir()
	}
	dir, err := os.MkdirTemp(filepath.Join(root, "stickers"), "picker-*")
	if err != nil {
		if mkdirErr := os.MkdirAll(filepath.Join(root, "stickers"), 0o700); mkdirErr != nil {
			return "", nil, nil, fmt.Errorf("create sticker picker root: %w", mkdirErr)
		}
		dir, err = os.MkdirTemp(filepath.Join(root, "stickers"), "picker-*")
		if err != nil {
			return "", nil, nil, fmt.Errorf("create sticker picker dir: %w", err)
		}
	}
	pickerFiles := make([]string, 0, len(stickers))
	stickersByPath := make(map[string]store.RecentSticker, len(stickers)*3)
	for i, sticker := range stickers {
		src := strings.TrimSpace(sticker.LocalPath)
		ext := recentStickerExtension(sticker.MIMEType, sticker.FileName, sticker.IsLottie)
		dst := filepath.Join(dir, fmt.Sprintf("%03d-%s%s", i+1, safeFileStem(sticker.ID), ext))
		if err := linkOrCopyFile(src, dst); err != nil {
			_ = os.RemoveAll(dir)
			return "", nil, nil, fmt.Errorf("prepare sticker picker file: %w", err)
		}
		pickerFiles = append(pickerFiles, dst)
		registerStickerPickerPath(stickersByPath, dst, sticker)
	}
	return dir, pickerFiles, stickersByPath, nil
}

func createStickerChooserFile(paths config.Paths) (string, error) {
	root := strings.TrimSpace(paths.TransientDir)
	if root == "" {
		root = os.TempDir()
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", fmt.Errorf("create sticker chooser dir: %w", err)
	}
	chooser, err := os.CreateTemp(root, "vimwhat-sticker-chooser-*")
	if err != nil {
		return "", fmt.Errorf("create sticker chooser: %w", err)
	}
	path := chooser.Name()
	if err := chooser.Close(); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return path, nil
}

func expandStickerPickerArgs(argv []string, chooserPath, pickerDir string, pickerFiles []string) []string {
	out := make([]string, 0, len(argv)+len(pickerFiles))
	filesExpanded := false
	for _, arg := range argv {
		arg = strings.ReplaceAll(arg, "{chooser}", chooserPath)
		arg = strings.ReplaceAll(arg, "{dir}", pickerDir)
		if arg == "{files}" {
			out = append(out, pickerFiles...)
			filesExpanded = true
			continue
		}
		if strings.Contains(arg, "{files}") {
			out = append(out, strings.ReplaceAll(arg, "{files}", strings.Join(pickerFiles, " ")))
			filesExpanded = true
			continue
		}
		out = append(out, arg)
	}
	if !filesExpanded {
		out = append(out, pickerFiles...)
	}
	return out
}

func selectedStickerForPath(path string, stickersByPath map[string]store.RecentSticker) (store.RecentSticker, bool) {
	path = strings.Trim(strings.TrimSpace(path), `"'`)
	if path == "" {
		return store.RecentSticker{}, false
	}
	for _, candidate := range []string{path, filepath.Clean(path)} {
		if sticker, ok := stickersByPath[candidate]; ok {
			return sticker, true
		}
		if abs, err := filepath.Abs(candidate); err == nil {
			if sticker, ok := stickersByPath[abs]; ok {
				return sticker, true
			}
		}
	}
	return store.RecentSticker{}, false
}

func registerStickerPickerPath(stickersByPath map[string]store.RecentSticker, path string, sticker store.RecentSticker) {
	path = filepath.Clean(path)
	stickersByPath[path] = sticker
	if abs, err := filepath.Abs(path); err == nil {
		stickersByPath[abs] = sticker
	}
}

func linkOrCopyFile(src, dst string) error {
	if err := os.Link(src, dst); err == nil {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
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
		template = platformDefaultAudioPlayerCommand()
	}
	hasPathPlaceholder := strings.Contains(template, "{path}")
	argv, err := splitCommandLine(template)
	if err != nil {
		return nil, path, err
	}
	if len(argv) == 0 {
		return nil, path, fmt.Errorf("audio player command is empty")
	}
	replaceArgPlaceholder(argv, "{path}", path)
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
	argv, err := splitCommandLine(template)
	if err != nil {
		return nil, path, err
	}
	if len(argv) == 0 {
		return nil, path, fmt.Errorf("media opener command is empty")
	}
	replaceArgPlaceholder(argv, "{path}", path)
	if !hasPathPlaceholder {
		argv = append(argv, path)
	}
	if _, err := exec.LookPath(argv[0]); err != nil {
		return nil, path, fmt.Errorf("media opener %q not found", argv[0])
	}
	return exec.Command(argv[0], argv[1:]...), path, nil
}

func replaceArgPlaceholder(argv []string, placeholder, value string) {
	for i, arg := range argv {
		argv[i] = strings.ReplaceAll(arg, placeholder, value)
	}
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
	for _, argv := range platformAutoOpenCommands(item, path) {
		if len(argv) == 0 {
			continue
		}
		if _, err := exec.LookPath(argv[0]); err == nil {
			return argv, nil
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

func mediaForOutgoingStickerMessage(messageID string, sticker store.RecentSticker, updatedAt time.Time) []store.MediaMetadata {
	return []store.MediaMetadata{{
		MessageID:     messageID,
		Kind:          "sticker",
		MIMEType:      sticker.MIMEType,
		FileName:      sticker.FileName,
		SizeBytes:     sticker.FileLength,
		LocalPath:     sticker.LocalPath,
		DownloadState: "downloaded",
		IsAnimated:    sticker.IsAnimated,
		IsLottie:      sticker.IsLottie,
		UpdatedAt:     updatedAt,
	}}
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
	return commandline.Split(input)
}
