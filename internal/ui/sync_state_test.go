package ui

import (
	tea "github.com/charmbracelet/bubbletea"
	"strings"
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

func TestStartupProgressBlocksUntilExplicitCachedBrowsing(t *testing.T) {
	m := composerTestModel()
	m.width, m.height = 100, 24
	m.requireOnlineForSend = true
	m.connectionState = ConnectionOnline
	m.protocolReady = true
	m.config.Keymap.SyncBrowse = "x"
	m, _ = m.handleLiveUpdate(LiveUpdate{Startup: &StartupProgressUpdate{
		Active: true, Stage: "Resolving contact names", Detail: "Contacts are still loading",
	}})
	if !m.syncBlocksUI() || m.whatsAppReady() || m.connectionStatusText() != "WA:SYNCING" {
		t.Fatal("connection alone must not imply readiness")
	}
	if view := stripANSI(m.View()); !strings.Contains(view, "Resolving contact names") || !strings.Contains(view, "x: browse") {
		t.Fatalf("missing progress or configured hint: %s", view)
	}
	next, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("b")})
	if !next.(Model).syncBlocksUI() {
		t.Fatal("hard-coded browse key bypassed config")
	}
	next, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	m = next.(Model)
	if m.syncBlocksUI() || m.whatsAppReady() {
		t.Fatal("cached browsing must not enable sends")
	}
	m.status = "unrelated UI action"
	if !strings.Contains(stripANSI(m.renderStatus()), "Resolving contact names") {
		t.Fatal("UI action hid persistent progress")
	}
	m, _ = m.handleLiveUpdate(LiveUpdate{Startup: &StartupProgressUpdate{Active: true, Failed: true, Stage: "Sync incomplete"}})
	if m.connectionStatusText() != "WA:SYNC FAILED" || m.whatsAppReady() {
		t.Fatal("failed sync looked ready")
	}
}
