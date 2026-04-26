//go:build !windows

package config

import (
	"os"
	"strings"
)

func platformDefaultEditor() string {
	editor := strings.TrimSpace(os.Getenv("EDITOR"))
	if editor == "" {
		editor = "vi"
	}
	return editor
}

func platformDefaultFilePickerCommand() string {
	return "yazi --chooser-file {chooser}"
}

func platformDefaultImageViewerCommand() string {
	return "nsxiv {path}"
}

func platformDefaultVideoPlayerCommand() string {
	return "mpv {path}"
}

func platformDefaultAudioPlayerCommand() string {
	return "mpv --no-video --no-terminal --really-quiet {path}"
}

func platformDefaultFileOpenerCommand() string {
	return "xdg-open {path}"
}
