package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadDefaultsWhenConfigMissing(t *testing.T) {
	t.Setenv("EDITOR", "nvim")
	t.Setenv("HOME", t.TempDir())

	paths := Paths{
		ConfigFile: filepath.Join(t.TempDir(), "config.toml"),
	}

	cfg, err := Load(paths)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.Editor != "nvim" {
		t.Fatalf("Editor = %q, want %q", cfg.Editor, "nvim")
	}

	if cfg.PreviewBackend != "auto" {
		t.Fatalf("PreviewBackend = %q, want %q", cfg.PreviewBackend, "auto")
	}
	if cfg.ImageViewerCommand != "nsxiv {path}" || cfg.VideoPlayerCommand != "mpv {path}" || cfg.FileOpenerCommand != "xdg-open {path}" {
		t.Fatalf("media commands = image %q video %q file %q", cfg.ImageViewerCommand, cfg.VideoPlayerCommand, cfg.FileOpenerCommand)
	}
	if cfg.LeaderKey != "space" {
		t.Fatalf("LeaderKey = %q, want space", cfg.LeaderKey)
	}
	if cfg.PreviewMaxWidth != 67 || cfg.PreviewMaxHeight != 40 {
		t.Fatalf("preview defaults = %dx%d, want 67x40", cfg.PreviewMaxWidth, cfg.PreviewMaxHeight)
	}
}

func TestLoadParsesSupportedKeys(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	content := stringsJoin(
		`editor = "nvim"`,
		`preview_backend = "chafa"`,
		`notification_command = "notify-send maybewhats"`,
		`clipboard_command = "wl-copy"`,
		`file_picker_command = "yazi --chooser-file {chooser}"`,
		`image_viewer_command = "imv {path}"`,
		`video_player_command = "mpv --force-window {path}"`,
		`file_opener_command = ""`,
		`leader_key = ","`,
		`preview_max_width = 44`,
		`preview_max_height = 10`,
		`preview_delay_ms = 0`,
		`downloads_dir = "~/Inbox"`,
	)

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	cfg, err := Load(Paths{ConfigFile: path})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.Editor != "nvim" {
		t.Fatalf("Editor = %q, want %q", cfg.Editor, "nvim")
	}
	if cfg.PreviewBackend != "chafa" {
		t.Fatalf("PreviewBackend = %q, want %q", cfg.PreviewBackend, "chafa")
	}
	if cfg.NotificationCommand != "notify-send maybewhats" {
		t.Fatalf("NotificationCommand = %q", cfg.NotificationCommand)
	}
	if cfg.ClipboardCommand != "wl-copy" {
		t.Fatalf("ClipboardCommand = %q", cfg.ClipboardCommand)
	}
	if cfg.FilePickerCommand != "yazi --chooser-file {chooser}" {
		t.Fatalf("FilePickerCommand = %q", cfg.FilePickerCommand)
	}
	if cfg.ImageViewerCommand != "imv {path}" {
		t.Fatalf("ImageViewerCommand = %q", cfg.ImageViewerCommand)
	}
	if cfg.VideoPlayerCommand != "mpv --force-window {path}" {
		t.Fatalf("VideoPlayerCommand = %q", cfg.VideoPlayerCommand)
	}
	if cfg.FileOpenerCommand != "" {
		t.Fatalf("FileOpenerCommand = %q", cfg.FileOpenerCommand)
	}
	if cfg.LeaderKey != "," {
		t.Fatalf("LeaderKey = %q", cfg.LeaderKey)
	}
	if cfg.PreviewMaxWidth != 44 || cfg.PreviewMaxHeight != 10 || cfg.PreviewDelayMS != 0 {
		t.Fatalf("preview sizing = %dx%d delay=%d", cfg.PreviewMaxWidth, cfg.PreviewMaxHeight, cfg.PreviewDelayMS)
	}

	wantDownloads := filepath.Join(os.Getenv("HOME"), "Inbox")
	if cfg.DownloadsDir != wantDownloads {
		t.Fatalf("DownloadsDir = %q, want %q", cfg.DownloadsDir, wantDownloads)
	}
}

func TestLoadRejectsUnknownKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	if err := os.WriteFile(path, []byte(`unknown = "value"`), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	_, err := Load(Paths{ConfigFile: path})
	if err == nil {
		t.Fatal("Load() error = nil, want error")
	}
}

func TestLoadRejectsInvalidLeaderKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	if err := os.WriteFile(path, []byte(`leader_key = "spacebar"`), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	_, err := Load(Paths{ConfigFile: path})
	if err == nil {
		t.Fatal("Load() error = nil, want invalid leader error")
	}
}

func stringsJoin(lines ...string) string {
	return strings.Join(lines, "\n")
}
