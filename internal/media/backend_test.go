package media

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestDetectExplicitAvailableBackend(t *testing.T) {
	prevLookPath := lookPath
	lookPath = func(name string) (string, error) {
		if name == "chafa" {
			return "/usr/bin/chafa", nil
		}
		return "", errors.New("not found")
	}
	t.Cleanup(func() {
		lookPath = prevLookPath
	})

	t.Setenv("TERM", "xterm-256color")
	t.Setenv("TERM_PROGRAM", "")
	t.Setenv("MAYBEWHATS_FORCE_SIXEL", "0")

	report := Detect("chafa")
	if report.Selected != BackendChafa {
		t.Fatalf("Selected = %q, want %q", report.Selected, BackendChafa)
	}
}

func TestDetectFallsBackInPriorityOrder(t *testing.T) {
	prevLookPath := lookPath
	lookPath = func(name string) (string, error) {
		switch name {
		case "ueberzugpp":
			return "/usr/bin/ueberzugpp", nil
		case "xdg-open":
			return "/usr/bin/xdg-open", nil
		default:
			return "", errors.New("not found")
		}
	}
	t.Cleanup(func() {
		lookPath = prevLookPath
	})

	t.Setenv("TERM", "xterm-256color")
	t.Setenv("TERM_PROGRAM", "")
	t.Setenv("MAYBEWHATS_FORCE_SIXEL", "0")
	t.Setenv("DISPLAY", ":0")

	report := Detect("auto")
	if report.Selected != BackendUeberzugPP {
		t.Fatalf("Selected = %q, want %q", report.Selected, BackendUeberzugPP)
	}
	if report.UeberzugPPOutput != "x11" {
		t.Fatalf("UeberzugPPOutput = %q, want x11", report.UeberzugPPOutput)
	}
}

func TestDetectChafaExplicitBeatsAvailableUeberzugPP(t *testing.T) {
	prevLookPath := lookPath
	lookPath = func(name string) (string, error) {
		switch name {
		case "chafa", "ueberzugpp", "xdg-open":
			return "/usr/bin/" + name, nil
		default:
			return "", errors.New("not found")
		}
	}
	t.Cleanup(func() {
		lookPath = prevLookPath
	})

	t.Setenv("TERM", "xterm-256color")
	t.Setenv("TERM_PROGRAM", "")
	t.Setenv("MAYBEWHATS_FORCE_SIXEL", "0")
	t.Setenv("DISPLAY", ":0")

	report := Detect("chafa")
	if report.Selected != BackendChafa {
		t.Fatalf("Selected = %q, want %q", report.Selected, BackendChafa)
	}
	if got := report.QualityPath(); !strings.Contains(got, "forced by preview_backend=chafa") || !strings.Contains(got, "ueberzug++ is available") {
		t.Fatalf("QualityPath() = %q, want forced chafa warning", got)
	}
}

func TestDetectReportsNoneWhenNoBackendIsAvailable(t *testing.T) {
	prevLookPath := lookPath
	lookPath = func(name string) (string, error) {
		return "", errors.New("not found")
	}
	t.Cleanup(func() {
		lookPath = prevLookPath
	})

	t.Setenv("TERM", "xterm-256color")
	t.Setenv("TERM_PROGRAM", "")
	t.Setenv("MAYBEWHATS_FORCE_SIXEL", "0")

	report := Detect("auto")
	if report.Selected != BackendNone {
		t.Fatalf("Selected = %q, want %q", report.Selected, BackendNone)
	}
}

func TestPreviewerRendersImageWithChafaSymbols(t *testing.T) {
	imagePath := filepath.Join(t.TempDir(), "photo.jpg")
	if err := os.WriteFile(imagePath, []byte("fake"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	prevLookPath := lookPath
	lookPath = func(name string) (string, error) {
		if name == "chafa" {
			return "/usr/bin/chafa", nil
		}
		return "", errors.New("not found")
	}
	t.Cleanup(func() {
		lookPath = prevLookPath
	})

	var calledName string
	var calledArgs []string
	prevRun := runPreviewCommand
	runPreviewCommand = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		calledName = name
		calledArgs = slices.Clone(args)
		return []byte("aa\nbb\n"), nil
	}
	t.Cleanup(func() {
		runPreviewCommand = prevRun
	})

	previewer := NewPreviewer(Report{Selected: BackendChafa}, t.TempDir(), 20, 8)
	preview := previewer.Render(context.Background(), PreviewRequest{
		MessageID: "m-1",
		MIMEType:  "image/jpeg",
		LocalPath: imagePath,
		Width:     10,
		Height:    4,
	})

	if preview.Err != nil {
		t.Fatalf("Render() error = %v", preview.Err)
	}
	if calledName != "chafa" || !slices.Contains(calledArgs, "--format=symbols") || !slices.Contains(calledArgs, "--size=10x4") {
		t.Fatalf("called %s %v, want chafa symbols size 10x4", calledName, calledArgs)
	}
	if got := preview.Lines; len(got) != 2 || got[0] != "aa" || got[1] != "bb" {
		t.Fatalf("preview lines = %+v", preview.Lines)
	}
}

func TestPreviewerReturnsOverlayPreviewForUeberzugPP(t *testing.T) {
	imagePath := filepath.Join(t.TempDir(), "photo.jpg")
	if err := os.WriteFile(imagePath, []byte("fake"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	previewer := NewPreviewer(Report{Selected: BackendUeberzugPP}, t.TempDir(), 20, 8)
	preview := previewer.Render(context.Background(), PreviewRequest{
		MessageID: "m-1",
		MIMEType:  "image/jpeg",
		LocalPath: imagePath,
		Width:     10,
		Height:    4,
	})

	if preview.Err != nil {
		t.Fatalf("Render() error = %v", preview.Err)
	}
	if preview.Display != PreviewDisplayOverlay || preview.RenderedBackend != BackendUeberzugPP {
		t.Fatalf("preview display/backend = %s/%s, want overlay/ueberzug++", preview.Display, preview.RenderedBackend)
	}
	if preview.SourcePath != imagePath || preview.SourceKind != SourceLocal || preview.Width != 10 || preview.Height != 4 || !preview.Ready() {
		t.Fatalf("overlay preview metadata = %+v", preview)
	}
}

func TestPreviewerProvidesFallbackLinesForUeberzugPP(t *testing.T) {
	imagePath := filepath.Join(t.TempDir(), "photo.jpg")
	if err := os.WriteFile(imagePath, []byte("fake"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	prevLookPath := lookPath
	lookPath = func(name string) (string, error) {
		if name == "chafa" {
			return "/usr/bin/chafa", nil
		}
		return "", errors.New("not found")
	}
	t.Cleanup(func() {
		lookPath = prevLookPath
	})

	prevRun := runPreviewCommand
	runPreviewCommand = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if name != "chafa" {
			t.Fatalf("unexpected command %q", name)
		}
		return []byte("aa\nbb\n"), nil
	}
	t.Cleanup(func() {
		runPreviewCommand = prevRun
	})

	previewer := NewPreviewer(Report{Selected: BackendUeberzugPP}, t.TempDir(), 20, 8)
	preview := previewer.Render(context.Background(), PreviewRequest{
		MessageID: "m-1",
		MIMEType:  "image/jpeg",
		LocalPath: imagePath,
		Width:     10,
		Height:    4,
	})

	if preview.Err != nil {
		t.Fatalf("Render() error = %v", preview.Err)
	}
	if preview.Display != PreviewDisplayOverlay || preview.RenderedBackend != BackendUeberzugPP {
		t.Fatalf("preview display/backend = %s/%s, want overlay/ueberzug++", preview.Display, preview.RenderedBackend)
	}
	if got := preview.Lines; len(got) != 2 || got[0] != "aa" || got[1] != "bb" {
		t.Fatalf("preview lines = %+v, want chafa fallback lines", got)
	}
}

func TestPreviewerPrefersFullImageOverThumbnail(t *testing.T) {
	dir := t.TempDir()
	imagePath := filepath.Join(dir, "photo.jpg")
	thumbPath := filepath.Join(dir, "thumb.jpg")
	if err := os.WriteFile(imagePath, []byte("full"), 0o644); err != nil {
		t.Fatalf("WriteFile(image) error = %v", err)
	}
	if err := os.WriteFile(thumbPath, []byte("thumb"), 0o644); err != nil {
		t.Fatalf("WriteFile(thumb) error = %v", err)
	}

	previewer := NewPreviewer(Report{Selected: BackendUeberzugPP}, t.TempDir(), 20, 8)
	preview := previewer.Render(context.Background(), PreviewRequest{
		MessageID:     "m-1",
		MIMEType:      "image/jpeg",
		LocalPath:     imagePath,
		ThumbnailPath: thumbPath,
		Width:         10,
		Height:        4,
	})

	if preview.Err != nil {
		t.Fatalf("Render() error = %v", preview.Err)
	}
	if preview.SourcePath != imagePath || preview.SourceKind != SourceLocal {
		t.Fatalf("preview source = %q %s, want full local image", preview.SourcePath, preview.SourceKind)
	}
}

func TestPreviewerRejectsImageThumbnailOnlyForOverlayBackend(t *testing.T) {
	thumbPath := filepath.Join(t.TempDir(), "thumb.jpg")
	if err := os.WriteFile(thumbPath, []byte("thumb"), 0o644); err != nil {
		t.Fatalf("WriteFile(thumb) error = %v", err)
	}

	previewer := NewPreviewer(Report{Selected: BackendUeberzugPP}, t.TempDir(), 20, 8)
	preview := previewer.Render(context.Background(), PreviewRequest{
		MessageID:     "m-1",
		MIMEType:      "image/jpeg",
		ThumbnailPath: thumbPath,
		Width:         10,
		Height:        4,
	})

	if preview.Err == nil || !strings.Contains(preview.Err.Error(), "only a thumbnail") {
		t.Fatalf("Render() error = %v, want thumbnail-only refusal", preview.Err)
	}
	if preview.SourcePath != "" || preview.SourceKind != "" {
		t.Fatalf("preview source = %q %s, want no thumbnail overlay", preview.SourcePath, preview.SourceKind)
	}
}

func TestPreviewerAllowsImageThumbnailFallbackForChafa(t *testing.T) {
	thumbPath := filepath.Join(t.TempDir(), "thumb.jpg")
	if err := os.WriteFile(thumbPath, []byte("thumb"), 0o644); err != nil {
		t.Fatalf("WriteFile(thumb) error = %v", err)
	}

	prevLookPath := lookPath
	lookPath = func(name string) (string, error) {
		if name == "chafa" {
			return "/usr/bin/chafa", nil
		}
		return "", errors.New("not found")
	}
	t.Cleanup(func() {
		lookPath = prevLookPath
	})

	prevRun := runPreviewCommand
	runPreviewCommand = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if name != "chafa" {
			t.Fatalf("unexpected command %q", name)
		}
		return []byte("thumb\n"), nil
	}
	t.Cleanup(func() {
		runPreviewCommand = prevRun
	})

	previewer := NewPreviewer(Report{Selected: BackendChafa}, t.TempDir(), 20, 8)
	preview := previewer.Render(context.Background(), PreviewRequest{
		MessageID:     "m-1",
		MIMEType:      "image/jpeg",
		ThumbnailPath: thumbPath,
		Width:         10,
		Height:        4,
	})

	if preview.Err != nil {
		t.Fatalf("Render() error = %v", preview.Err)
	}
	if preview.SourcePath != thumbPath || preview.SourceKind != SourceThumbnail {
		t.Fatalf("preview source = %q %s, want thumbnail fallback", preview.SourcePath, preview.SourceKind)
	}
	if len(preview.Lines) != 1 || preview.Lines[0] != "thumb" {
		t.Fatalf("preview lines = %+v", preview.Lines)
	}
}

func TestOverlayManagerSendsAddAndRemoveJSON(t *testing.T) {
	var buf bytes.Buffer
	manager := NewOverlayManagerForWriter(&buf)

	if err := manager.Place(context.Background(), Placement{
		Identifier: "media-1",
		X:          4,
		Y:          5,
		MaxWidth:   20,
		MaxHeight:  8,
		Path:       "/tmp/photo.jpg",
	}); err != nil {
		t.Fatalf("Place() error = %v", err)
	}
	if err := manager.Remove(context.Background()); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("overlay commands = %q, want add and remove", buf.String())
	}
	var add overlayCommand
	if err := json.Unmarshal([]byte(lines[0]), &add); err != nil {
		t.Fatalf("unmarshal add: %v", err)
	}
	if add.Action != "add" || add.Identifier != "media-1" || add.X != 4 || add.Y != 5 || add.MaxWidth != 20 || add.MaxHeight != 8 || add.Path != "/tmp/photo.jpg" || add.Scaler != "fit_contain" {
		t.Fatalf("add command = %+v", add)
	}
	var remove overlayCommand
	if err := json.Unmarshal([]byte(lines[1]), &remove); err != nil {
		t.Fatalf("unmarshal remove: %v", err)
	}
	if remove.Action != "remove" || remove.Identifier != "media-1" {
		t.Fatalf("remove command = %+v", remove)
	}
}

func TestOverlayManagerSyncKeepsMultipleVisiblePlacements(t *testing.T) {
	var buf bytes.Buffer
	manager := NewOverlayManagerForWriter(&buf)

	if err := manager.Sync(context.Background(), []Placement{
		{Identifier: "media-1", X: 1, Y: 2, MaxWidth: 20, MaxHeight: 8, Path: "/tmp/one.jpg"},
		{Identifier: "media-2", X: 4, Y: 6, MaxWidth: 12, MaxHeight: 5, Path: "/tmp/two.jpg"},
	}); err != nil {
		t.Fatalf("Sync(add) error = %v", err)
	}
	if err := manager.Sync(context.Background(), []Placement{
		{Identifier: "media-2", X: 4, Y: 7, MaxWidth: 12, MaxHeight: 5, Path: "/tmp/two.jpg"},
	}); err != nil {
		t.Fatalf("Sync(update) error = %v", err)
	}

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 5 {
		t.Fatalf("commands = %q, want 5 add/remove commands", buf.String())
	}
	var commands []overlayCommand
	for _, line := range lines {
		var command overlayCommand
		if err := json.Unmarshal([]byte(line), &command); err != nil {
			t.Fatalf("Unmarshal(%q) error = %v", line, err)
		}
		commands = append(commands, command)
	}
	if commands[0].Action != "add" || commands[0].Identifier != "media-1" {
		t.Fatalf("first command = %+v, want add media-1", commands[0])
	}
	if commands[1].Action != "add" || commands[1].Identifier != "media-2" {
		t.Fatalf("second command = %+v, want add media-2", commands[1])
	}
	if commands[2].Action != "remove" || commands[2].Identifier != "media-1" {
		t.Fatalf("third command = %+v, want remove media-1", commands[2])
	}
	if commands[3].Action != "remove" || commands[3].Identifier != "media-2" {
		t.Fatalf("fourth command = %+v, want remove changed media-2", commands[3])
	}
	if commands[4].Action != "add" || commands[4].Identifier != "media-2" || commands[4].Y != 7 {
		t.Fatalf("fifth command = %+v, want updated media-2", commands[4])
	}
}

func TestPreviewerGeneratesVideoThumbnail(t *testing.T) {
	videoPath := filepath.Join(t.TempDir(), "clip.mp4")
	if err := os.WriteFile(videoPath, []byte("fake"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	cacheDir := t.TempDir()

	prevLookPath := lookPath
	lookPath = func(name string) (string, error) {
		switch name {
		case "chafa", "ffmpeg":
			return "/usr/bin/" + name, nil
		default:
			return "", errors.New("not found")
		}
	}
	t.Cleanup(func() {
		lookPath = prevLookPath
	})

	prevRun := runPreviewCommand
	runPreviewCommand = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		switch name {
		case "ffmpeg":
			output := args[len(args)-1]
			if err := os.WriteFile(output, []byte("jpg"), 0o644); err != nil {
				t.Fatalf("write generated thumbnail error = %v", err)
			}
			return nil, nil
		case "chafa":
			return []byte("thumbnail\n"), nil
		default:
			t.Fatalf("unexpected command %q", name)
			return nil, nil
		}
	}
	t.Cleanup(func() {
		runPreviewCommand = prevRun
	})

	previewer := NewPreviewer(Report{Selected: BackendChafa}, cacheDir, 20, 8)
	preview := previewer.Render(context.Background(), PreviewRequest{
		MessageID: "m-1",
		MIMEType:  "video/mp4",
		LocalPath: videoPath,
		Width:     10,
		Height:    4,
	})

	if preview.Err != nil {
		t.Fatalf("Render() error = %v", preview.Err)
	}
	if preview.Kind != KindVideo || preview.SourceKind != SourceGeneratedThumbnail || !preview.GeneratedThumbnail || preview.ThumbnailPath == "" {
		t.Fatalf("preview video metadata = %+v", preview)
	}
	if !strings.HasPrefix(preview.ThumbnailPath, filepath.Join(cacheDir, "thumbs")) {
		t.Fatalf("thumbnail path = %q, want under cache thumbs", preview.ThumbnailPath)
	}
	if len(preview.Lines) != 1 || preview.Lines[0] != "thumbnail" {
		t.Fatalf("preview lines = %+v", preview.Lines)
	}
}

func TestPreviewerIgnoresProvidedVideoThumbnailWhenLocalVideoExists(t *testing.T) {
	dir := t.TempDir()
	videoPath := filepath.Join(dir, "clip.mp4")
	remoteThumbPath := filepath.Join(dir, "remote-thumb.jpg")
	if err := os.WriteFile(videoPath, []byte("fake"), 0o644); err != nil {
		t.Fatalf("WriteFile(video) error = %v", err)
	}
	if err := os.WriteFile(remoteThumbPath, []byte("remote thumb"), 0o644); err != nil {
		t.Fatalf("WriteFile(remote thumb) error = %v", err)
	}
	cacheDir := t.TempDir()

	prevLookPath := lookPath
	lookPath = func(name string) (string, error) {
		switch name {
		case "ffmpeg":
			return "/usr/bin/ffmpeg", nil
		default:
			return "", errors.New("not found")
		}
	}
	t.Cleanup(func() {
		lookPath = prevLookPath
	})

	prevRun := runPreviewCommand
	runPreviewCommand = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if name != "ffmpeg" {
			t.Fatalf("unexpected command %q", name)
		}
		output := args[len(args)-1]
		if err := os.WriteFile(output, []byte("generated"), 0o644); err != nil {
			t.Fatalf("write generated thumbnail error = %v", err)
		}
		return nil, nil
	}
	t.Cleanup(func() {
		runPreviewCommand = prevRun
	})

	previewer := NewPreviewer(Report{Selected: BackendUeberzugPP}, cacheDir, 20, 8)
	preview := previewer.Render(context.Background(), PreviewRequest{
		MessageID:     "m-1",
		MIMEType:      "video/mp4",
		LocalPath:     videoPath,
		ThumbnailPath: remoteThumbPath,
		Width:         10,
		Height:        4,
	})

	if preview.Err != nil {
		t.Fatalf("Render() error = %v", preview.Err)
	}
	if preview.SourcePath == remoteThumbPath || preview.SourceKind != SourceGeneratedThumbnail {
		t.Fatalf("preview source = %q %s, want generated cache thumbnail", preview.SourcePath, preview.SourceKind)
	}
	if !strings.HasPrefix(preview.SourcePath, filepath.Join(cacheDir, "thumbs")) {
		t.Fatalf("generated source = %q, want cache thumbnail", preview.SourcePath)
	}
}

func TestMediaKindFallsBackFromFileExtension(t *testing.T) {
	if got := MediaKind("", "image.webp"); got != KindImage {
		t.Fatalf("MediaKind(image.webp) = %s, want image", got)
	}
	if got := MediaKind("", "movie.webm"); got != KindVideo {
		t.Fatalf("MediaKind(movie.webm) = %s, want video", got)
	}
	if got := MediaKind("audio/ogg", "voice.ogg"); got != KindAudio {
		t.Fatalf("MediaKind(audio/ogg) = %s, want audio", got)
	}
	if got := MediaKind("", "voice.opus"); got != KindAudio {
		t.Fatalf("MediaKind(voice.opus) = %s, want audio", got)
	}
	if got := MediaKind("application/pdf", "doc.pdf"); got != KindUnsupported {
		t.Fatalf("MediaKind(pdf) = %s, want unsupported", got)
	}
}

func TestSaveLocalFileUsesCollisionSafeNames(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source.jpg")
	if err := os.WriteFile(source, []byte("image"), 0o644); err != nil {
		t.Fatalf("WriteFile(source) error = %v", err)
	}
	downloads := filepath.Join(dir, "downloads")
	if err := os.MkdirAll(downloads, 0o755); err != nil {
		t.Fatalf("MkdirAll(downloads) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(downloads, "photo.jpg"), []byte("existing"), 0o644); err != nil {
		t.Fatalf("WriteFile(existing) error = %v", err)
	}

	saved, err := SaveLocalFile(SaveRequest{
		SourcePath:   source,
		FileName:     "photo.jpg",
		MIMEType:     "image/jpeg",
		DownloadsDir: downloads,
	})
	if err != nil {
		t.Fatalf("SaveLocalFile() error = %v", err)
	}
	if filepath.Base(saved) != "photo-1.jpg" {
		t.Fatalf("saved path = %q, want photo-1.jpg", saved)
	}
	data, err := os.ReadFile(saved)
	if err != nil {
		t.Fatalf("ReadFile(saved) error = %v", err)
	}
	if string(data) != "image" {
		t.Fatalf("saved data = %q, want image", data)
	}
}
