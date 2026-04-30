package media

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type Kind string
type PreviewDisplay string
type SourceKind string

const (
	KindUnsupported Kind = "unsupported"
	KindImage       Kind = "image"
	KindVideo       Kind = "video"
	KindAudio       Kind = "audio"

	PreviewDisplayText    PreviewDisplay = "text"
	PreviewDisplayOverlay PreviewDisplay = "overlay"

	SourceLocal              SourceKind = "local"
	SourceThumbnail          SourceKind = "thumbnail"
	SourceGeneratedThumbnail SourceKind = "generated_thumbnail"
)

type PreviewRequest struct {
	MessageID     string
	MIMEType      string
	FileName      string
	LocalPath     string
	ThumbnailPath string
	CacheDir      string
	Backend       Backend
	Width         int
	Height        int
	Compact       bool
}

type Preview struct {
	Key                string
	MessageID          string
	Kind               Kind
	Backend            Backend
	RenderedBackend    Backend
	Display            PreviewDisplay
	SourceKind         SourceKind
	SourcePath         string
	ThumbnailPath      string
	GeneratedThumbnail bool
	Width              int
	Height             int
	Lines              []string
	Err                error
}

type Previewer struct {
	Backend   Backend
	CacheDir  string
	MaxWidth  int
	MaxHeight int
	Timeout   time.Duration
}

type terminalCellPixels struct {
	Width  int
	Height int
}

var detectTerminalCellPixels = platformTerminalCellPixels

func (p Preview) Ready() bool {
	if p.Err != nil {
		return false
	}
	if p.Display == PreviewDisplayOverlay {
		return p.SourcePath != "" && p.Width > 0 && p.Height > 0
	}
	return len(p.Lines) > 0
}

var runPreviewCommand = func(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	return cmd.CombinedOutput()
}

func NewPreviewer(report Report, cacheDir string, maxWidth, maxHeight int) Previewer {
	if maxWidth <= 0 {
		maxWidth = 67
	}
	if maxHeight <= 0 {
		maxHeight = 40
	}

	return Previewer{
		Backend:   report.Selected,
		CacheDir:  cacheDir,
		MaxWidth:  maxWidth,
		MaxHeight: maxHeight,
		Timeout:   3 * time.Second,
	}
}

func (p Previewer) Render(ctx context.Context, req PreviewRequest) Preview {
	req = p.normalizeRequest(req)
	preview := Preview{
		Key:       PreviewKey(req),
		MessageID: req.MessageID,
		Kind:      MediaKind(req.MIMEType, req.FileName),
		Backend:   req.Backend,
		Width:     req.Width,
		Height:    req.Height,
	}

	if preview.Kind == KindUnsupported || preview.Kind == KindAudio {
		return preview
	}
	if req.Backend == BackendNone || req.Backend == BackendExternal {
		preview.Err = fmt.Errorf("preview backend %s does not render inline", req.Backend)
		return preview
	}

	source, sourceKind, generated, err := p.previewSource(ctx, req, preview.Kind)
	if err != nil {
		preview.Err = err
		return preview
	}
	preview.SourceKind = sourceKind
	preview.SourcePath = source
	if sourceKind == SourceThumbnail || sourceKind == SourceGeneratedThumbnail {
		preview.ThumbnailPath = source
	}
	if generated {
		preview.ThumbnailPath = source
		preview.GeneratedThumbnail = true
	}

	lines, renderedBackend, display, err := p.renderSource(ctx, req, source)
	if err != nil {
		preview.Err = err
		return preview
	}
	preview.Lines = lines
	preview.RenderedBackend = renderedBackend
	preview.Display = display
	return preview
}

func (p Previewer) normalizeRequest(req PreviewRequest) PreviewRequest {
	if req.Backend == "" || req.Backend == BackendAuto {
		req.Backend = p.Backend
	}
	if req.CacheDir == "" {
		req.CacheDir = p.CacheDir
	}
	if req.Width <= 0 || req.Width > p.MaxWidth {
		req.Width = p.MaxWidth
	}
	if req.Height <= 0 || req.Height > p.MaxHeight {
		req.Height = p.MaxHeight
	}
	req.Width = maxInt(1, req.Width)
	req.Height = maxInt(1, req.Height)
	return req
}

func (p Previewer) previewSource(ctx context.Context, req PreviewRequest, kind Kind) (string, SourceKind, bool, error) {
	if kind == KindImage {
		if req.LocalPath != "" && fileExists(req.LocalPath) {
			return req.LocalPath, SourceLocal, false, nil
		}
		if req.ThumbnailPath != "" && fileExists(req.ThumbnailPath) {
			if !allowsThumbnailFallback(req) {
				return "", "", false, fmt.Errorf("full image is not downloaded; only a thumbnail is available")
			}
			return req.ThumbnailPath, SourceThumbnail, false, nil
		}
		if req.LocalPath != "" {
			return "", "", false, fmt.Errorf("image file is missing")
		}
		return "", "", false, fmt.Errorf("image is not downloaded")
	}
	if kind != KindVideo {
		return "", "", false, fmt.Errorf("unsupported media kind")
	}
	if req.LocalPath == "" {
		if req.ThumbnailPath != "" && fileExists(req.ThumbnailPath) {
			if !allowsThumbnailFallback(req) {
				return "", "", false, fmt.Errorf("full video is not downloaded; only a thumbnail is available")
			}
			return req.ThumbnailPath, SourceThumbnail, false, nil
		}
		return "", "", false, fmt.Errorf("video is not downloaded")
	}
	if !fileExists(req.LocalPath) {
		if req.ThumbnailPath != "" && fileExists(req.ThumbnailPath) {
			if !allowsThumbnailFallback(req) {
				return "", "", false, fmt.Errorf("video file is missing; only a thumbnail is available")
			}
			return req.ThumbnailPath, SourceThumbnail, false, nil
		}
		return "", "", false, fmt.Errorf("video file is missing")
	}

	thumbnailPath := p.videoThumbnailPath(req)
	if thumbnailPath == "" {
		return "", "", false, fmt.Errorf("preview cache is unavailable")
	}
	if fileExists(thumbnailPath) {
		return thumbnailPath, SourceGeneratedThumbnail, false, nil
	}
	if !hasCommand("ffmpeg") {
		return "", "", false, fmt.Errorf("ffmpeg not found")
	}
	if err := os.MkdirAll(filepath.Dir(thumbnailPath), 0o755); err != nil {
		return "", "", false, fmt.Errorf("create preview cache: %w", err)
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, p.timeout())
	defer cancel()
	args := []string{
		"-hide_banner",
		"-loglevel", "error",
		"-y",
		"-ss", "00:00:01",
		"-i", req.LocalPath,
		"-frames:v", "1",
		thumbnailPath,
	}
	if output, err := runPreviewCommand(timeoutCtx, "ffmpeg", args...); err != nil {
		return "", "", false, fmt.Errorf("generate video thumbnail: %s: %w", strings.TrimSpace(string(output)), err)
	}
	if !fileExists(thumbnailPath) {
		return "", "", false, fmt.Errorf("ffmpeg did not produce a thumbnail")
	}
	return thumbnailPath, SourceGeneratedThumbnail, true, nil
}

func allowsThumbnailFallback(req PreviewRequest) bool {
	return req.Compact || req.Backend == BackendChafa
}

func (p Previewer) renderSource(ctx context.Context, req PreviewRequest, source string) ([]string, Backend, PreviewDisplay, error) {
	timeoutCtx, cancel := context.WithTimeout(ctx, p.timeout())
	defer cancel()

	switch req.Backend {
	case BackendSixel:
		if hasCommand("chafa") {
			lines, err := runChafa(timeoutCtx, "sixels", req.Width, req.Height, source, false)
			if err == nil {
				return lines, BackendSixel, PreviewDisplayText, nil
			}
		}
		if hasCommand("img2sixel") {
			lines, err := runImg2Sixel(timeoutCtx, req.Width, req.Height, source)
			if err == nil {
				return lines, BackendSixel, PreviewDisplayText, nil
			}
		}
		return p.renderChafaFallback(timeoutCtx, req, source)
	case BackendUeberzugPP:
		return p.renderOverlayFallback(timeoutCtx, req, source), BackendUeberzugPP, PreviewDisplayOverlay, nil
	case BackendChafa, BackendAuto:
		return p.renderChafaFallback(timeoutCtx, req, source)
	default:
		return nil, BackendNone, "", fmt.Errorf("preview backend %s does not render inline", req.Backend)
	}
}

func (p Previewer) renderOverlayFallback(ctx context.Context, req PreviewRequest, source string) []string {
	if hasCommand("chafa") {
		if lines, _, _, err := p.renderChafaFallback(ctx, req, source); err == nil && len(lines) > 0 {
			return lines
		}
	}
	if req.Compact {
		return nil
	}
	return placeholderPreviewLines(req.Width, req.Height, MediaKind(req.MIMEType, req.FileName))
}

func (p Previewer) renderChafaFallback(ctx context.Context, req PreviewRequest, source string) ([]string, Backend, PreviewDisplay, error) {
	if !hasCommand("chafa") {
		return nil, BackendNone, "", fmt.Errorf("chafa not found")
	}
	lines, err := runChafa(ctx, "symbols", req.Width, req.Height, source, req.Compact)
	if err != nil {
		return nil, BackendChafa, "", err
	}
	return lines, BackendChafa, PreviewDisplayText, nil
}

func runChafa(ctx context.Context, format string, width, height int, source string, compact bool) ([]string, error) {
	args := []string{
		"--probe=off",
		"--animate=off",
		"--format=" + format,
		fmt.Sprintf("--size=%dx%d", width, height),
	}
	if format == "symbols" {
		if compact {
			args = append(args, "--symbols=half+quad+block", "--colors=full", "--work=9")
		} else {
			args = append(args, "--symbols=block", "--colors=full")
		}
	}
	args = append(args, source)

	output, err := runPreviewCommand(ctx, "chafa", args...)
	if err != nil {
		return nil, fmt.Errorf("chafa preview: %s: %w", strings.TrimSpace(string(output)), err)
	}
	return splitPreviewOutput(output), nil
}

func placeholderPreviewLines(width, height int, kind Kind) []string {
	width = maxInt(1, width)
	height = maxInt(1, height)

	label := "[preview]"
	switch kind {
	case KindImage:
		label = "[image preview]"
	case KindVideo:
		label = "[video preview]"
	}
	if len(label) > width {
		label = label[:width]
	}

	lines := make([]string, height)
	padding := strings.Repeat(" ", width)
	for i := range lines {
		lines[i] = padding
	}
	row := height / 2
	left := 0
	if width > len(label) {
		left = (width - len(label)) / 2
	}
	lines[row] = strings.Repeat(" ", left) + label + strings.Repeat(" ", maxInt(0, width-left-len(label)))
	return lines
}

func runImg2Sixel(ctx context.Context, width, height int, source string) ([]string, error) {
	cell := resolvedTerminalCellPixels()
	args := []string{
		"-w", fmt.Sprintf("%d", width*cell.Width),
		"-h", fmt.Sprintf("%d", height*cell.Height),
		source,
	}
	output, err := runPreviewCommand(ctx, "img2sixel", args...)
	if err != nil {
		return nil, fmt.Errorf("img2sixel preview: %s: %w", strings.TrimSpace(string(output)), err)
	}
	return splitPreviewOutput(output), nil
}

func resolvedTerminalCellPixels() terminalCellPixels {
	cell, ok := detectTerminalCellPixels()
	if !ok || cell.Width <= 0 || cell.Height <= 0 {
		return terminalCellPixels{Width: 8, Height: 16}
	}
	return cell
}

func (p Previewer) videoThumbnailPath(req PreviewRequest) string {
	cacheDir := req.CacheDir
	if cacheDir == "" {
		cacheDir = p.CacheDir
	}
	if cacheDir == "" {
		return ""
	}
	sum := sha1.Sum([]byte(req.MessageID + "\x00" + req.LocalPath))
	return filepath.Join(cacheDir, "thumbs", hex.EncodeToString(sum[:])+".jpg")
}

func (p Previewer) timeout() time.Duration {
	if p.Timeout <= 0 {
		return 3 * time.Second
	}
	return p.Timeout
}

func PreviewKey(req PreviewRequest) string {
	return strings.Join([]string{
		req.MessageID,
		req.MIMEType,
		req.FileName,
		req.LocalPath,
		req.ThumbnailPath,
		string(req.Backend),
		fmt.Sprintf("%dx%d", req.Width, req.Height),
		fmt.Sprintf("compact=%t", req.Compact),
	}, "\x00")
}

func MediaKind(mimeType, fileName string) Kind {
	mimeType = strings.ToLower(strings.TrimSpace(mimeType))
	fileName = strings.ToLower(strings.TrimSpace(fileName))
	switch {
	case strings.HasPrefix(mimeType, "image/"):
		return KindImage
	case strings.HasPrefix(mimeType, "video/"):
		return KindVideo
	case strings.HasPrefix(mimeType, "audio/"):
		return KindAudio
	case strings.HasSuffix(fileName, ".jpg"),
		strings.HasSuffix(fileName, ".jpeg"),
		strings.HasSuffix(fileName, ".png"),
		strings.HasSuffix(fileName, ".webp"),
		strings.HasSuffix(fileName, ".gif"):
		return KindImage
	case strings.HasSuffix(fileName, ".mp4"),
		strings.HasSuffix(fileName, ".mov"),
		strings.HasSuffix(fileName, ".mkv"),
		strings.HasSuffix(fileName, ".webm"):
		return KindVideo
	case strings.HasSuffix(fileName, ".ogg"),
		strings.HasSuffix(fileName, ".opus"),
		strings.HasSuffix(fileName, ".mp3"),
		strings.HasSuffix(fileName, ".m4a"),
		strings.HasSuffix(fileName, ".aac"),
		strings.HasSuffix(fileName, ".wav"),
		strings.HasSuffix(fileName, ".flac"),
		strings.HasSuffix(fileName, ".oga"):
		return KindAudio
	default:
		return KindUnsupported
	}
}

func splitPreviewOutput(output []byte) []string {
	text := strings.TrimRight(string(output), "\n")
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

func fileExists(path string) bool {
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}
