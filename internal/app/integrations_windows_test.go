//go:build windows

package app

import (
	"encoding/base64"
	"os"
	"path/filepath"
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
	for _, want := range []string{"GetImage", "GetDataObject", "GetFileDropList", "OpenStandardOutput", clipboardFileDropPrefix} {
		if !strings.Contains(script, want) {
			t.Fatalf("paste command missing %q: %s", want, script)
		}
	}

	copyCommands := platformImageCopyCommands(`C:\Temp\photo.png`, "image/png")
	if len(copyCommands) == 0 || !copyCommands[0].pathMode {
		t.Fatalf("copy commands = %+v, want path-mode command", copyCommands)
	}
}

func TestAttachmentFromClipboardFileDropStagesOriginalPaths(t *testing.T) {
	imagePath := filepath.Join(t.TempDir(), "photo.png")
	if err := os.WriteFile(imagePath, []byte("png"), 0o644); err != nil {
		t.Fatalf("WriteFile(image) error = %v", err)
	}
	filePath := filepath.Join(t.TempDir(), "report.pdf")
	if err := os.WriteFile(filePath, []byte("pdf"), 0o644); err != nil {
		t.Fatalf("WriteFile(file) error = %v", err)
	}

	for _, path := range []string{imagePath, filePath} {
		attachment, ok, err := attachmentFromClipboardFileDrop([]byte(clipboardFileDropPrefix + base64.StdEncoding.EncodeToString([]byte(path))))
		if err != nil || !ok {
			t.Fatalf("attachmentFromClipboardFileDrop(%q) ok=%v err=%v", path, ok, err)
		}
		if attachment.LocalPath != path {
			t.Fatalf("LocalPath = %q, want %q", attachment.LocalPath, path)
		}
	}
}
