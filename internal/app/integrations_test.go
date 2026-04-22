package app

import (
	"strings"
	"testing"

	"maybewhats/internal/config"
	"maybewhats/internal/store"
)

func TestAudioPlayerCommandUsesConfiguredTemplate(t *testing.T) {
	cmd, path, err := audioPlayerCommand(config.Config{
		AudioPlayerCommand: "sh -c true {path}",
	}, store.MediaMetadata{
		LocalPath: "/tmp/voice.ogg",
		MIMEType:  "audio/ogg",
		FileName:  "voice.ogg",
	})
	if err != nil {
		t.Fatalf("audioPlayerCommand() error = %v", err)
	}
	if path != "/tmp/voice.ogg" {
		t.Fatalf("path = %q, want /tmp/voice.ogg", path)
	}
	if got := strings.Join(cmd.Args, "\x00"); got != "sh\x00-c\x00true\x00/tmp/voice.ogg" {
		t.Fatalf("cmd.Args = %#v", cmd.Args)
	}
}

func TestAudioPlayerCommandAppendsPathWhenPlaceholderMissing(t *testing.T) {
	cmd, _, err := audioPlayerCommand(config.Config{
		AudioPlayerCommand: "sh -c true",
	}, store.MediaMetadata{
		LocalPath: "/tmp/voice.ogg",
		MIMEType:  "audio/ogg",
		FileName:  "voice.ogg",
	})
	if err != nil {
		t.Fatalf("audioPlayerCommand() error = %v", err)
	}
	if got := strings.Join(cmd.Args, "\x00"); got != "sh\x00-c\x00true\x00/tmp/voice.ogg" {
		t.Fatalf("cmd.Args = %#v", cmd.Args)
	}
}

func TestAudioPlayerCommandReportsMissingExecutable(t *testing.T) {
	_, _, err := audioPlayerCommand(config.Config{
		AudioPlayerCommand: "maybewhats-missing-audio-player {path}",
	}, store.MediaMetadata{
		LocalPath: "/tmp/voice.ogg",
		MIMEType:  "audio/ogg",
		FileName:  "voice.ogg",
	})
	if err == nil || !strings.Contains(err.Error(), "audio player") {
		t.Fatalf("audioPlayerCommand() error = %v, want audio player error", err)
	}
}
