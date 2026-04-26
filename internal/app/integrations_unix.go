//go:build !windows

package app

import (
	"os"
	"os/exec"

	"vimwhat/internal/media"
	"vimwhat/internal/store"
)

func platformClipboardCommands() [][]string {
	var commands [][]string
	if os.Getenv("WAYLAND_DISPLAY") != "" {
		commands = append(commands, []string{"wl-copy"})
	}
	if os.Getenv("DISPLAY") != "" {
		commands = append(commands, []string{"xclip", "-selection", "clipboard"})
		commands = append(commands, []string{"xsel", "--clipboard", "--input"})
	}
	return append(commands, []string{"pbcopy"}, []string{"termux-clipboard-set"})
}

func platformImagePasteCommands(mediaDir string) []imageClipboardCommand {
	var commands []imageClipboardCommand
	if os.Getenv("WAYLAND_DISPLAY") != "" {
		commands = append(commands, imageClipboardCommand{argv: []string{"wl-paste", "--type", "image/png"}})
	}
	if os.Getenv("DISPLAY") != "" {
		commands = append(commands, imageClipboardCommand{argv: []string{"xclip", "-selection", "clipboard", "-t", "image/png", "-o"}})
	}
	if _, err := exec.LookPath("pngpaste"); err == nil {
		target := clipboardImagePath(mediaDir, ".png")
		commands = append(commands, imageClipboardCommand{argv: []string{"pngpaste", target}, pathMode: true, path: target})
	}
	return commands
}

func platformImageCopyCommands(path, mimeType string) []imageClipboardCommand {
	var commands []imageClipboardCommand
	if os.Getenv("WAYLAND_DISPLAY") != "" {
		commands = append(commands, imageClipboardCommand{argv: []string{"wl-copy", "--type", mimeType}})
	}
	if os.Getenv("DISPLAY") != "" {
		commands = append(commands, imageClipboardCommand{argv: []string{"xclip", "-selection", "clipboard", "-t", mimeType}})
	}
	return commands
}

func platformDefaultFilePickerCommand() string {
	return "yazi --chooser-file {chooser}"
}

func platformDefaultAudioPlayerCommand() string {
	return "mpv --no-video --no-terminal --really-quiet {path}"
}

func platformAutoOpenCommands(item store.MediaMetadata, path string) [][]string {
	switch media.MediaKind(item.MIMEType, item.FileName) {
	case media.KindImage:
		return [][]string{{"nsxiv", path}, {"mpv", path}, {"xdg-open", path}}
	case media.KindVideo:
		return [][]string{{"mpv", path}, {"xdg-open", path}, {"nsxiv", path}}
	case media.KindAudio:
		return [][]string{{"mpv", path}, {"xdg-open", path}}
	default:
		return [][]string{{"xdg-open", path}, {"nsxiv", path}, {"mpv", path}}
	}
}
