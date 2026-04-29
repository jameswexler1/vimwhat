//go:build !windows

package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"vimwhat/internal/config"
	"vimwhat/internal/store"
	"vimwhat/internal/ui"
)

func TestReadImageFromClipboardStdoutCommandStagesAttachment(t *testing.T) {
	source := writeTinyPNG(t, t.TempDir(), "source.png")
	paths := config.Paths{MediaDir: filepath.Join(t.TempDir(), "media")}

	attachment, err := readImageFromClipboard(context.Background(), paths, `sh -c "cat `+source+`"`)
	if err != nil {
		t.Fatalf("readImageFromClipboard() error = %v", err)
	}
	if attachment.LocalPath == "" || attachment.FileName == "" || attachment.MIMEType != "image/png" || attachment.DownloadState != "local_pending" {
		t.Fatalf("attachment = %+v, want staged png attachment", attachment)
	}
	if !strings.HasPrefix(attachment.LocalPath, paths.MediaDir) {
		t.Fatalf("LocalPath = %q, want under %q", attachment.LocalPath, paths.MediaDir)
	}
}

func TestReadImageFromClipboardPathTemplateCommandStagesAttachment(t *testing.T) {
	source := writeTinyPNG(t, t.TempDir(), "source.png")
	paths := config.Paths{MediaDir: filepath.Join(t.TempDir(), "media")}

	attachment, err := readImageFromClipboard(context.Background(), paths, `sh -c "cp `+source+` {path}"`)
	if err != nil {
		t.Fatalf("readImageFromClipboard() error = %v", err)
	}
	if attachment.LocalPath == "" || attachment.MIMEType != "image/png" {
		t.Fatalf("attachment = %+v, want staged png attachment", attachment)
	}
	if !strings.HasPrefix(attachment.LocalPath, paths.MediaDir) {
		t.Fatalf("LocalPath = %q, want under %q", attachment.LocalPath, paths.MediaDir)
	}
}

func TestReadImageFromClipboardRejectsNonImageData(t *testing.T) {
	paths := config.Paths{MediaDir: filepath.Join(t.TempDir(), "media")}

	_, err := readImageFromClipboard(context.Background(), paths, `sh -c "printf not-image"`)
	if err == nil || !strings.Contains(err.Error(), "image") {
		t.Fatalf("readImageFromClipboard() error = %v, want image rejection", err)
	}
}

func TestWriteImageToClipboardPipesImageWhenPlaceholderMissing(t *testing.T) {
	dir := t.TempDir()
	source := writeTinyPNG(t, dir, "source.png")
	target := filepath.Join(dir, "copied.png")

	err := writeImageToClipboard(context.Background(), `sh -c "cat > `+target+`"`, store.MediaMetadata{
		LocalPath: source,
		MIMEType:  "image/png",
		FileName:  "source.png",
	})
	if err != nil {
		t.Fatalf("writeImageToClipboard() error = %v", err)
	}
	if got, want := mustReadFile(t, target), mustReadFile(t, source); !bytes.Equal(got, want) {
		t.Fatalf("copied bytes differ from source")
	}
}

func TestWriteImageToClipboardReplacesPathAndMIMEPlaceholders(t *testing.T) {
	dir := t.TempDir()
	source := writeTinyPNG(t, dir, "source.png")
	target := filepath.Join(dir, "copied.png")
	mimePath := filepath.Join(dir, "mime.txt")

	err := writeImageToClipboard(context.Background(), `sh -c "printf %s {mime} > `+mimePath+`; cat {path} > `+target+`"`, store.MediaMetadata{
		LocalPath: source,
		MIMEType:  "image/png",
		FileName:  "source.png",
	})
	if err != nil {
		t.Fatalf("writeImageToClipboard() error = %v", err)
	}
	if got := strings.TrimSpace(string(mustReadFile(t, mimePath))); got != "image/png" {
		t.Fatalf("mime placeholder = %q, want image/png", got)
	}
	if got, want := mustReadFile(t, target), mustReadFile(t, source); !bytes.Equal(got, want) {
		t.Fatalf("copied bytes differ from source")
	}
}

func TestAudioPlayerCommandUsesConfiguredTemplate(t *testing.T) {
	cmd, path, err := audioPlayerCommand(config.Config{
		AudioPlayerCommand: "sh -c true {path}",
	}, store.MediaMetadata{
		LocalPath: "/tmp/voice.ogg",
		MIMEType:  "audio/ogg",
		FileName:  "voice.ogg",
	})
	if err != nil {
		t.Fatalf("audioPlayerCommand() error = %v", err)
	}
	if path != "/tmp/voice.ogg" {
		t.Fatalf("path = %q, want /tmp/voice.ogg", path)
	}
	if got := strings.Join(cmd.Args, "\x00"); got != "sh\x00-c\x00true\x00/tmp/voice.ogg" {
		t.Fatalf("cmd.Args = %#v", cmd.Args)
	}
}

func TestStickerPickerCandidatesUseCachedRenderableStickers(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "state.sqlite3"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	dir := t.TempDir()
	cached := filepath.Join(dir, "cached.webp")
	if err := os.WriteFile(cached, []byte("webp"), 0o644); err != nil {
		t.Fatalf("WriteFile(cached) error = %v", err)
	}
	if err := db.UpsertRecentSticker(ctx, store.RecentSticker{
		ID:         "sticker-1",
		MIMEType:   "image/webp",
		FileName:   "sticker.webp",
		LocalPath:  cached,
		LastUsedAt: time.Now(),
	}); err != nil {
		t.Fatalf("UpsertRecentSticker(cached) error = %v", err)
	}
	if err := db.UpsertRecentSticker(ctx, store.RecentSticker{
		ID:         "sticker-lottie",
		MIMEType:   "application/x-tgsticker",
		FileName:   "sticker.tgs",
		LocalPath:  cached,
		IsLottie:   true,
		LastUsedAt: time.Now().Add(time.Second),
	}); err != nil {
		t.Fatalf("UpsertRecentSticker(lottie) error = %v", err)
	}

	candidates, err := stickerPickerCandidates(ctx, db, 10)
	if err != nil {
		t.Fatalf("stickerPickerCandidates() error = %v", err)
	}
	if len(candidates) != 1 || candidates[0].ID != "sticker-1" {
		t.Fatalf("candidates = %+v, want only renderable cached sticker", candidates)
	}
	pickerDir, files, byPath, err := prepareStickerPickerFiles(config.Paths{TransientDir: filepath.Join(t.TempDir(), "transient")}, candidates)
	if err != nil {
		t.Fatalf("prepareStickerPickerFiles() error = %v", err)
	}
	defer os.RemoveAll(pickerDir)
	if len(files) != 1 || !mediaPathAvailable(files[0]) {
		t.Fatalf("picker files = %+v, want one materialized file", files)
	}
	selected, ok := selectedStickerForPath(files[0], byPath)
	if !ok || selected.ID != "sticker-1" {
		t.Fatalf("selected sticker = %+v ok=%v", selected, ok)
	}
}

func TestPickStickerUsesExistingCacheWhenPreloadFails(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "state.sqlite3"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	dir := t.TempDir()
	cached := filepath.Join(dir, "cached.webp")
	if err := os.WriteFile(cached, []byte("webp"), 0o644); err != nil {
		t.Fatalf("WriteFile(cached) error = %v", err)
	}
	if err := db.UpsertRecentSticker(ctx, store.RecentSticker{
		ID:         "sticker-1",
		MIMEType:   "image/webp",
		FileName:   "sticker.webp",
		LocalPath:  cached,
		LastUsedAt: time.Now(),
	}); err != nil {
		t.Fatalf("UpsertRecentSticker(cached) error = %v", err)
	}

	msg := pickSticker(
		config.Paths{TransientDir: filepath.Join(t.TempDir(), "transient")},
		config.Config{StickerPickerCommand: "true"},
		db,
		func(context.Context) error { return errors.New("sync failed") },
	)()
	if picked, ok := msg.(ui.StickerPickedMsg); ok && picked.Err != nil {
		t.Fatalf("pickSticker() returned immediate error %v, want picker command from existing cache", picked.Err)
	}
}

func TestExpandStickerPickerArgsSplicesFiles(t *testing.T) {
	got := expandStickerPickerArgs(
		[]string{"nsxiv", "-t", "-o", "-p", "{files}"},
		"/tmp/chooser",
		"/tmp/picker",
		[]string{"/tmp/picker/a.webp", "/tmp/picker/b.webp"},
	)
	want := "nsxiv\x00-t\x00-o\x00-p\x00/tmp/picker/a.webp\x00/tmp/picker/b.webp"
	if strings.Join(got, "\x00") != want {
		t.Fatalf("expanded args = %#v", got)
	}
}

func writeTinyPNG(t *testing.T, dir, name string) string {
	t.Helper()
	data, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+/p9sAAAAASUVORK5CYII=")
	if err != nil {
		t.Fatalf("DecodeString() error = %v", err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("WriteFile(%s) error = %v", path, err)
	}
	return path
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", path, err)
	}
	return data
}

func TestAudioPlayerCommandAppendsPathWhenPlaceholderMissing(t *testing.T) {
	cmd, _, err := audioPlayerCommand(config.Config{
		AudioPlayerCommand: "sh -c true",
	}, store.MediaMetadata{
		LocalPath: "/tmp/voice.ogg",
		MIMEType:  "audio/ogg",
		FileName:  "voice.ogg",
	})
	if err != nil {
		t.Fatalf("audioPlayerCommand() error = %v", err)
	}
	if got := strings.Join(cmd.Args, "\x00"); got != "sh\x00-c\x00true\x00/tmp/voice.ogg" {
		t.Fatalf("cmd.Args = %#v", cmd.Args)
	}
}

func TestAudioPlayerCommandReportsMissingExecutable(t *testing.T) {
	_, _, err := audioPlayerCommand(config.Config{
		AudioPlayerCommand: "vimwhat-missing-audio-player {path}",
	}, store.MediaMetadata{
		LocalPath: "/tmp/voice.ogg",
		MIMEType:  "audio/ogg",
		FileName:  "voice.ogg",
	})
	if err == nil || !strings.Contains(err.Error(), "audio player") {
		t.Fatalf("audioPlayerCommand() error = %v, want audio player error", err)
	}
}
