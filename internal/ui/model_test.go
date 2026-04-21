package ui

import (
	"errors"
	"regexp"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

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
		PersistMessage: func(chatID, body string) (store.Message, error) {
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

func TestSearchMessagesUsesCallbackResults(t *testing.T) {
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats: []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{
				"chat-1": []store.Message{{ID: "m-1", ChatID: "chat-1", Sender: "Alice", Body: "old"}},
			},
			DraftsByChat: map[string]string{},
			ActiveChatID: "chat-1",
		},
		SearchMessages: func(chatID, query string, limit int) ([]store.Message, error) {
			if chatID != "chat-1" || query != "needle" {
				return nil, errors.New("unexpected search")
			}
			return []store.Message{{ID: "m-2", ChatID: "chat-1", Sender: "Alice", Body: "needle result"}}, nil
		},
	})
	model.focus = FocusMessages
	model.mode = ModeSearch
	model.searchLine = "needle"

	updated, _ := model.updateSearch(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	messages := got.messagesByChat["chat-1"]
	if len(messages) != 1 || messages[0].ID != "m-2" {
		t.Fatalf("messages = %+v, want callback result", messages)
	}
	if got.messageCursor != 0 {
		t.Fatalf("messageCursor = %d, want 0", got.messageCursor)
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

func TestClearSearchRestoresMessagesFromStore(t *testing.T) {
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats: []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{
				"chat-1": []store.Message{{ID: "m-1", ChatID: "chat-1", Sender: "Alice", Body: "normal"}},
			},
			DraftsByChat: map[string]string{},
			ActiveChatID: "chat-1",
		},
		SearchMessages: func(chatID, query string, limit int) ([]store.Message, error) {
			return []store.Message{{ID: "m-2", ChatID: chatID, Sender: "Alice", Body: "needle result"}}, nil
		},
		LoadMessages: func(chatID string, limit int) ([]store.Message, error) {
			return []store.Message{{ID: "m-1", ChatID: chatID, Sender: "Alice", Body: "normal"}}, nil
		},
	})
	model.focus = FocusMessages
	model.mode = ModeSearch
	model.searchLine = "needle"

	searched, _ := model.updateSearch(tea.KeyMsg{Type: tea.KeyEnter})
	searchModel := searched.(Model)
	if searchModel.messagesByChat["chat-1"][0].ID != "m-2" {
		t.Fatalf("search messages = %+v", searchModel.messagesByChat["chat-1"])
	}

	cleared, _ := searchModel.executeCommand("clear-search")
	got := cleared.(Model)
	if got.messagesByChat["chat-1"][0].ID != "m-1" {
		t.Fatalf("cleared messages = %+v, want normal store messages", got.messagesByChat["chat-1"])
	}
	if got.lastSearch != "" || len(got.searchMatches) != 0 {
		t.Fatalf("search state was not cleared: last=%q matches=%v", got.lastSearch, got.searchMatches)
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

func TestMessagesRenderAsIncomingAndOutgoingBubbles(t *testing.T) {
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats: []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{
				"chat-1": []store.Message{
					{ID: "m-1", ChatID: "chat-1", Sender: "Alice", Body: "incoming text"},
					{ID: "m-2", ChatID: "chat-1", Sender: "me", Body: "outgoing text that wraps across more than one line in the terminal viewport", IsOutgoing: true},
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
	if leadingSpaces(incomingLine) > 2 {
		t.Fatalf("incoming message was not left aligned: %q", incomingLine)
	}
	if leadingSpaces(outgoingLine) < 20 {
		t.Fatalf("outgoing message was not right aligned: %q", outgoingLine)
	}
	if !strings.Contains(stripANSI(view), "me") {
		t.Fatalf("outgoing bubble did not include sender label\n%s", view)
	}

	outgoingMeta := plainLineContaining(view, "me")
	if outgoingMeta == "" || leadingSpaces(outgoingMeta) < 20 {
		t.Fatalf("outgoing metadata was not part of the right-aligned block: %q", outgoingMeta)
	}

	outgoingIndentedLines := 0
	for _, line := range strings.Split(stripANSI(view), "\n") {
		if strings.Contains(line, "outgoing text") || strings.Contains(line, "more than one line") || strings.Contains(line, "viewport") {
			outgoingIndentedLines++
			if leadingSpaces(line) < 20 {
				t.Fatalf("wrapped outgoing message line drifted left: %q", line)
			}
		}
	}
	if outgoingIndentedLines < 2 {
		t.Fatalf("expected wrapped outgoing message lines, got %d\n%s", outgoingIndentedLines, view)
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

func leadingSpaces(value string) int {
	return len(value) - len(strings.TrimLeft(value, " "))
}
