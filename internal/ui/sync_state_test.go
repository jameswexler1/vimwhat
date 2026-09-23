package ui

import (
	tea "github.com/charmbracelet/bubbletea"
	"testing"
)

func TestBackgroundSyncKeepsLocalComposerUsable(t *testing.T) {
	m := composerTestModel()
	m.backgroundSync = true
	m.requireOnlineForSend = true
	m.connectionState = ConnectionOnline
	m, _ = m.handleSyncProgress(SyncProgressUpdate{Active: true, Title: "Syncing"})
	if m.syncBlocksUI() || m.whatsAppReady() {
		t.Fatal("background sync must permit local UI but gate protocol sends")
	}
	m.mode = ModeInsert
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("local draft")})
	if next.(Model).composer != "local draft" {
		t.Fatal("sync blocked composer")
	}
}
