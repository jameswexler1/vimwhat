//go:build windows

package app

import (
	"strings"
	"testing"

	"vimwhat/internal/store"
)

func TestPlatformAutoOpenCommandsUseNativeWindowsOpeners(t *testing.T) {
	commands := platformAutoOpenCommands(store.MediaMetadata{}, `C:\Users\Alice\Downloads\photo one.jpg`)
	if len(commands) == 0 {
		t.Fatal("platformAutoOpenCommands() returned no candidates")
	}
	if got := strings.Join(commands[0], "\x00"); got != `rundll32.exe`+"\x00"+`url.dll,FileProtocolHandler`+"\x00"+`C:\Users\Alice\Downloads\photo one.jpg` {
		t.Fatalf("first command = %#v", commands[0])
	}
}

func TestWindowsClipboardImagePasteCommandReadsImageAndFileDropFormats(t *testing.T) {
	paste := platformImagePasteCommands(`C:\Temp\vimwhat-media`)
	if len(paste) == 0 || paste[0].pathMode || paste[0].path != "" {
		t.Fatalf("paste commands = %+v, want stdout command", paste)
	}
	script := strings.Join(paste[0].argv, " ")
	for _, want := range []string{"GetImage", "GetDataObject", "GetFileDropList", "OpenStandardOutput"} {
		if !strings.Contains(script, want) {
			t.Fatalf("paste command missing %q: %s", want, script)
		}
	}

	copyCommands := platformImageCopyCommands(`C:\Temp\photo.png`, "image/png")
	if len(copyCommands) == 0 || !copyCommands[0].pathMode {
		t.Fatalf("copy commands = %+v, want path-mode command", copyCommands)
	}
}
