package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func EnsureDefaultFile(paths Paths) error {
	if strings.TrimSpace(paths.ConfigFile) == "" {
		return nil
	}
	if dir := configDirForFile(paths.ConfigFile); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create config dir: %w", err)
		}
	}
	file, err := os.OpenFile(paths.ConfigFile, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if os.IsExist(err) {
			return nil
		}
		return fmt.Errorf("create default config: %w", err)
	}
	defer file.Close()

	if _, err := file.WriteString(DefaultFileContent(paths)); err != nil {
		return fmt.Errorf("write default config: %w", err)
	}
	return nil
}

func DefaultFileContent(paths Paths) string {
	cfg := Default(paths)
	downloadsDir := "~/Downloads"
	if strings.TrimSpace(cfg.DownloadsDir) == "" {
		downloadsDir = ""
	}

	var b strings.Builder
	b.WriteString("# vimwhat configuration\n")
	b.WriteString("# This file is created on first run and is safe to edit.\n")
	b.WriteString("# First-run config is generated automatically at:\n")
	b.WriteString("# - Linux: $XDG_CONFIG_HOME/vimwhat/config.toml\n")
	b.WriteString("# - Windows: %APPDATA%\\vimwhat\\config.toml\n")
	b.WriteString("# Leave command fields empty to use built-in platform auto defaults.\n")
	b.WriteString("# Key tokens: printable single keys, space, enter, esc, tab, shift+tab, backspace, ctrl+x, alt+x, alt+enter, and leader sequences.\n\n")

	fmt.Fprintf(&b, "editor = %q\n", cfg.Editor)
	fmt.Fprintf(&b, "preview_backend = %q\n", cfg.PreviewBackend)
	fmt.Fprintf(&b, "emoji_mode = %q\n", cfg.EmojiMode)
	fmt.Fprintf(&b, "indicator_normal = %q\n", cfg.IndicatorNormal)
	fmt.Fprintf(&b, "indicator_insert = %q\n", cfg.IndicatorInsert)
	fmt.Fprintf(&b, "indicator_visual = %q\n", cfg.IndicatorVisual)
	fmt.Fprintf(&b, "indicator_command = %q\n", cfg.IndicatorCommand)
	fmt.Fprintf(&b, "indicator_search = %q\n", cfg.IndicatorSearch)
	fmt.Fprintf(&b, "notification_backend = %q\n", cfg.NotificationBackend)
	fmt.Fprintf(&b, "notification_command = %q\n", cfg.NotificationCommand)
	fmt.Fprintf(&b, "clipboard_command = %q\n", cfg.ClipboardCommand)
	fmt.Fprintf(&b, "clipboard_image_paste_command = %q\n", cfg.ClipboardImagePasteCommand)
	fmt.Fprintf(&b, "clipboard_image_copy_command = %q\n", cfg.ClipboardImageCopyCommand)
	fmt.Fprintf(&b, "file_picker_command = %q\n", cfg.FilePickerCommand)
	fmt.Fprintf(&b, "image_viewer_command = %q\n", cfg.ImageViewerCommand)
	fmt.Fprintf(&b, "video_player_command = %q\n", cfg.VideoPlayerCommand)
	fmt.Fprintf(&b, "audio_player_command = %q\n", cfg.AudioPlayerCommand)
	fmt.Fprintf(&b, "file_opener_command = %q\n", cfg.FileOpenerCommand)
	fmt.Fprintf(&b, "preview_max_width = %d\n", cfg.PreviewMaxWidth)
	fmt.Fprintf(&b, "preview_max_height = %d\n", cfg.PreviewMaxHeight)
	fmt.Fprintf(&b, "preview_delay_ms = %d\n", cfg.PreviewDelayMS)
	fmt.Fprintf(&b, "downloads_dir = %q\n\n", downloadsDir)

	fmt.Fprintf(&b, "leader_key = %q\n\n", cfg.LeaderKey)

	writeKeyGroup(&b, "Global", []KeyBinding{
		{Name: "key_global_quit", Value: cfg.Keymap.GlobalQuit},
	})
	writeKeyGroup(&b, "Help overlay", []KeyBinding{
		{Name: "key_help_close", Value: cfg.Keymap.HelpClose},
		{Name: "key_help_close_alt", Value: cfg.Keymap.HelpCloseAlt},
	})
	bindings := KeymapBindings(cfg.Keymap)
	writeKeyGroup(&b, "Normal mode", keyBindingsForMode(bindings, KeyModeNormal))
	writeKeyGroup(&b, "Insert mode", keyBindingsForMode(bindings, KeyModeInsert))
	writeKeyGroup(&b, "Visual mode", keyBindingsForMode(bindings, KeyModeVisual))
	writeKeyGroup(&b, "Command mode", keyBindingsForMode(bindings, KeyModeCommand))
	writeKeyGroup(&b, "Search mode", keyBindingsForMode(bindings, KeyModeSearch))
	writeKeyGroup(&b, "Confirm mode", keyBindingsForMode(bindings, KeyModeConfirm))

	return b.String()
}

func writeKeyGroup(b *strings.Builder, title string, bindings []KeyBinding) {
	fmt.Fprintf(b, "# %s\n", title)
	for _, binding := range bindings {
		fmt.Fprintf(b, "%s = %q\n", binding.Name, binding.Value)
	}
	b.WriteString("\n")
}

func keyBindingsForMode(bindings []KeyBinding, mode string) []KeyBinding {
	var out []KeyBinding
	for _, binding := range bindings {
		if binding.Mode == mode {
			out = append(out, binding)
		}
	}
	return out
}

func configDirForFile(path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	return filepath.Dir(path)
}
