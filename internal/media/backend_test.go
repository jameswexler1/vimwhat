//go:build !windows

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
	t.Setenv("VIMWHAT_FORCE_SIXEL", "0")

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
	t.Setenv("VIMWHAT_FORCE_SIXEL", "0")
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
	t.Setenv("VIMWHAT_FORCE_SIXEL", "0")
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
	t.Setenv("VIMWHAT_FORCE_SIXEL", "0")

	report := Detect("auto")
	if report.Selected != BackendNone {
		t.Fatalf("Selected = %q, want %q", report.Selected, BackendNone)
	}
}

func TestAvatarPreviewBackendPrefersSelectedOverlay(t *testing.T) {
	backend, ok := AvatarPreviewBackend(Report{
		Selected: BackendUeberzugPP,
		Reasons: map[Backend]string{
			BackendUeberzugPP: "available",
			BackendChafa:      "available",
		},
	})
	if !ok || backend != BackendUeberzugPP {
		t.Fatalf("AvatarPreviewBackend() = %q, %v; want %q, true", backend, ok, BackendUeberzugPP)
	}
}

func TestAvatarPreviewBackendUsesSelectedChafa(t *testing.T) {
	backend, ok := AvatarPreviewBackend(Report{
		Selected: BackendChafa,
		Reasons: map[Backend]string{
			BackendUeberzugPP: "available",
			BackendChafa:      "available",
		},
	})
	if !ok || backend != BackendChafa {
		t.Fatalf("AvatarPreviewBackend() = %q, %v; want %q, true", backend, ok, BackendChafa)
	}
}

func TestAvatarPreviewBackendAllowsSelectedSixel(t *testing.T) {
	backend, ok := AvatarPreviewBackend(Report{Selected: BackendSixel})
	if !ok || backend != BackendSixel {
		t.Fatalf("AvatarPreviewBackend() = %q, %v; want %q, true", backend, ok, BackendSixel)
	}
}

func TestAvatarPreviewBackendUnavailableForExternal(t *testing.T) {
	backend, ok := AvatarPreviewBackend(Report{
		Selected: BackendExternal,
		Reasons: map[Backend]string{
			BackendExternal: "available",
			BackendChafa:    "chafa not found in PATH",
		},
	})
	if ok || backend != "" {
		t.Fatalf("AvatarPreviewBackend() = %q, %v; want unavailable", backend, ok)
	}
}

func TestAvatarPreviewBackendHonorsPreviewNone(t *testing.T) {
	backend, ok := AvatarPreviewBackend(Report{
		Requested: BackendNone,
		Selected:  BackendNone,
		Reasons: map[Backend]string{
			BackendChafa: "available",
		},
	})
	if ok || backend != "" {
		t.Fatalf("AvatarPreviewBackend() = %q, %v; want unavailable", backend, ok)
	}
}

func TestPreviewKeyIncludesCompactFlag(t *testing.T) {
	request := PreviewRequest{
		MessageID: "m-1",
		MIMEType:  "image/jpeg",
		FileName:  "photo.jpg",
		LocalPath: "/tmp/photo.jpg",
		Backend:   BackendChafa,
		Width:     4,
		Height:    2,
	}
	compact := request
	compact.Compact = true

	if PreviewKey(request) == PreviewKey(compact) {
		t.Fatal("PreviewKey() did not distinguish compact and normal previews")
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
	if calledName != "chafa" || !slices.Contains(calledArgs, "--format=symbols") || !slices.Contains(calledArgs, "--size=10x4") || !slices.Contains(calledArgs, "--symbols=block") {
		t.Fatalf("called %s %v, want normal chafa symbols size 10x4", calledName, calledArgs)
	}
	if got := preview.Lines; len(got) != 2 || got[0] != "aa" || got[1] != "bb" {
		t.Fatalf("preview lines = %+v", preview.Lines)
	}
}

func TestPreviewerRendersCompactImageWithDetailedChafaSymbols(t *testing.T) {
	imagePath := filepath.Join(t.TempDir(), "avatar.jpg")
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
		MessageID: "chat-avatar:chat-1",
		MIMEType:  "image/jpeg",
		LocalPath: imagePath,
		Width:     4,
		Height:    2,
		Compact:   true,
	})

	if preview.Err != nil {
		t.Fatalf("Render() error = %v", preview.Err)
	}
	for _, want := range []string{"--format=symbols", "--size=4x2", "--symbols=half+quad+block", "--colors=full", "--work=9"} {
		if !slices.Contains(calledArgs, want) {
			t.Fatalf("called %s %v, want arg %s", calledName, calledArgs, want)
		}
	}
}

func TestPreviewerUsesDetectedCellPixelsForImg2Sixel(t *testing.T) {
	imagePath := filepath.Join(t.TempDir(), "photo.jpg")
	if err := os.WriteFile(imagePath, []byte("fake"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	prevLookPath := lookPath
	lookPath = func(name string) (string, error) {
		if name == "img2sixel" {
			return "/usr/bin/img2sixel", nil
		}
		return "", errors.New("not found")
	}
	t.Cleanup(func() {
		lookPath = prevLookPath
	})

	prevDetectCell := detectTerminalCellPixels
	detectTerminalCellPixels = func() (terminalCellPixels, bool) {
		return terminalCellPixels{Width: 9, Height: 17}, true
	}
	t.Cleanup(func() {
		detectTerminalCellPixels = prevDetectCell
	})

	var calledName string
	var calledArgs []string
	prevRun := runPreviewCommand
	runPreviewCommand = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		calledName = name
		calledArgs = slices.Clone(args)
		return []byte("\x1bPqfake\x1b\\\n"), nil
	}
	t.Cleanup(func() {
		runPreviewCommand = prevRun
	})

	previewer := NewPreviewer(Report{Selected: BackendSixel}, t.TempDir(), 20, 8)
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
	if calledName != "img2sixel" {
		t.Fatalf("called command = %q, want img2sixel", calledName)
	}
	if !slices.Contains(calledArgs, "-w") || !slices.Contains(calledArgs, "90") || !slices.Contains(calledArgs, "-h") || !slices.Contains(calledArgs, "68") {
		t.Fatalf("called args = %v, want -w 90 -h 68", calledArgs)
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

func TestCompactOverlayPreviewDoesNotUsePlaceholderFallback(t *testing.T) {
	imagePath := filepath.Join(t.TempDir(), "avatar.jpg")
	if err := os.WriteFile(imagePath, []byte("fake"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	prevLookPath := lookPath
	lookPath = func(name string) (string, error) {
		return "", errors.New("not found")
	}
	t.Cleanup(func() {
		lookPath = prevLookPath
	})

	previewer := NewPreviewer(Report{Selected: BackendUeberzugPP}, t.TempDir(), 20, 8)
	preview := previewer.Render(context.Background(), PreviewRequest{
		MessageID: "chat-avatar:chat-1",
		MIMEType:  "image/jpeg",
		LocalPath: imagePath,
		Backend:   BackendUeberzugPP,
		Width:     4,
		Height:    2,
		Compact:   true,
	})

	if preview.Err != nil {
		t.Fatalf("Render() error = %v", preview.Err)
	}
	if preview.Display != PreviewDisplayOverlay || !preview.Ready() {
		t.Fatalf("preview = %+v, want ready overlay", preview)
	}
	if len(preview.Lines) != 0 {
		t.Fatalf("preview lines = %+v, want no placeholder compact fallback", preview.Lines)
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

func TestOverlayManagerSyncEpochIgnoresStalePlacements(t *testing.T) {
	var buf bytes.Buffer
	manager := NewOverlayManagerForWriter(&buf)
	staleEpoch := manager.Epoch()
	manager.Invalidate()

	if err := manager.SyncEpoch(context.Background(), staleEpoch, []Placement{
		{Identifier: "media-1", X: 1, Y: 2, MaxWidth: 20, MaxHeight: 8, Path: "/tmp/one.jpg"},
	}); err != nil {
		t.Fatalf("SyncEpoch(stale) error = %v", err)
	}
	if strings.TrimSpace(buf.String()) != "" {
		t.Fatalf("stale SyncEpoch wrote overlay commands:\n%s", buf.String())
	}

	if err := manager.SyncEpoch(context.Background(), manager.Epoch(), []Placement{
		{Identifier: "media-1", X: 1, Y: 2, MaxWidth: 20, MaxHeight: 8, Path: "/tmp/one.jpg"},
	}); err != nil {
		t.Fatalf("SyncEpoch(current) error = %v", err)
	}
	commands := parseOverlayCommands(t, buf.String())
	if len(commands) != 1 || commands[0].Action != "add" || commands[0].Identifier != "media-1" {
		t.Fatalf("commands = %+v, want current add", commands)
	}
}

func TestOverlayManagerSyncEpochHonorsCanceledContext(t *testing.T) {
	var buf bytes.Buffer
	manager := NewOverlayManagerForWriter(&buf)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := manager.SyncEpoch(ctx, manager.Epoch(), []Placement{
		{Identifier: "media-1", X: 1, Y: 2, MaxWidth: 20, MaxHeight: 8, Path: "/tmp/one.jpg"},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("SyncEpoch() error = %v, want context canceled", err)
	}
	if buf.Len() != 0 {
		t.Fatalf("canceled SyncEpoch wrote overlay commands:\n%s", buf.String())
	}
}

func TestSixelManagerDrawsSkipsAndClearsPlacements(t *testing.T) {
	var buf bytes.Buffer
	manager := NewSixelManagerForWriter(&buf)
	placement := SixelPlacement{
		Identifier: "image-1",
		X:          1,
		Y:          2,
		MaxWidth:   3,
		MaxHeight:  2,
		Payload:    []string{"\x1bPqfake\x1b\\"},
	}

	if err := manager.SyncEpoch(context.Background(), manager.Epoch(), []SixelPlacement{placement}); err != nil {
		t.Fatalf("SyncEpoch() error = %v", err)
	}
	if out := buf.String(); !strings.Contains(out, "\x1b[3;2H") || !strings.Contains(out, placement.Payload[0]) {
		t.Fatalf("sixel output = %q, want cursor placement and payload", out)
	}

	buf.Reset()
	if err := manager.SyncEpoch(context.Background(), manager.Epoch(), []SixelPlacement{placement}); err != nil {
		t.Fatalf("unchanged SyncEpoch() error = %v", err)
	}
	if buf.Len() != 0 {
		t.Fatalf("unchanged placement wrote output: %q", buf.String())
	}

	buf.Reset()
	moved := placement
	moved.X = 4
	if err := manager.SyncEpoch(context.Background(), manager.Epoch(), []SixelPlacement{moved}); err != nil {
		t.Fatalf("moved SyncEpoch() error = %v", err)
	}
	if out := buf.String(); !strings.Contains(out, "\x1b[3;2H") || !strings.Contains(out, "\x1b[3;5H") || !strings.Contains(out, moved.Payload[0]) {
		t.Fatalf("moved sixel output = %q, want old clear, new cursor, and payload", out)
	}

	buf.Reset()
	if err := manager.SyncEpoch(context.Background(), manager.Epoch(), nil); err != nil {
		t.Fatalf("clear SyncEpoch() error = %v", err)
	}
	if out := buf.String(); !strings.Contains(out, "\x1b[3;5H") || strings.Contains(out, moved.Payload[0]) {
		t.Fatalf("clear sixel output = %q, want clear without payload", out)
	}
}

func TestSixelManagerSyncEpochIgnoresStalePlacements(t *testing.T) {
	var buf bytes.Buffer
	manager := NewSixelManagerForWriter(&buf)
	staleEpoch := manager.Epoch()
	manager.Invalidate()

	if err := manager.SyncEpoch(context.Background(), staleEpoch, []SixelPlacement{{
		Identifier: "image-1",
		X:          1,
		Y:          2,
		MaxWidth:   3,
		MaxHeight:  2,
		Payload:    []string{"\x1bPqfake\x1b\\"},
	}}); err != nil {
		t.Fatalf("stale SyncEpoch() error = %v", err)
	}
	if buf.Len() != 0 {
		t.Fatalf("stale SyncEpoch() wrote output: %q", buf.String())
	}
}

func TestSixelManagerSyncEpochHonorsCanceledContext(t *testing.T) {
	var buf bytes.Buffer
	manager := NewSixelManagerForWriter(&buf)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := manager.SyncEpoch(ctx, manager.Epoch(), []SixelPlacement{{
		Identifier: "image-1",
		X:          1,
		Y:          2,
		MaxWidth:   3,
		MaxHeight:  2,
		Payload:    []string{"\x1bPqfake\x1b\\"},
	}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("SyncEpoch() error = %v, want context canceled", err)
	}
	if buf.Len() != 0 {
		t.Fatalf("canceled SyncEpoch() wrote output: %q", buf.String())
	}
}

func parseOverlayCommands(t *testing.T, value string) []overlayCommand {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(value), "\n")
	var commands []overlayCommand
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var command overlayCommand
		if err := json.Unmarshal([]byte(line), &command); err != nil {
			t.Fatalf("Unmarshal(%q) error = %v", line, err)
		}
		commands = append(commands, command)
	}
	return commands
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
