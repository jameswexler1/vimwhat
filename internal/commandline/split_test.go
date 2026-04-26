package commandline

import (
	"runtime"
	"strings"
	"testing"
)

func TestSplitPreservesQuotedArgumentWithSpaces(t *testing.T) {
	args, err := Split(`tool --name "hello world"`)
	if err != nil {
		t.Fatalf("Split() error = %v", err)
	}
	if got := strings.Join(args, "\x00"); got != "tool\x00--name\x00hello world" {
		t.Fatalf("args = %#v", args)
	}
}

func TestSplitHandlesWindowsBackslashPaths(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows command-line escaping is only built on Windows")
	}
	args, err := Split(`rundll32.exe url.dll,FileProtocolHandler C:\Users\Alice\Downloads\photo.jpg`)
	if err != nil {
		t.Fatalf("Split() error = %v", err)
	}
	if got := strings.Join(args, "\x00"); got != `rundll32.exe`+"\x00"+`url.dll,FileProtocolHandler`+"\x00"+`C:\Users\Alice\Downloads\photo.jpg` {
		t.Fatalf("args = %#v", args)
	}
}
