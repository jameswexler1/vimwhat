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
	if cfg.EmojiMode != EmojiModeAuto {
		t.Fatalf("EmojiMode = %q, want %q", cfg.EmojiMode, EmojiModeAuto)
	}
	if cfg.IndicatorNormal != IndicatorPywal || cfg.IndicatorInsert != IndicatorPywal || cfg.IndicatorVisual != IndicatorPywal || cfg.IndicatorCommand != IndicatorPywal || cfg.IndicatorSearch != IndicatorPywal {
		t.Fatalf("indicator defaults = normal %q insert %q visual %q command %q search %q, want all %q", cfg.IndicatorNormal, cfg.IndicatorInsert, cfg.IndicatorVisual, cfg.IndicatorCommand, cfg.IndicatorSearch, IndicatorPywal)
	}
	if cfg.ImageViewerCommand != "nsxiv {path}" || cfg.VideoPlayerCommand != "mpv {path}" || cfg.AudioPlayerCommand != "mpv --no-video --no-terminal --really-quiet {path}" || cfg.FileOpenerCommand != "xdg-open {path}" {
		t.Fatalf("media commands = image %q video %q audio %q file %q", cfg.ImageViewerCommand, cfg.VideoPlayerCommand, cfg.AudioPlayerCommand, cfg.FileOpenerCommand)
	}
	if cfg.LeaderKey != "space" {
		t.Fatalf("LeaderKey = %q, want space", cfg.LeaderKey)
	}
	if cfg.PreviewMaxWidth != 67 || cfg.PreviewMaxHeight != 18 {
		t.Fatalf("preview defaults = %dx%d, want 67x18", cfg.PreviewMaxWidth, cfg.PreviewMaxHeight)
	}
}

func TestLoadParsesSupportedKeys(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	content := stringsJoin(
		`editor = "nvim"`,
		`preview_backend = "chafa"`,
		`emoji_mode = "full"`,
		`indicator_normal = "#112233"`,
		`indicator_insert = "pywal"`,
		`indicator_visual = "#abc"`,
		`indicator_command = "#AABBCC"`,
		`indicator_search = "PYWAL"`,
		`notification_command = "notify-send vimwhat"`,
		`clipboard_command = "wl-copy"`,
		`file_picker_command = "yazi --chooser-file {chooser}"`,
		`image_viewer_command = "imv {path}"`,
		`video_player_command = "mpv --force-window {path}"`,
		`audio_player_command = "mpv --no-video {path}"`,
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
	if cfg.EmojiMode != EmojiModeFull {
		t.Fatalf("EmojiMode = %q, want %q", cfg.EmojiMode, EmojiModeFull)
	}
	if cfg.IndicatorNormal != "#112233" || cfg.IndicatorInsert != IndicatorPywal || cfg.IndicatorVisual != "#abc" || cfg.IndicatorCommand != "#AABBCC" || cfg.IndicatorSearch != IndicatorPywal {
		t.Fatalf("indicators = normal %q insert %q visual %q command %q search %q", cfg.IndicatorNormal, cfg.IndicatorInsert, cfg.IndicatorVisual, cfg.IndicatorCommand, cfg.IndicatorSearch)
	}
	if cfg.NotificationCommand != "notify-send vimwhat" {
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
	if cfg.AudioPlayerCommand != "mpv --no-video {path}" {
		t.Fatalf("AudioPlayerCommand = %q", cfg.AudioPlayerCommand)
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

func TestLoadRejectsInvalidEmojiMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	if err := os.WriteFile(path, []byte(`emoji_mode = "broken"`), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	_, err := Load(Paths{ConfigFile: path})
	if err == nil {
		t.Fatal("Load() error = nil, want invalid emoji mode error")
	}
}

func TestLoadRejectsInvalidModeIndicator(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	if err := os.WriteFile(path, []byte(`indicator_insert = "magenta"`), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	_, err := Load(Paths{ConfigFile: path})
	if err == nil {
		t.Fatal("Load() error = nil, want invalid indicator error")
	}
	if !strings.Contains(err.Error(), "indicator_insert") {
		t.Fatalf("Load() error = %v, want indicator_insert context", err)
	}
}

func TestResolveEmojiModeAutoUsesFullForUTF8Terminals(t *testing.T) {
	if got := ResolveEmojiModeForEnv(EmojiModeAuto, "xterm-kitty", "", "", "en_US.UTF-8"); got != EmojiModeFull {
		t.Fatalf("ResolveEmojiModeForEnv() = %q, want %q", got, EmojiModeFull)
	}
}

func TestResolveEmojiModeAutoFallsBackForSTTerminals(t *testing.T) {
	tests := []string{"st", "st-256color"}

	for _, term := range tests {
		t.Run(term, func(t *testing.T) {
			if got := ResolveEmojiModeForEnv(EmojiModeAuto, term, "", "", "en_US.UTF-8"); got != EmojiModeCompat {
				t.Fatalf("ResolveEmojiModeForEnv() = %q, want %q", got, EmojiModeCompat)
			}
		})
	}
}

func TestResolveEmojiModeFullOverridesSTFallback(t *testing.T) {
	if got := ResolveEmojiModeForEnv(EmojiModeFull, "st-256color", "", "", "en_US.UTF-8"); got != EmojiModeFull {
		t.Fatalf("ResolveEmojiModeForEnv() = %q, want %q", got, EmojiModeFull)
	}
}

func TestResolveEmojiModeAutoFallsBackForClearlyUnsupportedTerminals(t *testing.T) {
	tests := []struct {
		name  string
		term  string
		lcAll string
		ctype string
		lang  string
	}{
		{name: "dumb term", term: "dumb", lang: "en_US.UTF-8"},
		{name: "c locale", term: "xterm-kitty", lang: "C"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := ResolveEmojiModeForEnv(EmojiModeAuto, test.term, test.lcAll, test.ctype, test.lang); got != EmojiModeCompat {
				t.Fatalf("ResolveEmojiModeForEnv() = %q, want %q", got, EmojiModeCompat)
			}
		})
	}
}

func stringsJoin(lines ...string) string {
	return strings.Join(lines, "\n")
}
