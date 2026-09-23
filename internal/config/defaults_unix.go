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

func platformDefaultStickerPickerCommand() string {
	return "nsxiv -t -o -p {files}"
}

func platformDefaultImageViewerCommand() string {
	return "auto"
}

func platformDefaultVideoPlayerCommand() string {
	return "auto"
}

func platformDefaultAudioPlayerCommand() string {
	return "mpv --no-video --no-terminal --really-quiet {path}"
}

func platformDefaultFileOpenerCommand() string {
	return "auto"
}
