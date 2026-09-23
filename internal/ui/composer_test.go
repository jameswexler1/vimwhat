package ui

import (
	"errors"
	"testing"
	"vimwhat/internal/store"
)

func composerTestModel() Model {
	return NewModel(Options{Snapshot: store.Snapshot{
		Chats: []store.Chat{{ID: "a", Title: "Alice"}, {ID: "b", Title: "Bob"}}, ActiveChatID: "a",
		MessagesByChat: map[string][]store.Message{}, DraftsByChat: map[string]string{},
	}})
}

func TestLateSendFailureCannotReplaceCurrentComposer(t *testing.T) {
	for _, otherChat := range []bool{false, true} {
		m := composerTestModel()
		if otherChat {
			m.activeChat = 1
		}
		m.mode = ModeInsert
		m.composer = "newer draft"
		m.composerVersion = 2
		m.messagesByChat["a"] = []store.Message{{ID: "temp", ChatID: "a", Body: "private Alice text", IsOutgoing: true}}
		next, cmd := m.handleOutgoingMessagePersisted(outgoingMessagePersistedMsg{ComposerVersion: 1, TempID: "temp", ChatID: "a", DraftBody: "private Alice text", Err: errors.New("queue failure")})
		if next.composer != "newer draft" || next.activeChat != m.activeChat || cmd != nil {
			t.Fatalf("late result replaced composer: %+v", next)
		}
		if next.messagesByChat["a"][0].Status != "failed" {
			t.Fatal("failed content not retained")
		}
	}
}

func TestLateSendSuccessDoesNotClearNewDraft(t *testing.T) {
	m := composerTestModel()
	m.mode = ModeInsert
	m.composer = "newer draft"
	saved := ""
	m.saveDraft = func(chatID, body string) error { saved = body; return nil }
	_, cmd := m.handleOutgoingMessagePersisted(outgoingMessagePersistedMsg{TempID: "temp", ChatID: "a", Message: store.Message{ID: "sent", ChatID: "a"}})
	if cmd != nil {
		cmd()
	}
	if saved != "newer draft" {
		t.Fatalf("saved %q", saved)
	}
}
