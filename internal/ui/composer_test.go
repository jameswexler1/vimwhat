package ui

import (
	"errors"
	tea "github.com/charmbracelet/bubbletea"
	"testing"
	"time"
	"vimwhat/internal/store"
)

func composerTestModel() Model {
	return NewModel(Options{Snapshot: store.Snapshot{
		Chats: []store.Chat{{ID: "a", Title: "Alice"}, {ID: "b", Title: "Bob"}}, ActiveChatID: "a",
		MessagesByChat: map[string][]store.Message{}, DraftsByChat: map[string]string{},
	}})
}

func TestComposerFlushIncludesUnescapedTextAndAttachments(t *testing.T) {
	m := composerTestModel()
	m.mode = ModeInsert
	m.attachments = []Attachment{{LocalPath: "/tmp/image.png", FileName: "image.png"}}
	m.replyTo = &store.Message{ID: "quoted", Body: "reply"}
	var saved store.ComposerDraft
	m.saveComposerDraft = func(_ string, draft store.ComposerDraft) error { saved = draft; return nil }
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("unfinished")})
	m = next.(Model)
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	if saved.Body != "unfinished" || len(saved.Media) != 1 || saved.Reply.ID != "quoted" {
		t.Fatalf("saved %+v", saved)
	}
	if cmd := m.captureComposerDraft(); cmd != nil {
		t.Fatal("unchanged draft repeatedly scheduled")
	}
}

func TestComposerCoordinatorDiscardsStaleSaves(t *testing.T) {
	d := &draftCoordinator{latest: map[string]draftTicket{}}
	old := d.reserve("a", store.ComposerDraft{Body: "old"})
	newer := d.reserve("a", store.ComposerDraft{Body: "new"})
	var saved string
	save := func(_ string, draft store.ComposerDraft) error { saved = draft.Body; return nil }
	if err := d.save(newer, save); err != nil {
		t.Fatal(err)
	}
	if err := d.save(old, save); err != nil {
		t.Fatal(err)
	}
	if saved != "new" {
		t.Fatalf("saved %q", saved)
	}
}

func TestComposerReservationDoesNotWaitForDiskWrite(t *testing.T) {
	d := &draftCoordinator{latest: map[string]draftTicket{}}
	first := d.reserve("a", store.ComposerDraft{Body: "old"})
	started, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		_ = d.save(first, func(string, store.ComposerDraft) error { close(started); <-release; return nil })
	}()
	<-started
	reserved := make(chan struct{})
	go func() { d.reserve("a", store.ComposerDraft{Body: "new"}); close(reserved) }()
	select {
	case <-reserved:
	case <-time.After(time.Second):
		close(release)
		<-done
		t.Fatal("typing blocked behind draft IO")
	}
	close(release)
	<-done
	var saved string
	if err := d.flush(func(_ string, draft store.ComposerDraft) error { saved = draft.Body; return nil }); err != nil {
		t.Fatal(err)
	}
	if saved != "new" {
		t.Fatalf("final save=%q", saved)
	}
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
