//go:build windows

package config

import "testing"

func TestWindowsEmojiAutoUsesCompatInClassicConsole(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "")
	t.Setenv("WT_SESSION", "")

	if got := ResolveEmojiModeForEnv(EmojiModeAuto, "xterm-256color", "", "", "en_US.UTF-8"); got != EmojiModeCompat {
		t.Fatalf("ResolveEmojiModeForEnv() = %q, want %q", got, EmojiModeCompat)
	}
}

func TestWindowsEmojiAutoUsesCompatInWindowsTerminal(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "")
	t.Setenv("WT_SESSION", "session")

	if got := ResolveEmojiModeForEnv(EmojiModeAuto, "xterm-256color", "", "", "en_US.UTF-8"); got != EmojiModeCompat {
		t.Fatalf("ResolveEmojiModeForEnv() = %q, want %q", got, EmojiModeCompat)
	}
}

func TestWindowsEmojiAutoAllowsFullInWezTerm(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "WezTerm")
	t.Setenv("WT_SESSION", "")

	if got := ResolveEmojiModeForEnv(EmojiModeAuto, "xterm-256color", "", "", "en_US.UTF-8"); got != EmojiModeFull {
		t.Fatalf("ResolveEmojiModeForEnv() = %q, want %q", got, EmojiModeFull)
	}
}
