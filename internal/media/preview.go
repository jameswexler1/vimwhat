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

	if preview.Kind == KindUnsupported {
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
			return req.ThumbnailPath, SourceThumbnail, false, nil
		}
		return "", "", false, fmt.Errorf("video is not downloaded")
	}
	if !fileExists(req.LocalPath) {
		if req.ThumbnailPath != "" && fileExists(req.ThumbnailPath) {
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

func (p Previewer) renderSource(ctx context.Context, req PreviewRequest, source string) ([]string, Backend, PreviewDisplay, error) {
	timeoutCtx, cancel := context.WithTimeout(ctx, p.timeout())
	defer cancel()

	switch req.Backend {
	case BackendSixel:
		if hasCommand("chafa") {
			lines, err := runChafa(timeoutCtx, "sixels", req.Width, req.Height, source)
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
		return nil, BackendUeberzugPP, PreviewDisplayOverlay, nil
	case BackendChafa, BackendAuto:
		return p.renderChafaFallback(timeoutCtx, req, source)
	default:
		return nil, BackendNone, "", fmt.Errorf("preview backend %s does not render inline", req.Backend)
	}
}

func (p Previewer) renderChafaFallback(ctx context.Context, req PreviewRequest, source string) ([]string, Backend, PreviewDisplay, error) {
	if !hasCommand("chafa") {
		return nil, BackendNone, "", fmt.Errorf("chafa not found")
	}
	lines, err := runChafa(ctx, "symbols", req.Width, req.Height, source)
	if err != nil {
		return nil, BackendChafa, "", err
	}
	return lines, BackendChafa, PreviewDisplayText, nil
}

func runChafa(ctx context.Context, format string, width, height int, source string) ([]string, error) {
	args := []string{
		"--probe=off",
		"--animate=off",
		"--format=" + format,
		fmt.Sprintf("--size=%dx%d", width, height),
	}
	if format == "symbols" {
		args = append(args, "--symbols=block", "--colors=full")
	}
	args = append(args, source)

	output, err := runPreviewCommand(ctx, "chafa", args...)
	if err != nil {
		return nil, fmt.Errorf("chafa preview: %s: %w", strings.TrimSpace(string(output)), err)
	}
	return splitPreviewOutput(output), nil
}

func runImg2Sixel(ctx context.Context, width, height int, source string) ([]string, error) {
	args := []string{
		"-w", fmt.Sprintf("%d", width*8),
		"-h", fmt.Sprintf("%d", height*16),
		source,
	}
	output, err := runPreviewCommand(ctx, "img2sixel", args...)
	if err != nil {
		return nil, fmt.Errorf("img2sixel preview: %s: %w", strings.TrimSpace(string(output)), err)
	}
	return splitPreviewOutput(output), nil
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
