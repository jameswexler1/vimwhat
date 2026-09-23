package ui

import (
	"fmt"
	"testing"

	"vimwhat/internal/store"
)

func TestQuoteJumpCompletesAfterDirectWindowLoad(t *testing.T) {
	m := composerTestModel()
	m.messagesByChat["a"] = []store.Message{{ID: "reply", ChatID: "a", QuotedMessageID: "old"}}
	m.focus = FocusMessages
	m.loadMessagesAround = func(chat, target string, limit int) ([]store.Message, error) {
		if chat != "a" || target != "old" || limit > maxHistoryWindow {
			t.Fatalf("unbounded/wrong request %s %s %d", chat, target, limit)
		}
		return []store.Message{{ID: "before", ChatID: chat}, {ID: "old", ChatID: chat}, {ID: "after", ChatID: chat}}, nil
	}
	cmd := m.jumpToQuotedMessage()
	if cmd == nil {
		t.Fatal("quote command missing")
	}
	next, _ := m.Update(cmd())
	m = next.(Model)
	if m.messageCursor != 1 || !m.historyDetached["a"] || m.pendingQuoteByChat["a"] != "" {
		t.Fatalf("jump incomplete: cursor=%d pending=%v", m.messageCursor, m.pendingQuoteByChat)
	}
}

func TestHistoryCacheBoundsChatsAndWindows(t *testing.T) {
	m := composerTestModel()
	for n := 0; n < 20; n++ {
		id := fmt.Sprint(n)
		m.messagesByChat[id] = make([]store.Message, 1000)
	}
	m.messagesByChat["a"] = make([]store.Message, 1000)
	m.messageCursor = 999
	m.boundHistoryCache()
	if len(m.messagesByChat) > maxCachedChats || len(m.messagesByChat["a"]) != maxHistoryWindow || m.messageCursor != maxHistoryWindow-1 {
		t.Fatalf("cache not bounded: chats=%d messages=%d cursor=%d", len(m.messagesByChat), len(m.messagesByChat["a"]), m.messageCursor)
	}
	m.addMessageLimit("a", 10000)
	if m.messageLimitForChat("a") > maxHistoryWindow {
		t.Fatal("reload limit unbounded")
	}
}

func TestNewerWindowKeepsNavigationContinuous(t *testing.T) {
	m := composerTestModel()
	m.messagesByChat["a"] = make([]store.Message, maxHistoryWindow)
	for i := range m.messagesByChat["a"] {
		m.messagesByChat["a"][i] = store.Message{ID: fmt.Sprint(i)}
	}
	m.historyDetached["a"] = true
	m = m.handleNewerWindow(newerWindowLoadedMsg{ChatID: "a", AnchorID: fmt.Sprint(maxHistoryWindow - 1), Messages: []store.Message{{ID: "new"}}})
	if len(m.messagesByChat["a"]) != maxHistoryWindow || m.currentMessages()[m.messageCursor].ID != "new" {
		t.Fatal("newer page lost cursor or exceeded cap")
	}
}
