package media

import (
	"context"
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

	report := Detect("auto")
	if report.Selected != BackendUeberzugPP {
		t.Fatalf("Selected = %q, want %q", report.Selected, BackendUeberzugPP)
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
	if preview.Kind != KindVideo || !preview.GeneratedThumbnail || preview.ThumbnailPath == "" {
		t.Fatalf("preview video metadata = %+v", preview)
	}
	if !strings.HasPrefix(preview.ThumbnailPath, filepath.Join(cacheDir, "thumbs")) {
		t.Fatalf("thumbnail path = %q, want under cache thumbs", preview.ThumbnailPath)
	}
	if len(preview.Lines) != 1 || preview.Lines[0] != "thumbnail" {
		t.Fatalf("preview lines = %+v", preview.Lines)
	}
}

func TestMediaKindFallsBackFromFileExtension(t *testing.T) {
	if got := MediaKind("", "image.webp"); got != KindImage {
		t.Fatalf("MediaKind(image.webp) = %s, want image", got)
	}
	if got := MediaKind("", "movie.webm"); got != KindVideo {
		t.Fatalf("MediaKind(movie.webm) = %s, want video", got)
	}
	if got := MediaKind("application/pdf", "doc.pdf"); got != KindUnsupported {
		t.Fatalf("MediaKind(pdf) = %s, want unsupported", got)
	}
}
