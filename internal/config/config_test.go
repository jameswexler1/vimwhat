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

func stringsJoin(lines ...string) string {
	return strings.Join(lines, "\n")
}
