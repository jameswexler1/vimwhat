package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"maybewhats/internal/config"
	"maybewhats/internal/media"
	"maybewhats/internal/store"
)

func TestInsertBackspacePreservesUTF8(t *testing.T) {
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats:          []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{"chat-1": nil},
			DraftsByChat:   map[string]string{},
			ActiveChatID:   "chat-1",
		},
	})
	model.mode = ModeInsert
	model.composer = "olá"

	updated, _ := model.updateInsert(tea.KeyMsg{Type: tea.KeyBackspace})
	got := updated.(Model).composer
	if got != "ol" {
		t.Fatalf("composer = %q, want %q", got, "ol")
	}
}

func TestInsertEscPersistsDraft(t *testing.T) {
	var savedChatID string
	var savedBody string
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats:          []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{"chat-1": nil},
			DraftsByChat:   map[string]string{},
			ActiveChatID:   "chat-1",
		},
		SaveDraft: func(chatID, body string) error {
			savedChatID = chatID
			savedBody = body
			return nil
		},
	})
	model.mode = ModeInsert
	model.composer = "draft text"

	updated, _ := model.updateInsert(tea.KeyMsg{Type: tea.KeyEsc})
	got := updated.(Model)
	if got.mode != ModeNormal {
		t.Fatalf("mode = %s, want %s", got.mode, ModeNormal)
	}
	if savedChatID != "chat-1" || savedBody != "draft text" {
		t.Fatalf("saved draft = (%q, %q), want (chat-1, draft text)", savedChatID, savedBody)
	}
	if !got.chats[0].HasDraft {
		t.Fatal("chat HasDraft was not updated")
	}
}

func TestInsertSendClearsDraft(t *testing.T) {
	var cleared bool
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats:          []store.Chat{{ID: "chat-1", Title: "Alice", HasDraft: true}},
			MessagesByChat: map[string][]store.Message{"chat-1": nil},
			DraftsByChat:   map[string]string{"chat-1": "old draft"},
			ActiveChatID:   "chat-1",
		},
		PersistMessage: func(chatID, body string, attachments []Attachment) (store.Message, error) {
			return store.Message{ID: "local-1", ChatID: chatID, Sender: "me", Body: body, IsOutgoing: true}, nil
		},
		SaveDraft: func(chatID, body string) error {
			cleared = chatID == "chat-1" && body == ""
			return nil
		},
	})
	model.mode = ModeInsert
	model.composer = "send this"

	updated, _ := model.updateInsert(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if !cleared {
		t.Fatal("draft was not cleared after send")
	}
	if got.chats[0].HasDraft {
		t.Fatal("chat HasDraft remained true after send")
	}
	if got.mode != ModeInsert {
		t.Fatalf("mode = %s, want %s", got.mode, ModeInsert)
	}
	if len(got.messagesByChat["chat-1"]) != 1 {
		t.Fatalf("message count = %d, want 1", len(got.messagesByChat["chat-1"]))
	}
}

func TestSearchHighlightsWithoutFilteringMessages(t *testing.T) {
	searchCalled := false
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats: []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{
				"chat-1": []store.Message{
					{ID: "m-1", ChatID: "chat-1", Sender: "Alice", Body: "old"},
					{ID: "m-2", ChatID: "chat-1", Sender: "Alice", Body: "needle result"},
					{ID: "m-3", ChatID: "chat-1", Sender: "Alice", Body: "later"},
				},
			},
			DraftsByChat: map[string]string{},
			ActiveChatID: "chat-1",
		},
		SearchMessages: func(chatID, query string, limit int) ([]store.Message, error) {
			searchCalled = true
			return nil, nil
		},
	})
	model.focus = FocusMessages
	model.mode = ModeSearch
	model.searchLine = "needle"

	updated, _ := model.updateSearch(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	messages := got.messagesByChat["chat-1"]
	if searchCalled {
		t.Fatal("/ search called SearchMessages; it should not filter through the store")
	}
	if len(messages) != 3 {
		t.Fatalf("message count = %d, want unfiltered 3", len(messages))
	}
	if got.messageCursor != 1 {
		t.Fatalf("messageCursor = %d, want first match at 1", got.messageCursor)
	}
	if got.activeSearch != "needle" || len(got.searchMatches) != 1 {
		t.Fatalf("search state = active %q matches %v, want needle and one match", got.activeSearch, got.searchMatches)
	}
}

func TestOpeningChatLoadsMessagesLazily(t *testing.T) {
	var loadedChatID string
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats: []store.Chat{
				{ID: "chat-1", Title: "Alice"},
				{ID: "chat-2", Title: "Project"},
			},
			MessagesByChat: map[string][]store.Message{
				"chat-1": []store.Message{{ID: "m-1", ChatID: "chat-1", Sender: "Alice", Body: "already loaded"}},
			},
			DraftsByChat: map[string]string{},
			ActiveChatID: "chat-1",
		},
		LoadMessages: func(chatID string, limit int) ([]store.Message, error) {
			loadedChatID = chatID
			return []store.Message{{ID: "m-2", ChatID: chatID, Sender: "Project", Body: "loaded on demand"}}, nil
		},
	})
	model.focus = FocusChats

	updated, _ := model.updateNormal(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	got := updated.(Model)
	if loadedChatID != "chat-2" {
		t.Fatalf("loadedChatID = %q, want chat-2", loadedChatID)
	}
	if got.messagesByChat["chat-2"][0].Body != "loaded on demand" {
		t.Fatalf("messagesByChat[chat-2] = %+v", got.messagesByChat["chat-2"])
	}
}

func TestNormalCountsMoveCursor(t *testing.T) {
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats: []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{
				"chat-1": []store.Message{
					{ID: "m-1", ChatID: "chat-1", Sender: "Alice", Body: "one"},
					{ID: "m-2", ChatID: "chat-1", Sender: "Alice", Body: "two"},
					{ID: "m-3", ChatID: "chat-1", Sender: "Alice", Body: "three"},
					{ID: "m-4", ChatID: "chat-1", Sender: "Alice", Body: "four"},
				},
			},
			DraftsByChat: map[string]string{},
			ActiveChatID: "chat-1",
		},
	})
	model.focus = FocusMessages

	counted, _ := model.updateNormal(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("3")})
	moved, _ := counted.(Model).updateNormal(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	got := moved.(Model)
	if got.messageCursor != 3 {
		t.Fatalf("messageCursor = %d, want 3", got.messageCursor)
	}
}

func TestUnreadFilterAndSortCommands(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats: []store.Chat{
				{ID: "read-pinned", Title: "Read Pinned", Pinned: true, LastMessageAt: now.Add(-time.Hour)},
				{ID: "unread-old", Title: "Unread Old", Unread: 1, LastMessageAt: now.Add(-2 * time.Hour)},
				{ID: "unread-new", Title: "Unread New", Unread: 2, LastMessageAt: now},
			},
			MessagesByChat: map[string][]store.Message{},
			DraftsByChat:   map[string]string{},
		},
	})

	filtered, _ := model.executeCommand("filter unread")
	got := filtered.(Model)
	if len(got.chats) != 2 {
		t.Fatalf("filtered chats = %+v, want two unread chats", got.chats)
	}

	sorted, _ := got.executeCommand("sort recent")
	got = sorted.(Model)
	if got.chats[0].ID != "unread-new" {
		t.Fatalf("first chat after recent sort = %s, want unread-new", got.chats[0].ID)
	}
}

func TestHelpOverlayRendersModeSpecificKeys(t *testing.T) {
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats:          []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{"chat-1": nil},
			DraftsByChat:   map[string]string{},
			ActiveChatID:   "chat-1",
		},
	})
	model.width = 100
	model.height = 24

	helped, _ := model.updateNormal(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")})
	got := helped.(Model)
	if !got.helpVisible {
		t.Fatal("helpVisible = false, want true")
	}
	view := got.View()
	for _, want := range []string{"maybewhats help", "normal:", "insert:", "command:"} {
		if !strings.Contains(view, want) {
			t.Fatalf("help view missing %q\n%s", want, view)
		}
	}
}

func TestInsertSupportsMultilineComposerPreview(t *testing.T) {
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats:          []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{"chat-1": nil},
			DraftsByChat:   map[string]string{},
			ActiveChatID:   "chat-1",
		},
	})
	model.mode = ModeInsert
	model.composer = "first"
	model.width = 80
	model.height = 12

	model.composer = "first\nsecond"
	input := stripANSI(model.renderMessages(80, 10))
	for _, want := range []string{"? help", "> first", "> second"} {
		if !strings.Contains(input, want) {
			t.Fatalf("renderMessages missing composer content %q:\n%s", want, input)
		}
	}
	if strings.Contains(input, "[INSERT]") || strings.Contains(input, "enter send") {
		t.Fatalf("composer retained noisy insert workflow text:\n%s", input)
	}
	if !strings.Contains(input, "▌") {
		t.Fatalf("composer missing cursor:\n%s", input)
	}
}

func TestClearSearchOnlyClearsSearchState(t *testing.T) {
	loadCalled := false
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats: []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{
				"chat-1": []store.Message{
					{ID: "m-1", ChatID: "chat-1", Sender: "Alice", Body: "normal"},
					{ID: "m-2", ChatID: "chat-1", Sender: "Alice", Body: "needle result"},
				},
			},
			DraftsByChat: map[string]string{},
			ActiveChatID: "chat-1",
		},
		LoadMessages: func(chatID string, limit int) ([]store.Message, error) {
			loadCalled = true
			return []store.Message{{ID: "m-1", ChatID: chatID, Sender: "Alice", Body: "normal"}}, nil
		},
	})
	model.focus = FocusMessages
	model.mode = ModeSearch
	model.searchLine = "needle"

	searched, _ := model.updateSearch(tea.KeyMsg{Type: tea.KeyEnter})
	searchModel := searched.(Model)
	if len(searchModel.messagesByChat["chat-1"]) != 2 {
		t.Fatalf("search filtered messages unexpectedly: %+v", searchModel.messagesByChat["chat-1"])
	}

	cleared, _ := searchModel.executeCommand("clear-search")
	got := cleared.(Model)
	if loadCalled {
		t.Fatal("clear-search reloaded messages; it should only clear search state")
	}
	if len(got.messagesByChat["chat-1"]) != 2 {
		t.Fatalf("cleared search changed messages: %+v", got.messagesByChat["chat-1"])
	}
	if got.lastSearch != "" || len(got.searchMatches) != 0 {
		t.Fatalf("search state was not cleared: last=%q matches=%v", got.lastSearch, got.searchMatches)
	}
}

func TestSearchNextAndPreviousNavigateMatchesWithoutFiltering(t *testing.T) {
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats: []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{
				"chat-1": []store.Message{
					{ID: "m-1", ChatID: "chat-1", Sender: "Alice", Body: "needle first"},
					{ID: "m-2", ChatID: "chat-1", Sender: "Alice", Body: "plain"},
					{ID: "m-3", ChatID: "chat-1", Sender: "Alice", Body: "needle second"},
				},
			},
			DraftsByChat: map[string]string{},
			ActiveChatID: "chat-1",
		},
	})
	model.focus = FocusMessages
	model.mode = ModeSearch
	model.searchLine = "needle"

	searched, _ := model.updateSearch(tea.KeyMsg{Type: tea.KeyEnter})
	got := searched.(Model)
	if got.messageCursor != 0 {
		t.Fatalf("first search cursor = %d, want 0", got.messageCursor)
	}

	next, _ := got.updateNormal(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	got = next.(Model)
	if got.messageCursor != 2 {
		t.Fatalf("n cursor = %d, want 2", got.messageCursor)
	}

	prev, _ := got.updateNormal(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("N")})
	got = prev.(Model)
	if got.messageCursor != 0 {
		t.Fatalf("N cursor = %d, want 0", got.messageCursor)
	}
	if len(got.messagesByChat["chat-1"]) != 3 {
		t.Fatalf("search navigation filtered messages: %+v", got.messagesByChat["chat-1"])
	}
}

func TestFilterMessagesCommandFiltersAndClearsCurrentChat(t *testing.T) {
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats: []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{
				"chat-1": []store.Message{
					{ID: "m-1", ChatID: "chat-1", Sender: "Alice", Body: "normal"},
					{ID: "m-2", ChatID: "chat-1", Sender: "Alice", Body: "needle result"},
					{ID: "m-3", ChatID: "chat-1", Sender: "Alice", Body: "another normal"},
				},
			},
			DraftsByChat: map[string]string{},
			ActiveChatID: "chat-1",
		},
	})

	filtered, _ := model.executeCommand("filter messages needle")
	got := filtered.(Model)
	if got.messageFilter != "needle" {
		t.Fatalf("messageFilter = %q, want needle", got.messageFilter)
	}
	if messages := got.messagesByChat["chat-1"]; len(messages) != 1 || messages[0].ID != "m-2" {
		t.Fatalf("filtered messages = %+v, want only m-2", messages)
	}

	cleared, _ := got.executeCommand("filter clear")
	got = cleared.(Model)
	if got.messageFilter != "" {
		t.Fatalf("messageFilter = %q, want cleared", got.messageFilter)
	}
	if messages := got.messagesByChat["chat-1"]; len(messages) != 3 {
		t.Fatalf("cleared filtered messages = %+v, want all three", messages)
	}
}

func TestDraftPreservedAcrossLazyChatSwitch(t *testing.T) {
	saved := make(map[string]string)
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats: []store.Chat{
				{ID: "chat-1", Title: "Alice"},
				{ID: "chat-2", Title: "Project"},
			},
			MessagesByChat: map[string][]store.Message{
				"chat-1": []store.Message{{ID: "m-1", ChatID: "chat-1", Sender: "Alice", Body: "one"}},
			},
			DraftsByChat: map[string]string{},
			ActiveChatID: "chat-1",
		},
		SaveDraft: func(chatID, body string) error {
			saved[chatID] = body
			return nil
		},
		LoadMessages: func(chatID string, limit int) ([]store.Message, error) {
			return []store.Message{{ID: "m-2", ChatID: chatID, Sender: "Project", Body: "two"}}, nil
		},
	})
	model.mode = ModeInsert
	model.composer = "draft for alice"

	escaped, _ := model.updateInsert(tea.KeyMsg{Type: tea.KeyEsc})
	normal := escaped.(Model)
	normal.focus = FocusChats
	switched, _ := normal.updateNormal(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	got := switched.(Model)

	if saved["chat-1"] != "draft for alice" {
		t.Fatalf("saved draft = %q, want draft for alice", saved["chat-1"])
	}
	if !got.allChats[0].HasDraft {
		t.Fatal("draft flag was not preserved on original chat")
	}
}

func TestViewRendersPaneContentWithinTerminalWidth(t *testing.T) {
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats: []store.Chat{
				{ID: "chat-1", Title: "Alice", Unread: 2, Pinned: true},
				{ID: "chat-2", Title: "Project Maybewhats"},
			},
			MessagesByChat: map[string][]store.Message{
				"chat-1": []store.Message{{ID: "m-1", ChatID: "chat-1", Sender: "Alice", Body: "hello from the local cache"}},
			},
			DraftsByChat: map[string]string{},
			ActiveChatID: "chat-1",
		},
	})
	model.width = 120
	model.height = 30

	view := model.View()
	for _, want := range []string{"Chats", "Alice", "hello from the local cache"} {
		if !strings.Contains(view, want) {
			t.Fatalf("View() did not contain %q\n%s", want, view)
		}
	}
	if strings.Contains(view, "Info") {
		t.Fatalf("View() rendered the optional info pane by default\n%s", view)
	}

	for i, line := range strings.Split(view, "\n") {
		if width := lipgloss.Width(line); width > model.width {
			t.Fatalf("line %d width = %d, want <= %d", i+1, width, model.width)
		}
	}
	if got := len(strings.Split(view, "\n")); got > model.height {
		t.Fatalf("View() produced %d lines, want <= %d", got, model.height)
	}
}

func TestPanelSizingDoesNotWrapExactWidthContent(t *testing.T) {
	model := NewModel(Options{})
	model.focus = FocusMessages

	const panelWidth = 40
	const panelHeight = 6
	style := model.panelStyle(FocusMessages)
	contentWidth := panelContentWidth(style, panelWidth)
	content := strings.Repeat("x", contentWidth)

	view := stripANSI(model.renderPanel(FocusMessages, panelWidth, panelHeight, content))
	lines := strings.Split(view, "\n")
	if got := len(lines); got != panelHeight {
		t.Fatalf("rendered panel height = %d, want %d\n%s", got, panelHeight, view)
	}
	for i, line := range lines {
		if width := lipgloss.Width(line); width > panelWidth {
			t.Fatalf("line %d width = %d, want <= %d\n%s", i+1, width, panelWidth, view)
		}
	}
	if !strings.Contains(view, content) {
		t.Fatalf("exact-width content wrapped or was clipped\n%s", view)
	}
}

func TestChatRowsShowPreviewAndIndicators(t *testing.T) {
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats: []store.Chat{
				{
					ID:          "chat-1",
					Title:       "Project",
					Kind:        "group",
					Unread:      3,
					Pinned:      true,
					Muted:       true,
					HasDraft:    true,
					LastPreview: "latest project update",
				},
			},
			MessagesByChat: map[string][]store.Message{"chat-1": nil},
			DraftsByChat:   map[string]string{"chat-1": "draft reply"},
			ActiveChatID:   "chat-1",
		},
	})

	view := stripANSI(model.renderChats(40, 12))
	for _, want := range []string{"Project", "[PMDG] 3", "draft: draft reply"} {
		if !strings.Contains(view, want) {
			t.Fatalf("chat list missing %q\n%s", want, view)
		}
	}
	if !strings.Contains(view, "┌") || !strings.Contains(view, "└") {
		t.Fatalf("chat list did not render bordered cells\n%s", view)
	}
}

func TestChatCellsScrollWithActiveChat(t *testing.T) {
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats:        numberedChats(8),
			DraftsByChat: map[string]string{},
			ActiveChatID: "chat-0",
		},
	})
	model.width = 120
	model.height = 14
	model.focus = FocusChats

	for i := 0; i < 7; i++ {
		updated, _ := model.updateNormal(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
		model = updated.(Model)
	}
	if model.activeChat != 7 {
		t.Fatalf("activeChat = %d, want 7", model.activeChat)
	}
	if model.chatScrollTop == 0 {
		t.Fatal("chatScrollTop did not advance while moving through chat cells")
	}
	view := stripANSI(model.renderChats(30, 11))
	if !strings.Contains(view, "Chat 07") {
		t.Fatalf("active chat cell is not visible after scrolling\n%s", view)
	}
	if strings.Contains(view, "Chat 00") {
		t.Fatalf("chat viewport did not move away from oldest cells\n%s", view)
	}
}

func TestChatTopAndBottomCommandsKeepActiveCellVisible(t *testing.T) {
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats:        numberedChats(8),
			DraftsByChat: map[string]string{},
			ActiveChatID: "chat-0",
		},
	})
	model.width = 120
	model.height = 14
	model.focus = FocusChats

	bottom, _ := model.updateNormal(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("G")})
	model = bottom.(Model)
	if model.activeChat != 7 || model.chatScrollTop == 0 {
		t.Fatalf("G activeChat=%d chatScrollTop=%d, want bottom visible", model.activeChat, model.chatScrollTop)
	}
	bottomView := stripANSI(model.renderChats(30, 11))
	if !strings.Contains(bottomView, "Chat 07") || strings.Contains(bottomView, "Chat 00") {
		t.Fatalf("G did not show bottom chat cell\n%s", bottomView)
	}

	top, _ := model.updateNormal(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("g")})
	model = top.(Model)
	if model.activeChat != 0 || model.chatScrollTop != 0 {
		t.Fatalf("g activeChat=%d chatScrollTop=%d, want top visible", model.activeChat, model.chatScrollTop)
	}
	topView := stripANSI(model.renderChats(30, 11))
	if !strings.Contains(topView, "Chat 00") || strings.Contains(topView, "Chat 07") {
		t.Fatalf("g did not show top chat cell\n%s", topView)
	}
}

func TestChatSearchKeepsMatchedCellVisible(t *testing.T) {
	chats := numberedChats(8)
	chats[6].Title = "Needle Team"
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats:        chats,
			DraftsByChat: map[string]string{},
			ActiveChatID: "chat-0",
		},
	})
	model.width = 120
	model.height = 14
	model.focus = FocusChats
	model.mode = ModeSearch
	model.searchLine = "needle"

	searched, _ := model.updateSearch(tea.KeyMsg{Type: tea.KeyEnter})
	got := searched.(Model)
	if got.activeChat != 6 {
		t.Fatalf("activeChat = %d, want matched chat 6", got.activeChat)
	}
	if got.chatScrollTop == 0 {
		t.Fatal("chatScrollTop did not advance to reveal search match")
	}
	view := stripANSI(got.renderChats(30, 11))
	if !strings.Contains(view, "Needle Team") {
		t.Fatalf("matched chat cell is not visible\n%s", view)
	}
}

func TestChatFilterClampsCellViewport(t *testing.T) {
	chats := numberedChats(8)
	chats[7].Unread = 2
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats:        chats,
			DraftsByChat: map[string]string{},
			ActiveChatID: "chat-7",
		},
	})
	model.width = 120
	model.height = 14
	model.focus = FocusChats
	model.activeChat = 7
	model.chatScrollTop = 6

	filtered, _ := model.executeCommand("filter unread")
	got := filtered.(Model)
	if len(got.chats) != 1 || got.activeChat != 0 || got.chatScrollTop != 0 {
		t.Fatalf("filtered chats=%d activeChat=%d chatScrollTop=%d, want one visible chat", len(got.chats), got.activeChat, got.chatScrollTop)
	}
	view := stripANSI(got.renderChats(30, 11))
	if !strings.Contains(view, "Chat 07") {
		t.Fatalf("filtered unread chat cell is not visible\n%s", view)
	}
}

func TestCompactViewShowsFocusedPaneOnly(t *testing.T) {
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats: []store.Chat{
				{ID: "chat-1", Title: "Alice"},
			},
			MessagesByChat: map[string][]store.Message{
				"chat-1": []store.Message{{ID: "m-1", ChatID: "chat-1", Sender: "Alice", Body: "compact message"}},
			},
			DraftsByChat: map[string]string{},
			ActiveChatID: "chat-1",
		},
	})
	model.width = 70
	model.height = 18
	model.compactLayout = true
	model.focus = FocusMessages

	view := model.View()
	if !strings.Contains(view, "compact message") {
		t.Fatalf("compact message pane missing body\n%s", view)
	}
	if strings.Contains(view, "Chats") {
		t.Fatalf("compact message focus rendered chat pane too\n%s", view)
	}
	for i, line := range strings.Split(view, "\n") {
		if width := lipgloss.Width(line); width > model.width {
			t.Fatalf("line %d width = %d, want <= %d", i+1, width, model.width)
		}
	}
}

func TestCompactChatFocusUsesFullTerminalWidth(t *testing.T) {
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats: []store.Chat{
				{ID: "chat-1", Title: "Alice", LastPreview: "wide compact chat cell"},
			},
			MessagesByChat: map[string][]store.Message{
				"chat-1": []store.Message{{ID: "m-1", ChatID: "chat-1", Sender: "Alice", Body: "message pane should be hidden"}},
			},
			DraftsByChat: map[string]string{},
			ActiveChatID: "chat-1",
		},
	})
	model.width = 70
	model.height = 18
	model.compactLayout = true
	model.focus = FocusChats

	view := stripANSI(model.View())
	lines := strings.Split(view, "\n")
	if width := lipgloss.Width(lines[0]); width != model.width {
		t.Fatalf("compact chat pane width = %d, want %d\n%s", width, model.width, view)
	}
	if !strings.Contains(view, "wide compact chat cell") {
		t.Fatalf("compact chat focus did not render chat cell content\n%s", view)
	}
	if strings.Contains(view, "message pane should be hidden") {
		t.Fatalf("compact chat focus rendered message pane too\n%s", view)
	}
}

func TestPreviewCommandTogglesInfoPane(t *testing.T) {
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats:          []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{"chat-1": nil},
			DraftsByChat:   map[string]string{},
			ActiveChatID:   "chat-1",
		},
	})
	model.width = 120
	model.height = 30

	updated, _ := model.executeCommand("preview")
	got := updated.(Model)
	if !got.infoPaneVisible {
		t.Fatal("infoPaneVisible = false, want true")
	}
	if !strings.Contains(got.View(), "Info") {
		t.Fatal("View() did not render the info pane after :preview")
	}
}

func TestMessagesRenderIncomingLeftAndOutgoingRight(t *testing.T) {
	now := time.Date(2026, 4, 21, 20, 59, 0, 0, time.UTC)
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats: []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{
				"chat-1": []store.Message{
					{ID: "m-1", ChatID: "chat-1", Sender: "Alice", Body: "incoming text", Timestamp: now.Add(-time.Minute)},
					{ID: "m-2", ChatID: "chat-1", Sender: "me", Body: "outgoing text that wraps across more than one line in the terminal viewport", Timestamp: now, IsOutgoing: true, Status: "sent"},
				},
			},
			DraftsByChat: map[string]string{},
			ActiveChatID: "chat-1",
		},
	})

	view := model.renderMessages(80, 24)
	incomingLine := plainLineContaining(view, "incoming text")
	outgoingLine := plainLineContaining(view, "outgoing text")
	if incomingLine == "" || outgoingLine == "" {
		t.Fatalf("rendered messages missing expected bodies\n%s", view)
	}
	if leadingSpaces(incomingLine) > 4 {
		t.Fatalf("incoming message was not left aligned: %q", incomingLine)
	}
	if leadingSpaces(outgoingLine) < 20 {
		t.Fatalf("outgoing message was not right aligned: %q", outgoingLine)
	}
	plain := stripANSI(view)
	for _, want := range []string{"╭", "╰", "20:58", "20:59 ✓"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("message bubbles missing %q\n%s", want, plain)
		}
	}
	for _, unwanted := range []string{"me 20:59", "sent", "pending"} {
		if strings.Contains(plain, unwanted) {
			t.Fatalf("message bubble retained old metadata %q\n%s", unwanted, plain)
		}
	}
}

func TestMessageBubblesShowGroupSenderOnlyForIncomingGroups(t *testing.T) {
	now := time.Date(2026, 4, 21, 20, 59, 0, 0, time.UTC)
	groupModel := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats:          []store.Chat{{ID: "chat-1", JID: "family@g.us", Title: "Family", Kind: "group"}},
			MessagesByChat: map[string][]store.Message{"chat-1": nil},
			DraftsByChat:   map[string]string{},
			ActiveChatID:   "chat-1",
		},
	})
	groupBubble := stripANSI(groupModel.renderMessageBubble(store.Message{
		ID:        "m-1",
		ChatID:    "chat-1",
		ChatJID:   "family@g.us",
		Sender:    "Dad",
		Body:      "Dinner on Sunday?",
		Timestamp: now,
	}, 80, false, false))
	if !strings.Contains(groupBubble, "Dad") || !strings.Contains(groupBubble, "20:59") {
		t.Fatalf("group incoming bubble missing sender/time\n%s", groupBubble)
	}

	directModel := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats:          []store.Chat{{ID: "chat-1", JID: "alice@s.whatsapp.net", Title: "Alice", Kind: "direct"}},
			MessagesByChat: map[string][]store.Message{"chat-1": nil},
			DraftsByChat:   map[string]string{},
			ActiveChatID:   "chat-1",
		},
	})
	directBubble := stripANSI(directModel.renderMessageBubble(store.Message{
		ID:        "m-1",
		ChatID:    "chat-1",
		ChatJID:   "alice@s.whatsapp.net",
		Sender:    "Alice",
		Body:      "Dinner on Sunday?",
		Timestamp: now,
	}, 80, false, false))
	if strings.Contains(directBubble, "Alice") {
		t.Fatalf("direct incoming bubble should not repeat sender name\n%s", directBubble)
	}
	if !strings.Contains(directBubble, "20:59") {
		t.Fatalf("direct incoming bubble missing time\n%s", directBubble)
	}
}

func TestMessageStatusTicks(t *testing.T) {
	tests := map[string]string{
		"":           "",
		"pending":    "…",
		"queued":     "…",
		"sending":    "…",
		"sent":       "✓",
		"server_ack": "✓",
		"delivered":  "✓✓",
		"read":       "✓✓",
		"custom":     "✓",
	}
	for status, want := range tests {
		if got := messageStatusTicks(status); got != want {
			t.Fatalf("messageStatusTicks(%q) = %q, want %q", status, got, want)
		}
	}
}

func TestShortMessageBubblesUseProportionalWidth(t *testing.T) {
	now := time.Date(2026, 4, 21, 20, 59, 0, 0, time.UTC)
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats:          []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{"chat-1": nil},
			DraftsByChat:   map[string]string{},
			ActiveChatID:   "chat-1",
		},
	})

	shortBubble := stripANSI(model.renderMessageBubble(store.Message{
		ID:        "m-1",
		ChatID:    "chat-1",
		Body:      "ok",
		Timestamp: now,
	}, 80, false, false))
	longBubble := stripANSI(model.renderMessageBubble(store.Message{
		ID:        "m-2",
		ChatID:    "chat-1",
		Body:      "this message is long enough to use a wider bubble and wrap naturally",
		Timestamp: now,
	}, 80, false, false))

	shortWidth := maxRenderedLineWidth(shortBubble)
	longWidth := maxRenderedLineWidth(longBubble)
	if shortWidth >= longWidth {
		t.Fatalf("short bubble width = %d, long bubble width = %d, want short < long\nshort:\n%s\nlong:\n%s", shortWidth, longWidth, shortBubble, longBubble)
	}
	if shortWidth > 12 {
		t.Fatalf("short bubble width = %d, want compact width <= 12\n%s", shortWidth, shortBubble)
	}
}

func TestShortOutgoingBubbleIsSmallAndRightAligned(t *testing.T) {
	now := time.Date(2026, 4, 21, 20, 59, 0, 0, time.UTC)
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats: []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{
				"chat-1": []store.Message{{
					ID:         "m-1",
					ChatID:     "chat-1",
					Body:       "ok",
					Timestamp:  now,
					IsOutgoing: true,
					Status:     "read",
				}},
			},
			DraftsByChat: map[string]string{},
			ActiveChatID: "chat-1",
		},
	})

	view := stripANSI(model.renderMessages(80, 10))
	bodyLine := plainLineContaining(view, "ok")
	if bodyLine == "" {
		t.Fatalf("short outgoing body missing\n%s", view)
	}
	if leadingSpaces(bodyLine) < 50 {
		t.Fatalf("short outgoing bubble was not right aligned: %q\n%s", bodyLine, view)
	}
	if got := lipgloss.Width(strings.TrimLeft(bodyLine, " ")); got > 12 {
		t.Fatalf("short outgoing bubble line width = %d, want compact <= 12\n%s", got, view)
	}
	if !strings.Contains(view, "20:59 ✓✓") {
		t.Fatalf("short outgoing bubble missing compact read receipt\n%s", view)
	}
}

func TestOutgoingBubblesKeepRightMarginToAvoidTerminalWrap(t *testing.T) {
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats: []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{
				"chat-1": []store.Message{
					{
						ID:         "m-1",
						ChatID:     "chat-1",
						Sender:     "me",
						Body:       "outgoing text that wraps across several visual lines in a narrow message pane",
						IsOutgoing: true,
					},
				},
			},
			DraftsByChat: map[string]string{},
			ActiveChatID: "chat-1",
		},
	})

	const width = 42
	view := model.renderMessages(width, 12)
	for i, line := range strings.Split(view, "\n") {
		plain := stripANSI(line)
		if !strings.Contains(plain, "outgoing") && !strings.Contains(plain, "visual") && !strings.Contains(plain, "narrow") && !strings.Contains(plain, "╭") && !strings.Contains(plain, "╰") {
			continue
		}
		if got := lipgloss.Width(line); got >= width {
			t.Fatalf("outgoing line %d width = %d, want < %d to avoid terminal edge wrap\n%s", i+1, got, width, stripANSI(view))
		}
	}
	messageStarted := false
	blankAfterMessage := false
	for _, line := range strings.Split(stripANSI(view), "\n") {
		if messageStarted && isFooterLine(line) {
			break
		}
		if strings.Contains(line, "outgoing text") {
			messageStarted = true
		}
		if messageStarted && strings.TrimSpace(line) == "" {
			blankAfterMessage = true
			continue
		}
		if messageStarted && blankAfterMessage && strings.TrimSpace(line) != "" {
			t.Fatalf("outgoing message rendered blank spacer lines inside the bubble\n%s", stripANSI(view))
		}
	}
}

func TestFullViewOutgoingWrappedMessagesDoNotRenderBlankRows(t *testing.T) {
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats: []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{
				"chat-1": []store.Message{
					{
						ID:         "m-1",
						ChatID:     "chat-1",
						Sender:     "me",
						Body:       "this is a long outgoing message that should wrap cleanly without visual spacer rows between wrapped lines",
						IsOutgoing: true,
					},
				},
			},
			DraftsByChat: map[string]string{},
			ActiveChatID: "chat-1",
		},
	})
	model.width = 92
	model.height = 14
	model.compactLayout = true
	model.focus = FocusMessages

	view := stripANSI(model.View())
	messageStarted := false
	blankAfterMessage := false
	for _, line := range strings.Split(view, "\n") {
		if messageStarted && isFooterLine(line) {
			break
		}
		if strings.Contains(line, "long outgoing message") {
			messageStarted = true
		}
		if messageStarted && strings.TrimSpace(line) == "" {
			blankAfterMessage = true
			continue
		}
		if messageStarted && blankAfterMessage && strings.TrimSpace(line) != "" {
			t.Fatalf("full view rendered blank rows inside outgoing message\n%s", view)
		}
	}
	for _, line := range strings.Split(model.View(), "\n") {
		plain := stripANSI(line)
		if strings.Contains(plain, "outgoing") || strings.Contains(plain, "visual spacer") {
			if strings.HasSuffix(plain, " ") {
				t.Fatalf("outgoing message line retained trailing spaces that can wrap visually: %q", plain)
			}
		}
	}
}

func TestStatusAndPromptExposeModeWorkflow(t *testing.T) {
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats:          []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{"chat-1": nil},
			DraftsByChat:   map[string]string{},
			ActiveChatID:   "chat-1",
		},
	})
	model.width = 100
	model.height = 20
	model.mode = ModeCommand
	model.focus = FocusMessages
	model.commandLine = "help"

	status := stripANSI(model.renderStatus())
	if !strings.Contains(status, "COMMAND") || !strings.Contains(status, "MESSAGES") {
		t.Fatalf("status missing mode/focus: %q", status)
	}
	prompt := stripANSI(model.renderInput())
	if !strings.Contains(prompt, "[COMMAND]") || !strings.Contains(prompt, ":help") || !strings.Contains(prompt, "enter run") {
		t.Fatalf("command prompt missing workflow: %q", prompt)
	}

	model.mode = ModeSearch
	model.searchLine = "needle"
	prompt = stripANSI(model.renderInput())
	if !strings.Contains(prompt, "[SEARCH]") || !strings.Contains(prompt, "/needle") || !strings.Contains(prompt, "empty clears") {
		t.Fatalf("search prompt missing workflow: %q", prompt)
	}

	commandStatus := model.renderStatus()
	model.mode = ModeInsert
	insertStatus := model.renderStatus()
	if commandStatus == insertStatus {
		t.Fatal("statusbar styling did not change between command and insert modes")
	}
	if modeStatusColor(ModeInsert) != uiTheme.InsertModeBG {
		t.Fatalf("insert mode status color = %q, want %q", modeStatusColor(ModeInsert), uiTheme.InsertModeBG)
	}
}

func TestFullViewShowsStatusAndComposerInInsertMode(t *testing.T) {
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats: []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{
				"chat-1": []store.Message{{ID: "m-1", ChatID: "chat-1", Sender: "Alice", Body: "hello"}},
			},
			DraftsByChat: map[string]string{},
			ActiveChatID: "chat-1",
		},
	})
	model.width = 88
	model.height = 8
	model.mode = ModeInsert
	model.focus = FocusMessages
	model.composer = "draft reply"

	view := stripANSI(model.View())
	for _, want := range []string{"INSERT", "MESSAGES", "? help", "> draft reply▌"} {
		if !strings.Contains(view, want) {
			t.Fatalf("full insert view missing %q\n%s", want, view)
		}
	}
	if strings.Contains(view, "[INSERT] to Alice") || strings.Contains(view, "enter send") {
		t.Fatalf("full insert view retained noisy footer workflow text\n%s", view)
	}
	composerLine := plainLineContaining(view, "? help")
	composerColumn := strings.Index(composerLine, "? help")
	if composerColumn < 24 {
		t.Fatalf("composer rendered outside the message pane at column %d\n%s", composerColumn, view)
	}
	if got := len(strings.Split(view, "\n")); got > model.height {
		t.Fatalf("View() produced %d lines, want <= %d\n%s", got, model.height, view)
	}
}

func TestComposerOverlaysBottomOfShortMessagePane(t *testing.T) {
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats:          []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{"chat-1": nil},
			DraftsByChat:   map[string]string{},
			ActiveChatID:   "chat-1",
		},
	})
	model.mode = ModeInsert
	model.focus = FocusMessages
	model.composer = "short"

	view := stripANSI(model.renderMessages(70, 10))
	lines := strings.Split(view, "\n")
	composerLine := -1
	for i, line := range lines {
		if strings.Contains(line, "? help") {
			composerLine = i
			break
		}
	}
	if composerLine < len(lines)-3 {
		t.Fatalf("composer was not fixed to bottom: line %d of %d\n%s", composerLine+1, len(lines), view)
	}
	if !strings.Contains(view, "No messages in current chat.") {
		t.Fatalf("short chat lost message area while showing composer\n%s", view)
	}
}

func TestFullViewShowsComposerForShortChatInInsertMode(t *testing.T) {
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats: []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{
				"chat-1": []store.Message{{ID: "m-1", ChatID: "chat-1", Sender: "Alice", Body: "short chat"}},
			},
			DraftsByChat: map[string]string{},
			ActiveChatID: "chat-1",
		},
	})
	model.width = 100
	model.height = 18
	model.mode = ModeInsert
	model.focus = FocusMessages
	model.composer = "visible composer"

	view := stripANSI(model.View())
	if !strings.Contains(view, "? help") || !strings.Contains(view, "> visible composer▌") {
		t.Fatalf("full view did not show composer for short chat\n%s", view)
	}
	if strings.Contains(view, "[INSERT] to Alice") || strings.Contains(view, "ctrl+j newline") {
		t.Fatalf("full view retained noisy insert footer text\n%s", view)
	}
	composerLine := plainLineContaining(view, "? help")
	if strings.Index(composerLine, "? help") < 24 {
		t.Fatalf("composer did not render inside message pane\n%s", view)
	}
}

func TestFullViewShowsComposerForEmptyChatInInsertMode(t *testing.T) {
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats:          []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{"chat-1": nil},
			DraftsByChat:   map[string]string{},
			ActiveChatID:   "chat-1",
		},
	})
	model.width = 100
	model.height = 18
	model.mode = ModeInsert
	model.focus = FocusMessages
	model.composer = ""

	view := stripANSI(model.View())
	for _, want := range []string{"No messages in current chat.", "? help", "> ▌"} {
		if !strings.Contains(view, want) {
			t.Fatalf("full view missing %q for empty insert chat\n%s", want, view)
		}
	}
	if strings.Contains(view, "[INSERT]") || strings.Contains(view, "esc save draft") {
		t.Fatalf("empty insert footer retained noisy workflow text\n%s", view)
	}
}

func TestFullViewShowsComposerForEmptyChatInNormalMode(t *testing.T) {
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats:          []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{"chat-1": nil},
			DraftsByChat:   map[string]string{},
			ActiveChatID:   "chat-1",
		},
	})
	model.width = 100
	model.height = 18
	model.mode = ModeNormal
	model.focus = FocusMessages

	view := stripANSI(model.View())
	for _, want := range []string{"No messages in current chat.", "? help", ">"} {
		if !strings.Contains(view, want) {
			t.Fatalf("full view missing %q for empty normal chat\n%s", want, view)
		}
	}
	if strings.Contains(view, "[NORMAL]") || strings.Contains(view, "i insert") {
		t.Fatalf("empty normal footer retained noisy workflow text\n%s", view)
	}
}

func TestVisualFooterIsMinimal(t *testing.T) {
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats: []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{
				"chat-1": []store.Message{{ID: "m-1", ChatID: "chat-1", Sender: "Alice", Body: "selected message"}},
			},
			DraftsByChat: map[string]string{"chat-1": "draft reply"},
			ActiveChatID: "chat-1",
		},
	})
	model.mode = ModeVisual
	model.focus = FocusMessages

	view := stripANSI(model.renderMessages(70, 10))
	for _, want := range []string{"? help", "> draft reply"} {
		if !strings.Contains(view, want) {
			t.Fatalf("visual footer missing %q\n%s", want, view)
		}
	}
	for _, unwanted := range []string{"[VISUAL]", "j/k extend", "y yank", "esc normal"} {
		if strings.Contains(view, unwanted) {
			t.Fatalf("visual footer retained noisy workflow text %q\n%s", unwanted, view)
		}
	}
}

func TestVisualYankCopiesSelectionToClipboard(t *testing.T) {
	var copied string
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats: []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{
				"chat-1": []store.Message{
					{ID: "m-1", ChatID: "chat-1", Sender: "Alice", Body: "first"},
					{ID: "m-2", ChatID: "chat-1", Sender: "Alice", Body: "second"},
				},
			},
			DraftsByChat: map[string]string{},
			ActiveChatID: "chat-1",
		},
		CopyToClipboard: func(text string) error {
			copied = text
			return nil
		},
	})
	model.mode = ModeVisual
	model.focus = FocusMessages
	model.visualAnchor = 0
	model.messageCursor = 1

	updated, cmd := model.updateVisual(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	got := updated.(Model)
	if got.yankRegister != "first\nsecond" {
		t.Fatalf("yankRegister = %q", got.yankRegister)
	}
	if cmd == nil {
		t.Fatal("visual yank did not return clipboard command")
	}
	final, _ := got.Update(cmd())
	got = final.(Model)
	if copied != "first\nsecond" {
		t.Fatalf("copied = %q", copied)
	}
	if !strings.Contains(got.status, "clipboard") {
		t.Fatalf("status = %q, want clipboard copy result", got.status)
	}
}

func TestAttachmentPickerStagesAttachmentInComposer(t *testing.T) {
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats:          []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{"chat-1": nil},
			DraftsByChat:   map[string]string{},
			ActiveChatID:   "chat-1",
		},
		PickAttachment: func() tea.Cmd {
			return func() tea.Msg {
				return AttachmentPickedMsg{Attachment: Attachment{
					LocalPath:     "/tmp/photo.jpg",
					FileName:      "photo.jpg",
					MIMEType:      "image/jpeg",
					SizeBytes:     2048,
					DownloadState: "local_pending",
				}}
			}
		},
	})
	model.mode = ModeInsert
	model.focus = FocusMessages

	updated, cmd := model.updateInsert(tea.KeyMsg{Type: tea.KeyCtrlF})
	got := updated.(Model)
	if cmd == nil {
		t.Fatal("ctrl+f did not start picker")
	}
	final, _ := got.Update(cmd())
	got = final.(Model)
	if len(got.attachments) != 1 || got.attachments[0].FileName != "photo.jpg" {
		t.Fatalf("attachments = %+v", got.attachments)
	}
	view := stripANSI(got.renderMessages(70, 10))
	if !strings.Contains(view, "[img] photo.jpg 2.0KB local_pending") {
		t.Fatalf("composer did not render staged attachment\n%s", view)
	}
}

func TestAttachmentOnlySendPersistsMedia(t *testing.T) {
	var sentAttachments []Attachment
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats:          []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{"chat-1": nil},
			DraftsByChat:   map[string]string{},
			ActiveChatID:   "chat-1",
		},
		PersistMessage: func(chatID, body string, attachments []Attachment) (store.Message, error) {
			sentAttachments = attachments
			return store.Message{
				ID:         "local-1",
				ChatID:     chatID,
				Sender:     "me",
				Body:       body,
				IsOutgoing: true,
				Media: []store.MediaMetadata{{
					MessageID:     "local-1",
					FileName:      attachments[0].FileName,
					MIMEType:      attachments[0].MIMEType,
					SizeBytes:     attachments[0].SizeBytes,
					DownloadState: attachments[0].DownloadState,
				}},
			}, nil
		},
		SaveDraft: func(chatID, body string) error { return nil },
	})
	model.mode = ModeInsert
	model.attachments = []Attachment{{
		LocalPath:     "/tmp/report.pdf",
		FileName:      "report.pdf",
		MIMEType:      "application/pdf",
		SizeBytes:     1024,
		DownloadState: "local_pending",
	}}

	updated, _ := model.updateInsert(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if len(sentAttachments) != 1 || sentAttachments[0].FileName != "report.pdf" {
		t.Fatalf("sentAttachments = %+v", sentAttachments)
	}
	if len(got.attachments) != 0 {
		t.Fatalf("attachments were not cleared: %+v", got.attachments)
	}
	if messages := got.messagesByChat["chat-1"]; len(messages) != 1 || len(messages[0].Media) != 1 {
		t.Fatalf("messages after send = %+v", messages)
	}
}

func TestMessageBubbleRendersAttachmentRows(t *testing.T) {
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats:          []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{"chat-1": nil},
			DraftsByChat:   map[string]string{},
			ActiveChatID:   "chat-1",
		},
	})

	bubble := stripANSI(model.renderMessageBubble(store.Message{
		ID:     "m-1",
		ChatID: "chat-1",
		Body:   "see attached",
		Media: []store.MediaMetadata{{
			MessageID:     "m-1",
			FileName:      "report.pdf",
			MIMEType:      "application/pdf",
			SizeBytes:     2048,
			DownloadState: "downloaded",
		}},
	}, 80, false, false))
	if !strings.Contains(bubble, "[pdf] report.pdf 2.0KB") || !strings.Contains(bubble, "see attached") {
		t.Fatalf("bubble did not render media row and caption\n%s", bubble)
	}
}

func TestVisibleMediaQueuesPreviewRequest(t *testing.T) {
	model := NewModel(Options{
		Config: configWithPreview(24, 6),
		Paths:  testPaths(t),
		PreviewReport: media.Report{
			Selected: media.BackendChafa,
		},
		Snapshot: store.Snapshot{
			Chats: []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{
				"chat-1": []store.Message{{
					ID:     "m-1",
					ChatID: "chat-1",
					Sender: "Alice",
					Body:   "photo",
					Media: []store.MediaMetadata{{
						MessageID:     "m-1",
						FileName:      "photo.jpg",
						MIMEType:      "image/jpeg",
						LocalPath:     "/tmp/photo.jpg",
						DownloadState: "downloaded",
					}},
				}},
			},
			DraftsByChat: map[string]string{},
			ActiveChatID: "chat-1",
		},
	})
	model.width = 100
	model.height = 20

	requests := model.visiblePreviewRequests()
	if len(requests) != 1 {
		t.Fatalf("visiblePreviewRequests() = %+v, want one request", requests)
	}
	if requests[0].Width != 24 || requests[0].Height != 6 || requests[0].Backend != media.BackendChafa {
		t.Fatalf("request sizing/backend = %+v", requests[0])
	}

	queued, cmd := model.queueVisiblePreviewCmd()
	if cmd == nil {
		t.Fatal("queueVisiblePreviewCmd() returned nil command")
	}
	if !queued.previewInflight[media.PreviewKey(requests[0])] {
		t.Fatalf("previewInflight = %+v, want request key marked", queued.previewInflight)
	}
}

func TestCachedMediaPreviewRendersInsideBubble(t *testing.T) {
	model := NewModel(Options{
		Config: configWithPreview(24, 6),
		Paths:  testPaths(t),
		PreviewReport: media.Report{
			Selected: media.BackendChafa,
		},
		Snapshot: store.Snapshot{
			Chats: []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{
				"chat-1": []store.Message{{
					ID:     "m-1",
					ChatID: "chat-1",
					Sender: "Alice",
					Body:   "photo",
					Media: []store.MediaMetadata{{
						MessageID:     "m-1",
						FileName:      "photo.jpg",
						MIMEType:      "image/jpeg",
						LocalPath:     "/tmp/photo.jpg",
						DownloadState: "downloaded",
					}},
				}},
			},
			DraftsByChat: map[string]string{},
			ActiveChatID: "chat-1",
		},
	})
	model.width = 100
	model.height = 20
	request := model.visiblePreviewRequests()[0]
	model.previewCache[media.PreviewKey(request)] = media.Preview{
		Key:             media.PreviewKey(request),
		MessageID:       "m-1",
		Kind:            media.KindImage,
		Backend:         media.BackendChafa,
		RenderedBackend: media.BackendChafa,
		Lines:           []string{"IMAGE PREVIEW"},
	}

	view := stripANSI(model.renderMessages(80, 12))
	if !strings.Contains(view, "IMAGE PREVIEW") {
		t.Fatalf("renderMessages missing cached preview\n%s", view)
	}
	if strings.Contains(view, "[img] photo.jpg") {
		t.Fatalf("renderMessages kept attachment row despite cached preview\n%s", view)
	}
}

func TestGeneratedVideoThumbnailUpdatesMessageMedia(t *testing.T) {
	var saved store.MediaMetadata
	model := NewModel(Options{
		Config: configWithPreview(24, 6),
		Paths:  testPaths(t),
		PreviewReport: media.Report{
			Selected: media.BackendChafa,
		},
		SaveMedia: func(media store.MediaMetadata) error {
			saved = media
			return nil
		},
		Snapshot: store.Snapshot{
			Chats: []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{
				"chat-1": []store.Message{{
					ID:     "m-1",
					ChatID: "chat-1",
					Sender: "Alice",
					Media: []store.MediaMetadata{{
						MessageID:     "m-1",
						FileName:      "clip.mp4",
						MIMEType:      "video/mp4",
						LocalPath:     "/tmp/clip.mp4",
						DownloadState: "downloaded",
					}},
				}},
			},
			DraftsByChat: map[string]string{},
			ActiveChatID: "chat-1",
		},
	})
	model.width = 100
	model.height = 20
	request := model.visiblePreviewRequests()[0]
	updated, _ := model.handleMediaPreviewReady(mediaPreviewReadyMsg{
		Key:     media.PreviewKey(request),
		Request: request,
		Preview: media.Preview{
			Key:                media.PreviewKey(request),
			MessageID:          "m-1",
			Kind:               media.KindVideo,
			Backend:            media.BackendChafa,
			RenderedBackend:    media.BackendChafa,
			ThumbnailPath:      "/tmp/thumb.jpg",
			GeneratedThumbnail: true,
			Lines:              []string{"VIDEO PREVIEW"},
		},
	})
	got := updated.(Model)
	if got.messagesByChat["chat-1"][0].Media[0].ThumbnailPath != "/tmp/thumb.jpg" {
		t.Fatalf("thumbnail path not applied: %+v", got.messagesByChat["chat-1"][0].Media[0])
	}
	if saved.ThumbnailPath != "/tmp/thumb.jpg" {
		t.Fatalf("saved media = %+v, want thumbnail path", saved)
	}
	view := stripANSI(got.renderMessages(80, 12))
	if !strings.Contains(view, "VIDEO PREVIEW") {
		t.Fatalf("renderMessages missing generated video preview\n%s", view)
	}
}

func TestDeleteMessageRequiresConfirmationAndRemovesFocusedMessage(t *testing.T) {
	var deletedID string
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats: []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{
				"chat-1": []store.Message{
					{ID: "m-1", ChatID: "chat-1", Sender: "Alice", Body: "keep"},
					{ID: "m-2", ChatID: "chat-1", Sender: "Alice", Body: "delete me"},
				},
			},
			DraftsByChat: map[string]string{},
			ActiveChatID: "chat-1",
		},
		DeleteMessage: func(messageID string) error {
			deletedID = messageID
			return nil
		},
	})
	model.focus = FocusMessages
	model.messageCursor = 1

	armed, _ := model.executeCommand("delete-message")
	got := armed.(Model)
	if deletedID != "" {
		t.Fatal("delete-message deleted without confirmation")
	}
	if got.deleteConfirmID != "m-2" {
		t.Fatalf("deleteConfirmID = %q, want m-2", got.deleteConfirmID)
	}

	deleted, _ := got.executeCommand("delete-message confirm")
	got = deleted.(Model)
	if deletedID != "m-2" {
		t.Fatalf("deletedID = %q, want m-2", deletedID)
	}
	messages := got.messagesByChat["chat-1"]
	if len(messages) != 1 || messages[0].ID != "m-1" {
		t.Fatalf("messages after delete = %+v", messages)
	}
}

func TestCompactInsertComposerVisibleAt80ColumnsWithDatedMessages(t *testing.T) {
	now := time.Date(2026, 4, 21, 20, 0, 0, 0, time.UTC)
	messages := []store.Message{
		{ID: "m-1", ChatID: "chat-1", Sender: "Alice", Body: "Are you finally building the terminal client?", Timestamp: now.Add(-25 * time.Hour)},
		{ID: "m-2", ChatID: "chat-1", Sender: "me", Body: "Yes. The shell is real now and backed by SQLite.", Timestamp: now.Add(-23 * time.Hour), IsOutgoing: true, Status: "sent"},
		{ID: "m-3", ChatID: "chat-1", Sender: "Alice", Body: "Good. Make the motions feel strict, not chatty.", Timestamp: now.Add(-10 * time.Minute)},
		{ID: "m-4", ChatID: "chat-1", Sender: "me", Body: "I'm tightening the TUI before touching live sync.", Timestamp: now, IsOutgoing: true, Status: "pending"},
	}
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats:          []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{"chat-1": messages},
			DraftsByChat:   map[string]string{},
			ActiveChatID:   "chat-1",
		},
	})
	model.width = 80
	model.height = 24
	model.compactLayout = true
	model.focus = FocusMessages
	model.mode = ModeInsert
	model.messageCursor = len(messages) - 1
	model.messageScrollTop = len(messages) - 1

	view := stripANSI(model.View())
	for _, want := range []string{"? help", "> ▌"} {
		if !strings.Contains(view, want) {
			t.Fatalf("compact 80-column insert view missing %q\n%s", want, view)
		}
	}
	if strings.Contains(view, "[INSERT]") || strings.Contains(view, "enter send") {
		t.Fatalf("compact 80-column insert footer retained noisy workflow text\n%s", view)
	}
	for _, line := range strings.Split(view, "\n") {
		inner := strings.TrimSpace(line)
		inner = strings.TrimPrefix(inner, "│")
		inner = strings.TrimSuffix(inner, "│")
		if strings.TrimSpace(inner) == "--" {
			t.Fatalf("panel content wrapped and produced a stray separator continuation row\n%s", view)
		}
	}
	if got := len(strings.Split(view, "\n")); got > model.height {
		t.Fatalf("View() produced %d lines, want <= %d\n%s", got, model.height, view)
	}
	for i, line := range strings.Split(view, "\n") {
		if width := lipgloss.Width(line); width > model.width {
			t.Fatalf("line %d width = %d, want <= %d\n%s", i+1, width, model.width, view)
		}
	}
}

func TestComposerRemainsVisibleOverFullMessagePane(t *testing.T) {
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats:          []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{"chat-1": numberedMessages(24)},
			DraftsByChat:   map[string]string{},
			ActiveChatID:   "chat-1",
		},
	})
	model.mode = ModeInsert
	model.focus = FocusMessages
	model.composer = "visible"
	model.messageCursor = 23
	model.messageScrollTop = 23

	view := stripANSI(model.renderMessages(70, 8))
	if !strings.Contains(view, "? help") || !strings.Contains(view, "> visible▌") {
		t.Fatalf("composer was not visible over full message pane\n%s", view)
	}
	if strings.Contains(view, "[INSERT]") || strings.Contains(view, "ctrl+j newline") {
		t.Fatalf("full message pane retained noisy insert footer text\n%s", view)
	}
	if got := len(strings.Split(view, "\n")); got > 8 {
		t.Fatalf("renderMessages produced %d lines, want <= 8\n%s", got, view)
	}
}

func TestMessageVerticalAlignmentDoesNotChangeBetweenNormalAndInsert(t *testing.T) {
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats:          []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{"chat-1": numberedMessages(20)},
			DraftsByChat:   map[string]string{},
			ActiveChatID:   "chat-1",
		},
	})
	model.focus = FocusMessages
	model.messageCursor = 19
	model.messageScrollTop = 19

	normalView := stripANSI(model.renderMessages(70, 10))
	normalLine := lineIndexContaining(normalView, "message 19")
	if normalLine == -1 {
		t.Fatalf("normal view missing newest message\n%s", normalView)
	}

	model.mode = ModeInsert
	insertView := stripANSI(model.renderMessages(70, 10))
	insertLine := lineIndexContaining(insertView, "message 19")
	if insertLine == -1 {
		t.Fatalf("insert view missing newest message\n%s", insertView)
	}
	if normalLine != insertLine {
		t.Fatalf("newest message line changed from normal=%d to insert=%d\nnormal:\n%s\ninsert:\n%s", normalLine, insertLine, normalView, insertView)
	}
}

func TestSendingMessageScrollsConversationToNewestMessage(t *testing.T) {
	messages := numberedMessages(18)
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats:          []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{"chat-1": messages},
			DraftsByChat:   map[string]string{},
			ActiveChatID:   "chat-1",
		},
		PersistMessage: func(chatID, body string, attachments []Attachment) (store.Message, error) {
			return store.Message{ID: "local-1", ChatID: chatID, Sender: "me", Body: body, IsOutgoing: true}, nil
		},
	})
	model.mode = ModeInsert
	model.focus = FocusMessages
	model.composer = "newest sent message"

	updated, _ := model.updateInsert(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if got.messageCursor != len(got.currentMessages())-1 {
		t.Fatalf("messageCursor = %d, want last message", got.messageCursor)
	}
	if got.messageScrollTop != got.messageCursor {
		t.Fatalf("messageScrollTop = %d, want cursor %d", got.messageScrollTop, got.messageCursor)
	}
	view := stripANSI(got.renderMessages(70, 8))
	if !strings.Contains(view, "newest sent message") {
		t.Fatalf("sent message was not visible after send\n%s", view)
	}
	if strings.Contains(view, "message 00") {
		t.Fatalf("viewport did not move away from the oldest message after send\n%s", view)
	}
	assertLastNonEmptyLineContains(t, view, "> ▌")
	assertLineBeforeComposerContains(t, view, "newest sent message")
}

func TestMessageNavigationMovesViewportDown(t *testing.T) {
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats:          []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{"chat-1": numberedMessages(20)},
			DraftsByChat:   map[string]string{},
			ActiveChatID:   "chat-1",
		},
	})
	model.focus = FocusMessages

	for i := 0; i < 10; i++ {
		updated, _ := model.updateNormal(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
		model = updated.(Model)
	}
	if model.messageCursor != 10 {
		t.Fatalf("messageCursor = %d, want 10", model.messageCursor)
	}
	if model.messageScrollTop == 0 {
		t.Fatal("messageScrollTop did not advance while moving down")
	}
	view := stripANSI(model.renderMessages(70, 8))
	if !strings.Contains(view, "message 10") {
		t.Fatalf("selected message was not visible after scrolling down\n%s", view)
	}
	if strings.Contains(view, "message 00") {
		t.Fatalf("viewport did not move away from oldest messages\n%s", view)
	}
}

func TestMessageNavigationTopAndBottomCommandsMoveViewport(t *testing.T) {
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats:          []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{"chat-1": numberedMessages(20)},
			DraftsByChat:   map[string]string{},
			ActiveChatID:   "chat-1",
		},
	})
	model.focus = FocusMessages

	bottom, _ := model.updateNormal(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("G")})
	model = bottom.(Model)
	bottomView := stripANSI(model.renderMessages(70, 8))
	if !strings.Contains(bottomView, "message 19") {
		t.Fatalf("G did not show newest message\n%s", bottomView)
	}
	if strings.Contains(bottomView, "message 00") {
		t.Fatalf("G left oldest message visible\n%s", bottomView)
	}
	assertLineBeforeFooterContains(t, bottomView, "message 19")

	top, _ := model.updateNormal(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("g")})
	model = top.(Model)
	topView := stripANSI(model.renderMessages(70, 8))
	if !strings.Contains(topView, "message 00") {
		t.Fatalf("g did not show oldest message\n%s", topView)
	}
	if strings.Contains(topView, "message 19") {
		t.Fatalf("g left newest message visible\n%s", topView)
	}
}

func TestShortChatStartsAtTopEvenWhenNewestSelected(t *testing.T) {
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats: []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{
				"chat-1": []store.Message{
					{ID: "m-1", ChatID: "chat-1", Sender: "Alice", Body: "first short"},
					{ID: "m-2", ChatID: "chat-1", Sender: "Alice", Body: "second short"},
				},
			},
			DraftsByChat: map[string]string{},
			ActiveChatID: "chat-1",
		},
	})
	model.focus = FocusMessages
	model.messageCursor = 1
	model.messageScrollTop = 1

	view := stripANSI(model.renderMessages(70, 12))
	lines := strings.Split(view, "\n")
	if len(lines) < 2 || strings.TrimSpace(lines[1]) == "" {
		t.Fatalf("short chat did not start at top\n%s", view)
	}
	if !strings.Contains(view, "first short") || !strings.Contains(view, "second short") {
		t.Fatalf("short chat did not show all messages\n%s", view)
	}
}

func TestViewportDoesNotAllowBlankSpaceBelowNewestMessageWhenOverscrolled(t *testing.T) {
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats:          []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{"chat-1": numberedMessages(20)},
			DraftsByChat:   map[string]string{},
			ActiveChatID:   "chat-1",
		},
	})
	model.focus = FocusMessages
	model.messageCursor = 18
	model.messageScrollTop = 19

	view := stripANSI(model.renderMessages(70, 8))
	assertLineBeforeFooterContains(t, view, "message 19")
	if !strings.Contains(view, "message 18") {
		t.Fatalf("overscrolled viewport did not keep selected message visible\n%s", view)
	}
}

func TestDefaultRenderingAvoidsBackgroundFills(t *testing.T) {
	t.Setenv("MAYBEWHATS_TRANSPARENT_BARS", "1")
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats: []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{
				"chat-1": []store.Message{{ID: "m-1", ChatID: "chat-1", Sender: "Alice", Body: "transparent"}},
			},
			DraftsByChat: map[string]string{},
			ActiveChatID: "chat-1",
		},
	})
	model.width = 100
	model.height = 20

	view := model.View()
	if strings.Contains(view, "\x1b[48;") {
		t.Fatalf("view contains ANSI background fill despite transparent bars:\n%q", view)
	}
}

func TestLoadThemeReadsPywalColors(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	walDir := filepath.Join(home, ".cache", "wal")
	if err := os.MkdirAll(walDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	data := `{
		"special": {"background": "#000001", "foreground": "#eeeeee"},
		"colors": {
			"color0": "#000001",
			"color2": "#222222",
			"color3": "#333333",
			"color4": "#444444",
			"color5": "#555555",
			"color6": "#666666",
			"color8": "#888888",
			"color10": "#aaaaaa"
		}
	}`
	if err := os.WriteFile(filepath.Join(walDir, "colors.json"), []byte(data), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	theme := loadTheme()
	if theme.PrimaryFG != lipgloss.Color("#eeeeee") {
		t.Fatalf("PrimaryFG = %q, want #eeeeee", theme.PrimaryFG)
	}
	if theme.AccentFG != lipgloss.Color("#444444") {
		t.Fatalf("AccentFG = %q, want #444444", theme.AccentFG)
	}
	if theme.InsertModeBG != lipgloss.Color("#555555") {
		t.Fatalf("InsertModeBG = %q, want #555555", theme.InsertModeBG)
	}
	if theme.FocusedLine != lipgloss.Color("#666666") {
		t.Fatalf("FocusedLine = %q, want #666666", theme.FocusedLine)
	}
}

func TestFullViewDoesNotGrowPastTerminalHeightWithManyMessages(t *testing.T) {
	messages := make([]store.Message, 0, 40)
	for i := 0; i < 40; i++ {
		messages = append(messages, store.Message{
			ID:         "m",
			ChatID:     "chat-1",
			Sender:     "me",
			Body:       "a message that should not force the outer tui to move upward",
			IsOutgoing: i%2 == 1,
		})
	}

	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats:          []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{"chat-1": messages},
			DraftsByChat:   map[string]string{},
			ActiveChatID:   "chat-1",
		},
	})
	model.width = 120
	model.height = 24
	model.messageCursor = len(messages) - 1

	view := model.View()
	if got := len(strings.Split(view, "\n")); got > model.height {
		t.Fatalf("View() produced %d lines, want <= %d", got, model.height)
	}
	for i, line := range strings.Split(view, "\n") {
		if width := lipgloss.Width(line); width > model.width {
			t.Fatalf("line %d width = %d, want <= %d", i+1, width, model.width)
		}
	}
}

func TestMessagesViewportDoesNotGrowPastPanelHeight(t *testing.T) {
	messages := make([]store.Message, 0, 30)
	for i := 0; i < 30; i++ {
		messages = append(messages, store.Message{
			ID:         "m",
			ChatID:     "chat-1",
			Sender:     "me",
			Body:       "a message that should not force the outer tui to move upward",
			IsOutgoing: i%2 == 1,
		})
	}

	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats:          []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{"chat-1": messages},
			DraftsByChat:   map[string]string{},
			ActiveChatID:   "chat-1",
		},
	})
	model.messageCursor = len(messages) - 1

	const height = 12
	view := model.renderMessages(70, height)
	if got := len(strings.Split(view, "\n")); got > height {
		t.Fatalf("renderMessages produced %d lines, want <= %d\n%s", got, height, view)
	}
}

var ansiRE = regexp.MustCompile(`\x1b\[[0-9;:]*m`)

func stripANSI(value string) string {
	return ansiRE.ReplaceAllString(value, "")
}

func plainLineContaining(view, needle string) string {
	for _, line := range strings.Split(stripANSI(view), "\n") {
		if strings.Contains(line, needle) {
			return line
		}
	}
	return ""
}

func maxRenderedLineWidth(view string) int {
	width := 0
	for _, line := range strings.Split(stripANSI(view), "\n") {
		width = max(width, lipgloss.Width(line))
	}
	return width
}

func leadingSpaces(value string) int {
	return len(value) - len(strings.TrimLeft(value, " "))
}

func isFooterLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	return strings.Contains(trimmed, "? help") ||
		(trimmed != "" && strings.Trim(trimmed, "-") == "")
}

func lineIndexContaining(view, needle string) int {
	for i, line := range strings.Split(view, "\n") {
		if strings.Contains(line, needle) {
			return i
		}
	}
	return -1
}

func assertLastNonEmptyLineContains(t *testing.T, view, want string) {
	t.Helper()
	lines := strings.Split(view, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) == "" {
			continue
		}
		if !strings.Contains(lines[i], want) {
			t.Fatalf("last non-empty line = %q, want it to contain %q\n%s", lines[i], want, view)
		}
		return
	}
	t.Fatalf("view had no non-empty lines; want %q\n%s", want, view)
}

func assertLineBeforeComposerContains(t *testing.T, view, want string) {
	t.Helper()
	lines := strings.Split(view, "\n")
	for i, line := range lines {
		if strings.Contains(line, ">") && strings.Contains(line, "▌") {
			start := max(0, i-4)
			window := strings.Join(lines[start:i], "\n")
			if !strings.Contains(window, want) {
				t.Fatalf("line before composer = %q, want it to contain %q\n%s", lineBefore(lines, i-1), want, view)
			}
			return
		}
	}
	t.Fatalf("composer marker not found\n%s", view)
}

func assertLineBeforeFooterContains(t *testing.T, view, want string) {
	t.Helper()
	lines := strings.Split(view, "\n")
	for i, line := range lines {
		if strings.Contains(line, "? help") {
			if i == 0 || strings.TrimSpace(lines[i-1]) == "" {
				t.Fatalf("blank space before footer; want message bubble touching footer\n%s", view)
			}
			start := max(0, i-4)
			window := strings.Join(lines[start:i], "\n")
			if !strings.Contains(window, want) {
				t.Fatalf("line before footer = %q, want it to contain %q\n%s", lineBefore(lines, i-1), want, view)
			}
			return
		}
	}
	t.Fatalf("footer marker not found\n%s", view)
}

func lineBefore(lines []string, index int) string {
	if index <= 0 || index > len(lines)-1 {
		return ""
	}
	return lines[index-1]
}

func numberedMessages(count int) []store.Message {
	messages := make([]store.Message, 0, count)
	for i := 0; i < count; i++ {
		messages = append(messages, store.Message{
			ID:     "m",
			ChatID: "chat-1",
			Sender: "Alice",
			Body:   fmt.Sprintf("message %02d", i),
		})
	}
	return messages
}

func numberedChats(count int) []store.Chat {
	chats := make([]store.Chat, 0, count)
	for i := 0; i < count; i++ {
		chats = append(chats, store.Chat{
			ID:          fmt.Sprintf("chat-%d", i),
			Title:       fmt.Sprintf("Chat %02d", i),
			LastPreview: fmt.Sprintf("preview %02d", i),
		})
	}
	return chats
}

func configWithPreview(width, height int) config.Config {
	return config.Config{
		PreviewMaxWidth:  width,
		PreviewMaxHeight: height,
		PreviewDelayMS:   0,
	}
}

func testPaths(t *testing.T) config.Paths {
	t.Helper()
	dir := t.TempDir()
	return config.Paths{
		CacheDir:        dir,
		MediaDir:        filepath.Join(dir, "media"),
		PreviewCacheDir: filepath.Join(dir, "preview"),
	}
}
