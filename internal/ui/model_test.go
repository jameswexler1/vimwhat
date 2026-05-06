package ui

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"vimwhat/internal/config"
	"vimwhat/internal/media"
	"vimwhat/internal/store"
)

type rawStringMsg string

func (m rawStringMsg) String() string {
	return string(m)
}

func runImmediateCmd(t *testing.T, model Model, cmd tea.Cmd) Model {
	t.Helper()
	if cmd == nil {
		return model
	}
	msg, ok := immediateCmdMsg(cmd)
	if !ok {
		return model
	}
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, nextCmd := range batch {
			model = runImmediateCmd(t, model, nextCmd)
		}
		return model
	}
	updated, nextCmd := model.Update(msg)
	next, ok := updated.(Model)
	if !ok {
		t.Fatalf("updated model type = %T, want Model", updated)
	}
	return runImmediateCmd(t, next, nextCmd)
}

func immediateCmdMsg(cmd tea.Cmd) (tea.Msg, bool) {
	done := make(chan tea.Msg, 1)
	go func() {
		done <- cmd()
	}()
	select {
	case msg := <-done:
		return msg, true
	case <-time.After(100 * time.Millisecond):
		return nil, false
	}
}

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

func TestInsertBackspaceRemovesEmojiCluster(t *testing.T) {
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats:          []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{"chat-1": nil},
			DraftsByChat:   map[string]string{},
			ActiveChatID:   "chat-1",
		},
	})
	model.mode = ModeInsert
	model.composer = "ok 👩🏻‍⚕️"

	updated, _ := model.updateInsert(tea.KeyMsg{Type: tea.KeyBackspace})
	got := updated.(Model).composer
	if got != "ok " {
		t.Fatalf("composer = %q, want %q", got, "ok ")
	}
}

func TestInsertMentionAutocompleteSelectsAndSendsMention(t *testing.T) {
	var (
		persisted bool
		outgoing  OutgoingMessage
	)
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats:          []store.Chat{{ID: "group@g.us", Title: "Group", Kind: "group"}},
			MessagesByChat: map[string][]store.Message{"group@g.us": nil},
			DraftsByChat:   map[string]string{},
			ActiveChatID:   "group@g.us",
		},
		SearchMentionCandidates: func(chatID, query string, limit int) ([]store.MentionCandidate, error) {
			if chatID != "group@g.us" {
				t.Fatalf("mention chatID = %q, want group@g.us", chatID)
			}
			return []store.MentionCandidate{{JID: "111@s.whatsapp.net", DisplayName: "José Silva"}}, nil
		},
		PersistMessage: func(message OutgoingMessage) (store.Message, error) {
			persisted = true
			outgoing = message
			return store.Message{
				ID:         "sent-1",
				ChatID:     message.ChatID,
				Sender:     "me",
				Body:       message.Body,
				IsOutgoing: true,
				Timestamp:  time.Unix(1, 0),
				Mentions:   slices.Clone(message.Mentions),
			}, nil
		},
	})
	model.mode = ModeInsert

	typedAt, cmd := model.updateInsert(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("@")})
	model = typedAt.(Model)
	model = runImmediateCmd(t, model, cmd)
	if !model.mentionActive || len(model.mentionCandidates) != 1 {
		t.Fatalf("mention state after @ = active %v candidates %+v", model.mentionActive, model.mentionCandidates)
	}
	selected, _ := model.updateInsert(tea.KeyMsg{Type: tea.KeyEnter})
	model = selected.(Model)
	if persisted {
		t.Fatal("Enter while mention popup is active sent the message")
	}
	if model.composer != "@José Silva " || model.mentionActive {
		t.Fatalf("composer after mention select = %q active=%v", model.composer, model.mentionActive)
	}
	model.composer += "hello"

	sent, cmd := model.updateInsert(tea.KeyMsg{Type: tea.KeyEnter})
	model = sent.(Model)
	model = runImmediateCmd(t, model, cmd)
	if !persisted {
		t.Fatal("message was not persisted")
	}
	if outgoing.Body != "@José Silva hello" || len(outgoing.Mentions) != 1 || outgoing.Mentions[0].JID != "111@s.whatsapp.net" {
		t.Fatalf("outgoing = %+v, want body with one mention", outgoing)
	}
	if len(model.messagesByChat["group@g.us"]) != 1 || len(model.messagesByChat["group@g.us"][0].Mentions) != 1 {
		t.Fatalf("stored UI message mentions = %+v", model.messagesByChat["group@g.us"])
	}
}

func TestInsertMentionAutocompleteOnlyStartsInGroups(t *testing.T) {
	called := false
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats:          []store.Chat{{ID: "chat-1", Title: "Alice", Kind: "direct"}},
			MessagesByChat: map[string][]store.Message{"chat-1": nil},
			DraftsByChat:   map[string]string{},
			ActiveChatID:   "chat-1",
		},
		SearchMentionCandidates: func(chatID, query string, limit int) ([]store.MentionCandidate, error) {
			called = true
			return nil, nil
		},
	})
	model.mode = ModeInsert

	updated, _ := model.updateInsert(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("@")})
	got := updated.(Model)
	if got.mentionActive || called || got.composer != "@" {
		t.Fatalf("direct chat mention state = active %v called %v composer %q", got.mentionActive, called, got.composer)
	}
}

func TestNewModelReportsInitialActiveChat(t *testing.T) {
	var reported []string
	NewModel(Options{
		Snapshot: store.Snapshot{
			Chats: []store.Chat{
				{ID: "chat-1", Title: "Alice"},
				{ID: "chat-2", Title: "Bob"},
			},
		},
		ActiveChatChanged: func(chatID string) {
			reported = append(reported, chatID)
		},
	})

	if len(reported) != 1 || reported[0] != "chat-1" {
		t.Fatalf("reported = %#v, want [chat-1]", reported)
	}
}

func TestMoveCursorReportsActiveChatChange(t *testing.T) {
	var reported []string
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats: []store.Chat{
				{ID: "chat-1", Title: "Alice"},
				{ID: "chat-2", Title: "Bob"},
			},
		},
		ActiveChatChanged: func(chatID string) {
			reported = append(reported, chatID)
		},
	})

	reported = nil
	model.moveCursor(1)

	if len(reported) != 1 || reported[0] != "chat-2" {
		t.Fatalf("reported = %#v, want [chat-2]", reported)
	}
}

func TestUnreadFilterReportsActiveChatChange(t *testing.T) {
	var reported []string
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats: []store.Chat{
				{ID: "chat-1", Title: "Alice"},
				{ID: "chat-2", Title: "Bob", Unread: 1},
			},
		},
		ActiveChatChanged: func(chatID string) {
			reported = append(reported, chatID)
		},
	})

	reported = nil
	if err := model.setUnreadOnly(true); err != nil {
		t.Fatalf("setUnreadOnly() error = %v", err)
	}

	if len(reported) != 1 || reported[0] != "chat-2" {
		t.Fatalf("reported = %#v, want [chat-2]", reported)
	}
}

func TestApplySnapshotReportsChangedActiveChat(t *testing.T) {
	var reported []string
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats: []store.Chat{{ID: "chat-1", Title: "Alice"}},
		},
		ActiveChatChanged: func(chatID string) {
			reported = append(reported, chatID)
		},
	})

	reported = nil
	if err := model.applySnapshot(store.Snapshot{
		Chats: []store.Chat{{ID: "chat-3", Title: "Carol"}},
	}, "chat-3", ""); err != nil {
		t.Fatalf("applySnapshot() error = %v", err)
	}

	if len(reported) != 1 || reported[0] != "chat-3" {
		t.Fatalf("reported = %#v, want [chat-3]", reported)
	}
}

func TestFocusMsgReportsAppFocusChanged(t *testing.T) {
	var reported []bool
	model := NewModel(Options{
		AppFocusChanged: func(focused bool) {
			reported = append(reported, focused)
		},
	})

	updated, _ := model.Update(tea.FocusMsg{})
	got := updated.(Model)

	if len(reported) != 1 || !reported[0] {
		t.Fatalf("reported = %#v, want [true]", reported)
	}
	if !got.appFocusKnown || !got.appFocused {
		t.Fatalf("focus state = known:%v focused:%v, want known:true focused:true", got.appFocusKnown, got.appFocused)
	}
}

func TestBlurMsgReportsAppFocusChanged(t *testing.T) {
	var reported []bool
	model := NewModel(Options{
		AppFocusChanged: func(focused bool) {
			reported = append(reported, focused)
		},
	})

	updated, _ := model.Update(tea.BlurMsg{})
	got := updated.(Model)

	if len(reported) != 1 || reported[0] {
		t.Fatalf("reported = %#v, want [false]", reported)
	}
	if !got.appFocusKnown || got.appFocused {
		t.Fatalf("focus state = known:%v focused:%v, want known:true focused:false", got.appFocusKnown, got.appFocused)
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

	updated, cmd := model.updateInsert(tea.KeyMsg{Type: tea.KeyEsc})
	got := updated.(Model)
	got = runImmediateCmd(t, got, cmd)
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
		PersistMessage: func(outgoing OutgoingMessage) (store.Message, error) {
			return store.Message{ID: "local-1", ChatID: outgoing.ChatID, Sender: "me", Body: outgoing.Body, IsOutgoing: true}, nil
		},
		SaveDraft: func(chatID, body string) error {
			cleared = chatID == "chat-1" && body == ""
			return nil
		},
	})
	model.mode = ModeInsert
	model.composer = "send this"

	updated, cmd := model.updateInsert(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	got = runImmediateCmd(t, got, cmd)
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

func TestInsertSendDefersPersistUntilCommand(t *testing.T) {
	var called bool
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats:          []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{"chat-1": nil},
			DraftsByChat:   map[string]string{},
			ActiveChatID:   "chat-1",
		},
		PersistMessage: func(outgoing OutgoingMessage) (store.Message, error) {
			called = true
			return store.Message{ID: "persisted-1", ChatID: outgoing.ChatID, Sender: "me", Body: outgoing.Body, IsOutgoing: true}, nil
		},
	})
	model.mode = ModeInsert
	model.composer = "send later"

	updated, cmd := model.updateInsert(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if called {
		t.Fatal("PersistMessage ran before returned command was executed")
	}
	if cmd == nil {
		t.Fatal("send command = nil, want async persist command")
	}
	if len(got.messagesByChat["chat-1"]) != 1 || got.messagesByChat["chat-1"][0].Body != "send later" {
		t.Fatalf("optimistic messages = %+v", got.messagesByChat["chat-1"])
	}
	got = runImmediateCmd(t, got, cmd)
	if !called {
		t.Fatal("PersistMessage did not run after command execution")
	}
	if got.messagesByChat["chat-1"][0].ID != "persisted-1" {
		t.Fatalf("persisted message = %+v", got.messagesByChat["chat-1"][0])
	}
}

func TestReplyKeyStartsInsertAndSendCarriesQuote(t *testing.T) {
	var sent OutgoingMessage
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats: []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{
				"chat-1": {{
					ID:        "m-1",
					RemoteID:  "remote-1",
					ChatID:    "chat-1",
					Sender:    "Alice",
					SenderJID: "alice@s.whatsapp.net",
					Body:      "original",
				}},
			},
			DraftsByChat: map[string]string{},
			ActiveChatID: "chat-1",
		},
		PersistMessage: func(outgoing OutgoingMessage) (store.Message, error) {
			sent = outgoing
			return store.Message{
				ID:              "local-1",
				ChatID:          outgoing.ChatID,
				Sender:          "me",
				Body:            outgoing.Body,
				IsOutgoing:      true,
				QuotedMessageID: outgoing.Quote.ID,
				QuotedRemoteID:  outgoing.Quote.RemoteID,
			}, nil
		},
		SaveDraft: func(chatID, body string) error { return nil },
	})
	model.focus = FocusMessages

	updated, _ := model.updateNormal(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	replying := updated.(Model)
	if replying.mode != ModeInsert || replying.replyTo == nil || replying.replyTo.ID != "m-1" {
		t.Fatalf("reply state = mode %s reply %+v", replying.mode, replying.replyTo)
	}
	replying.composer = "reply body"
	sentModel, cmd := replying.updateInsert(tea.KeyMsg{Type: tea.KeyEnter})
	got := runImmediateCmd(t, sentModel.(Model), cmd)
	if sent.Quote == nil || sent.Quote.ID != "m-1" || sent.Quote.RemoteID != "remote-1" {
		t.Fatalf("sent quote = %+v", sent.Quote)
	}
	if len(got.messagesByChat["chat-1"]) != 2 || got.messagesByChat["chat-1"][1].QuotedRemoteID != "remote-1" {
		t.Fatalf("messages after reply = %+v", got.messagesByChat["chat-1"])
	}
}

func TestRightEdgeReplyStartsInsertWhenInfoPaneHidden(t *testing.T) {
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats: []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{
				"chat-1": {{
					ID:        "m-1",
					RemoteID:  "remote-1",
					ChatID:    "chat-1",
					Sender:    "Alice",
					SenderJID: "alice@s.whatsapp.net",
					Body:      "original",
				}},
			},
			DraftsByChat: map[string]string{},
			ActiveChatID: "chat-1",
		},
	})
	model.focus = FocusMessages

	updated, _ := model.updateNormal(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	replying := updated.(Model)
	if replying.mode != ModeInsert || replying.replyTo == nil || replying.replyTo.ID != "m-1" {
		t.Fatalf("reply state = mode %s reply %+v", replying.mode, replying.replyTo)
	}
}

func TestRightEdgeReplyStartsInsertInCompactLayout(t *testing.T) {
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats: []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{
				"chat-1": {{
					ID:        "m-1",
					RemoteID:  "remote-1",
					ChatID:    "chat-1",
					Sender:    "Alice",
					SenderJID: "alice@s.whatsapp.net",
					Body:      "original",
				}},
			},
			DraftsByChat: map[string]string{},
			ActiveChatID: "chat-1",
		},
	})
	model.focus = FocusMessages
	model.compactLayout = true
	model.infoPaneVisible = true

	updated, _ := model.updateNormal(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	replying := updated.(Model)
	if replying.mode != ModeInsert || replying.replyTo == nil || replying.replyTo.ID != "m-1" {
		t.Fatalf("reply state = mode %s reply %+v", replying.mode, replying.replyTo)
	}
}

func TestLMovesFocusToPreviewWhenInfoPaneVisible(t *testing.T) {
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats: []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{
				"chat-1": {{
					ID:        "m-1",
					RemoteID:  "remote-1",
					ChatID:    "chat-1",
					Sender:    "Alice",
					SenderJID: "alice@s.whatsapp.net",
					Body:      "original",
				}},
			},
			DraftsByChat: map[string]string{},
			ActiveChatID: "chat-1",
		},
	})
	model.focus = FocusMessages
	model.infoPaneVisible = true

	updated, _ := model.updateNormal(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	got := updated.(Model)
	if got.focus != FocusPreview {
		t.Fatalf("focus = %s, want %s", got.focus, FocusPreview)
	}
	if got.mode != ModeNormal || got.replyTo != nil {
		t.Fatalf("mode = %s reply = %+v, want normal mode without reply", got.mode, got.replyTo)
	}
}

func TestReactCommandUsesFocusedMessage(t *testing.T) {
	var reactedMessage store.Message
	var reactedEmoji string
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats: []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{
				"chat-1": {{
					ID:        "m-1",
					RemoteID:  "remote-1",
					ChatID:    "chat-1",
					ChatJID:   "chat-1",
					Sender:    "Alice",
					SenderJID: "alice@s.whatsapp.net",
					Body:      "hello",
				}},
			},
			DraftsByChat: map[string]string{},
			ActiveChatID: "chat-1",
		},
		ConnectionState: ConnectionOnline,
		SendReaction: func(message store.Message, emoji string) error {
			reactedMessage = message
			reactedEmoji = emoji
			return nil
		},
	})
	model.focus = FocusMessages

	updated, cmd := model.executeCommand("react 🔥")
	got := runImmediateCmd(t, updated.(Model), cmd)
	if reactedMessage.ID != "m-1" || reactedEmoji != "🔥" {
		t.Fatalf("reaction callback = message %+v emoji %q", reactedMessage, reactedEmoji)
	}
	if !strings.Contains(got.status, "reaction queued") {
		t.Fatalf("status = %q, want reaction queued", got.status)
	}
}

func TestLiveModeBlocksSendAndPreservesDraft(t *testing.T) {
	var persisted bool
	var savedChatID string
	var savedBody string
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats:          []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{"chat-1": nil},
			DraftsByChat:   map[string]string{},
			ActiveChatID:   "chat-1",
		},
		BlockSending: true,
		PersistMessage: func(OutgoingMessage) (store.Message, error) {
			persisted = true
			return store.Message{}, nil
		},
		SaveDraft: func(chatID, body string) error {
			savedChatID = chatID
			savedBody = body
			return nil
		},
	})
	model.mode = ModeInsert
	model.composer = "not yet"

	updated, cmd := model.updateInsert(tea.KeyMsg{Type: tea.KeyEnter})
	got := runImmediateCmd(t, updated.(Model), cmd)
	if persisted {
		t.Fatal("PersistMessage was called in live read-only mode")
	}
	if savedChatID != "chat-1" || savedBody != "not yet" {
		t.Fatalf("saved draft = (%q, %q), want (chat-1, not yet)", savedChatID, savedBody)
	}
	if got.composer != "not yet" || len(got.messagesByChat["chat-1"]) != 0 {
		t.Fatalf("composer/messages = %q/%d, want preserved composer and no message", got.composer, len(got.messagesByChat["chat-1"]))
	}
	if !strings.Contains(got.status, "not implemented") {
		t.Fatalf("status = %q, want not implemented", got.status)
	}
}

func TestLiveAttachmentSendQueuesAndClearsComposerState(t *testing.T) {
	attachmentPath := filepath.Join(t.TempDir(), "photo.jpg")
	if err := os.WriteFile(attachmentPath, []byte("image-bytes"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	var sent OutgoingMessage
	var cleared bool
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats:          []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{"chat-1": nil},
			DraftsByChat:   map[string]string{},
			ActiveChatID:   "chat-1",
		},
		ConnectionState:      ConnectionOnline,
		RequireOnlineForSend: true,
		PersistMessage: func(outgoing OutgoingMessage) (store.Message, error) {
			sent = outgoing
			return store.Message{
				ID:         "local-1",
				ChatID:     outgoing.ChatID,
				Sender:     "me",
				Body:       outgoing.Body,
				IsOutgoing: true,
				Status:     "sending",
				Media: []store.MediaMetadata{{
					MessageID:     "local-1",
					LocalPath:     outgoing.Attachments[0].LocalPath,
					FileName:      outgoing.Attachments[0].FileName,
					MIMEType:      outgoing.Attachments[0].MIMEType,
					SizeBytes:     outgoing.Attachments[0].SizeBytes,
					DownloadState: "downloaded",
				}},
			}, nil
		},
		SaveDraft: func(chatID, body string) error {
			cleared = chatID == "chat-1" && body == ""
			return nil
		},
	})
	model.mode = ModeInsert
	model.composer = "caption"
	model.attachments = []Attachment{{
		LocalPath:     attachmentPath,
		FileName:      "photo.jpg",
		MIMEType:      "image/jpeg",
		SizeBytes:     2048,
		DownloadState: "local_pending",
	}}

	updated, cmd := model.updateInsert(tea.KeyMsg{Type: tea.KeyEnter})
	got := runImmediateCmd(t, updated.(Model), cmd)
	if !cleared {
		t.Fatal("draft was not cleared after attachment send")
	}
	if sent.Body != "caption" || len(sent.Attachments) != 1 || sent.Attachments[0].LocalPath != attachmentPath {
		t.Fatalf("sent outgoing = %+v, want caption plus attachment", sent)
	}
	if got.composer != "" || len(got.attachments) != 0 {
		t.Fatalf("composer/attachments = %q/%+v, want cleared after queue", got.composer, got.attachments)
	}
	if len(got.messagesByChat["chat-1"]) != 1 || len(got.messagesByChat["chat-1"][0].Media) != 1 {
		t.Fatalf("messages after attachment send = %+v", got.messagesByChat["chat-1"])
	}
	if !strings.Contains(got.status, "queued") {
		t.Fatalf("status = %q, want queued status", got.status)
	}
}

func TestAudioAttachmentCaptionValidationPreventsSend(t *testing.T) {
	attachmentPath := filepath.Join(t.TempDir(), "voice.ogg")
	if err := os.WriteFile(attachmentPath, []byte("audio-bytes"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	var persisted bool
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats:          []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{"chat-1": nil},
			DraftsByChat:   map[string]string{},
			ActiveChatID:   "chat-1",
		},
		ConnectionState:      ConnectionOnline,
		RequireOnlineForSend: true,
		PersistMessage: func(OutgoingMessage) (store.Message, error) {
			persisted = true
			return store.Message{}, nil
		},
	})
	model.mode = ModeInsert
	model.composer = "caption"
	model.attachments = []Attachment{{
		LocalPath: attachmentPath,
		FileName:  "voice.ogg",
		MIMEType:  "audio/ogg",
	}}

	updated, cmd := model.updateInsert(tea.KeyMsg{Type: tea.KeyEnter})
	got := runImmediateCmd(t, updated.(Model), cmd)
	if persisted {
		t.Fatal("PersistMessage was called for invalid audio caption send")
	}
	if got.composer != "caption" || len(got.attachments) != 1 {
		t.Fatalf("composer/attachments = %q/%+v, want preserved", got.composer, got.attachments)
	}
	if !strings.Contains(got.status, "do not support captions") {
		t.Fatalf("status = %q, want audio caption validation error", got.status)
	}
}

func TestMissingAttachmentValidationPreventsSend(t *testing.T) {
	var persisted bool
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats:          []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{"chat-1": nil},
			DraftsByChat:   map[string]string{},
			ActiveChatID:   "chat-1",
		},
		ConnectionState:      ConnectionOnline,
		RequireOnlineForSend: true,
		PersistMessage: func(OutgoingMessage) (store.Message, error) {
			persisted = true
			return store.Message{}, nil
		},
	})
	model.mode = ModeInsert
	model.composer = "caption"
	model.attachments = []Attachment{{LocalPath: filepath.Join(t.TempDir(), "missing.pdf"), FileName: "missing.pdf", MIMEType: "application/pdf"}}

	updated, cmd := model.updateInsert(tea.KeyMsg{Type: tea.KeyEnter})
	got := runImmediateCmd(t, updated.(Model), cmd)
	if persisted {
		t.Fatal("PersistMessage was called for missing attachment")
	}
	if !strings.Contains(got.status, "attachment file is missing") {
		t.Fatalf("status = %q, want missing attachment error", got.status)
	}
}

func TestRetryFocusedFailedMediaQueuesNewMessage(t *testing.T) {
	attachmentPath := filepath.Join(t.TempDir(), "report.pdf")
	if err := os.WriteFile(attachmentPath, []byte("pdf-bytes"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	var retried store.Message
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats: []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{
				"chat-1": {{
					ID:         "failed-1",
					ChatID:     "chat-1",
					Sender:     "me",
					Body:       "caption",
					IsOutgoing: true,
					Status:     "failed",
					Media: []store.MediaMetadata{{
						MessageID:     "failed-1",
						FileName:      "report.pdf",
						MIMEType:      "application/pdf",
						LocalPath:     attachmentPath,
						DownloadState: "downloaded",
					}},
				}},
			},
			DraftsByChat: map[string]string{},
			ActiveChatID: "chat-1",
		},
		ConnectionState:      ConnectionOnline,
		RequireOnlineForSend: true,
		RetryMessage: func(message store.Message) (store.Message, error) {
			retried = message
			return store.Message{
				ID:         "retry-1",
				ChatID:     message.ChatID,
				Sender:     "me",
				Body:       message.Body,
				IsOutgoing: true,
				Status:     "sending",
				Media: []store.MediaMetadata{{
					MessageID:     "retry-1",
					FileName:      message.Media[0].FileName,
					MIMEType:      message.Media[0].MIMEType,
					LocalPath:     message.Media[0].LocalPath,
					DownloadState: "downloaded",
				}},
			}, nil
		},
	})
	model.focus = FocusMessages
	model.composer = "draft stays"

	updated, cmd := model.updateNormal(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("R")})
	got := runImmediateCmd(t, updated.(Model), cmd)
	if retried.ID != "failed-1" {
		t.Fatalf("retry callback received %+v, want failed message", retried)
	}
	if got.composer != "draft stays" {
		t.Fatalf("composer = %q, want unchanged", got.composer)
	}
	if len(got.messagesByChat["chat-1"]) != 2 {
		t.Fatalf("messages = %+v, want original plus retry row", got.messagesByChat["chat-1"])
	}
	if got.messagesByChat["chat-1"][0].ID != "failed-1" || got.messagesByChat["chat-1"][1].ID != "retry-1" {
		t.Fatalf("message ids after retry = %+v, want failed-1 then retry-1", got.messagesByChat["chat-1"])
	}
	if got.status != "retry queued" {
		t.Fatalf("status = %q, want retry queued", got.status)
	}
}

func TestRetryMessageCommandUsesFocusedFailedMedia(t *testing.T) {
	attachmentPath := filepath.Join(t.TempDir(), "report.pdf")
	if err := os.WriteFile(attachmentPath, []byte("pdf-bytes"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	var called bool
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats: []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{
				"chat-1": {{
					ID:         "failed-1",
					ChatID:     "chat-1",
					Sender:     "me",
					Body:       "caption",
					IsOutgoing: true,
					Status:     "failed",
					Media: []store.MediaMetadata{{
						MessageID: "failed-1",
						FileName:  "report.pdf",
						MIMEType:  "application/pdf",
						LocalPath: attachmentPath,
					}},
				}},
			},
			DraftsByChat: map[string]string{},
			ActiveChatID: "chat-1",
		},
		ConnectionState:      ConnectionOnline,
		RequireOnlineForSend: true,
		RetryMessage: func(message store.Message) (store.Message, error) {
			called = true
			return store.Message{
				ID:         "retry-1",
				ChatID:     message.ChatID,
				Sender:     "me",
				Body:       message.Body,
				IsOutgoing: true,
				Status:     "sending",
				Media:      message.Media,
			}, nil
		},
	})
	model.focus = FocusMessages

	updated, cmd := model.executeCommand("retry-message")
	got := runImmediateCmd(t, updated.(Model), cmd)
	if !called {
		t.Fatal("RetryMessage was not called")
	}
	if got.status != "retry queued" {
		t.Fatalf("status = %q, want retry queued", got.status)
	}
}

func TestRetryFocusedMediaRejectsInvalidSelection(t *testing.T) {
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats: []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{
				"chat-1": {{
					ID:     "m-1",
					ChatID: "chat-1",
					Sender: "Alice",
					Body:   "hello",
				}},
			},
			DraftsByChat: map[string]string{},
			ActiveChatID: "chat-1",
		},
		ConnectionState:      ConnectionOnline,
		RequireOnlineForSend: true,
	})
	model.focus = FocusMessages

	updated, _ := model.updateNormal(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("R")})
	got := updated.(Model)
	if !strings.Contains(got.status, "outgoing") {
		t.Fatalf("status = %q, want retry validation error", got.status)
	}
}

func TestRequireOnlineForSendPreservesDraft(t *testing.T) {
	var persisted bool
	var savedChatID string
	var savedBody string
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats:          []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{"chat-1": nil},
			DraftsByChat:   map[string]string{},
			ActiveChatID:   "chat-1",
		},
		ConnectionState:      ConnectionConnecting,
		RequireOnlineForSend: true,
		PersistMessage: func(OutgoingMessage) (store.Message, error) {
			persisted = true
			return store.Message{}, nil
		},
		SaveDraft: func(chatID, body string) error {
			savedChatID = chatID
			savedBody = body
			return nil
		},
	})
	model.mode = ModeInsert
	model.composer = "wait for online"

	updated, cmd := model.updateInsert(tea.KeyMsg{Type: tea.KeyEnter})
	got := runImmediateCmd(t, updated.(Model), cmd)
	if persisted {
		t.Fatal("PersistMessage was called while WhatsApp was offline")
	}
	if savedChatID != "chat-1" || savedBody != "wait for online" {
		t.Fatalf("saved draft = (%q, %q), want offline draft", savedChatID, savedBody)
	}
	if got.composer != "wait for online" || len(got.messagesByChat["chat-1"]) != 0 {
		t.Fatalf("composer/messages = %q/%d, want preserved composer and no message", got.composer, len(got.messagesByChat["chat-1"]))
	}
	if !strings.Contains(got.status, "online") {
		t.Fatalf("status = %q, want online requirement", got.status)
	}
}

func TestPersistSendErrorSavesDraft(t *testing.T) {
	var savedChatID string
	var savedBody string
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats:          []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{"chat-1": nil},
			DraftsByChat:   map[string]string{},
			ActiveChatID:   "chat-1",
		},
		PersistMessage: func(OutgoingMessage) (store.Message, error) {
			return store.Message{}, fmt.Errorf("boom")
		},
		SaveDraft: func(chatID, body string) error {
			savedChatID = chatID
			savedBody = body
			return nil
		},
	})
	model.mode = ModeInsert
	model.composer = "do not drop me"

	updated, cmd := model.updateInsert(tea.KeyMsg{Type: tea.KeyEnter})
	got := runImmediateCmd(t, updated.(Model), cmd)
	if savedChatID != "chat-1" || savedBody != "do not drop me" {
		t.Fatalf("saved draft = (%q, %q), want failed body", savedChatID, savedBody)
	}
	if got.composer != "do not drop me" || len(got.messagesByChat["chat-1"]) != 0 {
		t.Fatalf("composer/messages = %q/%d, want preserved composer and no message", got.composer, len(got.messagesByChat["chat-1"]))
	}
	if !strings.Contains(got.status, "send failed") {
		t.Fatalf("status = %q, want send failed", got.status)
	}
}

func TestSnapshotReloadRestoresFailedSendDraftWhenComposerEmpty(t *testing.T) {
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats:          []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{"chat-1": nil},
			DraftsByChat:   map[string]string{},
			ActiveChatID:   "chat-1",
		},
	})
	model.mode = ModeInsert

	if err := model.applySnapshot(store.Snapshot{
		Chats:          []store.Chat{{ID: "chat-1", Title: "Alice", HasDraft: true}},
		MessagesByChat: map[string][]store.Message{"chat-1": nil},
		DraftsByChat:   map[string]string{"chat-1": "retry me"},
		ActiveChatID:   "chat-1",
	}, "chat-1", ""); err != nil {
		t.Fatalf("applySnapshot() error = %v", err)
	}
	if model.composer != "retry me" {
		t.Fatalf("composer = %q, want restored failed send draft", model.composer)
	}

	model.composer = "new text"
	if err := model.applySnapshot(store.Snapshot{
		Chats:          []store.Chat{{ID: "chat-1", Title: "Alice", HasDraft: true}},
		MessagesByChat: map[string][]store.Message{"chat-1": nil},
		DraftsByChat:   map[string]string{"chat-1": "old failed text"},
		ActiveChatID:   "chat-1",
	}, "chat-1", ""); err != nil {
		t.Fatalf("applySnapshot(second) error = %v", err)
	}
	if model.composer != "new text" {
		t.Fatalf("composer = %q, want newly typed text preserved", model.composer)
	}
}

func TestFailedMessageStatusUsesFailureMarker(t *testing.T) {
	if got := messageStatusTicks("failed"); got != "!" {
		t.Fatalf("messageStatusTicks(failed) = %q, want !", got)
	}
}

func TestLiveUpdateRefreshesSnapshotAndStatusLine(t *testing.T) {
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats:          []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{"chat-1": nil},
			DraftsByChat:   map[string]string{},
			ActiveChatID:   "chat-1",
		},
		ConnectionState: ConnectionPaired,
		ReloadSnapshot: func(activeChatID string, limit int) (store.Snapshot, error) {
			if activeChatID != "chat-1" || limit != messageLoadLimit {
				t.Fatalf("reload args = (%q, %d), want (chat-1, %d)", activeChatID, limit, messageLoadLimit)
			}
			return store.Snapshot{
				Chats: []store.Chat{{ID: "chat-1", Title: "Alice", Unread: 1}},
				MessagesByChat: map[string][]store.Message{
					"chat-1": {{ID: "msg-1", ChatID: "chat-1", Sender: "Alice", Body: "live"}},
				},
				DraftsByChat: map[string]string{},
				ActiveChatID: "chat-1",
			}, nil
		},
	})
	model.width = 120
	model.height = 24

	updated, _ := model.handleLiveUpdate(LiveUpdate{ConnectionState: ConnectionOnline, Refresh: true})
	if updated.connectionState != ConnectionOnline || !updated.refreshDebouncePending || updated.reloadInFlight {
		t.Fatalf("state/debounce/reload = %q/%v/%v, want online/pending/not inflight", updated.connectionState, updated.refreshDebouncePending, updated.reloadInFlight)
	}
	debounced, cmd := updated.handleRefreshDebounced()
	if !debounced.reloadInFlight || cmd == nil {
		t.Fatalf("debounced reload = %v cmd nil=%v, want inflight command", debounced.reloadInFlight, cmd == nil)
	}
	reloadMsg := cmd().(snapshotReloadedMsg)
	refreshed, _ := debounced.handleSnapshotReloaded(reloadMsg)
	if len(refreshed.messagesByChat["chat-1"]) != 1 || refreshed.messagesByChat["chat-1"][0].Body != "live" {
		t.Fatalf("messages after refresh = %+v", refreshed.messagesByChat["chat-1"])
	}
	if status := stripANSI(refreshed.renderStatus()); !strings.Contains(status, "WA:ONLINE") {
		t.Fatalf("status = %q, want WA:ONLINE", status)
	}
}

func TestSnapshotAppendScrollsWhenFocusedMessageWasTail(t *testing.T) {
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats: []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{"chat-1": {
				{ID: "m-1", ChatID: "chat-1", Body: "one"},
				{ID: "m-2", ChatID: "chat-1", Body: "two"},
			}},
			DraftsByChat: map[string]string{},
			ActiveChatID: "chat-1",
		},
	})
	model.focus = FocusMessages
	model.messageCursor = 1
	model.messageScrollTop = 1

	err := model.applySnapshot(store.Snapshot{
		Chats: []store.Chat{{ID: "chat-1", Title: "Alice"}},
		MessagesByChat: map[string][]store.Message{"chat-1": {
			{ID: "m-1", ChatID: "chat-1", Body: "one"},
			{ID: "m-2", ChatID: "chat-1", Body: "two"},
			{ID: "m-3", ChatID: "chat-1", Body: "three"},
		}},
		DraftsByChat: map[string]string{},
		ActiveChatID: "chat-1",
	}, "chat-1", "")
	if err != nil {
		t.Fatalf("applySnapshot() error = %v", err)
	}
	if model.messageCursor != 2 || model.messageScrollTop != 2 {
		t.Fatalf("cursor/scroll = %d/%d, want 2/2", model.messageCursor, model.messageScrollTop)
	}
	if model.hasNewMessagesBelow("chat-1") {
		t.Fatal("new-messages indicator remained set after auto-follow")
	}
}

func TestSnapshotAppendPreservesHistoryPositionAndShowsNewMessagesIndicator(t *testing.T) {
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats: []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{"chat-1": {
				{ID: "m-1", ChatID: "chat-1", Body: "one"},
				{ID: "m-2", ChatID: "chat-1", Body: "two"},
			}},
			DraftsByChat: map[string]string{},
			ActiveChatID: "chat-1",
		},
	})
	model.focus = FocusMessages
	model.messageCursor = 0
	model.messageScrollTop = 0

	err := model.applySnapshot(store.Snapshot{
		Chats: []store.Chat{{ID: "chat-1", Title: "Alice"}},
		MessagesByChat: map[string][]store.Message{"chat-1": {
			{ID: "m-1", ChatID: "chat-1", Body: "one"},
			{ID: "m-2", ChatID: "chat-1", Body: "two"},
			{ID: "m-3", ChatID: "chat-1", Body: "three"},
		}},
		DraftsByChat: map[string]string{},
		ActiveChatID: "chat-1",
	}, "chat-1", "")
	if err != nil {
		t.Fatalf("applySnapshot() error = %v", err)
	}
	if model.messageCursor != 0 || model.messageScrollTop != 0 {
		t.Fatalf("cursor/scroll = %d/%d, want 0/0", model.messageCursor, model.messageScrollTop)
	}
	if !model.hasNewMessagesBelow("chat-1") {
		t.Fatal("new-messages indicator was not set")
	}
	if footer := stripANSI(model.renderMessageFooter(60)); !strings.Contains(footer, "↓ 2 new below") {
		t.Fatalf("footer missing new-messages indicator:\n%s", footer)
	}
}

func TestNewMessageNoticeAndWrappedComposerStayWithinMessagePane(t *testing.T) {
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats: []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{"chat-1": {
				{ID: "m-1", ChatID: "chat-1", Body: strings.Repeat("older message ", 8)},
				{ID: "m-2", ChatID: "chat-1", Body: strings.Repeat("current message ", 8)},
			}},
			DraftsByChat: map[string]string{},
			ActiveChatID: "chat-1",
		},
	})
	model.focus = FocusMessages
	model.mode = ModeInsert
	model.messageCursor = 0
	model.messageScrollTop = 0
	model.composer = strings.Repeat("composer wraps ", 6)

	err := model.applySnapshot(store.Snapshot{
		Chats: []store.Chat{{ID: "chat-1", Title: "Alice"}},
		MessagesByChat: map[string][]store.Message{"chat-1": {
			{ID: "m-1", ChatID: "chat-1", Body: strings.Repeat("older message ", 8)},
			{ID: "m-2", ChatID: "chat-1", Body: strings.Repeat("current message ", 8)},
			{ID: "m-3", ChatID: "chat-1", Body: "new one"},
			{ID: "m-4", ChatID: "chat-1", Body: "new two"},
		}},
		DraftsByChat: map[string]string{},
		ActiveChatID: "chat-1",
	}, "chat-1", "")
	if err != nil {
		t.Fatalf("applySnapshot() error = %v", err)
	}

	const width = 48
	const height = 10
	view := stripANSI(model.renderMessages(width, height))
	lines := strings.Split(view, "\n")
	if len(lines) > height {
		t.Fatalf("renderMessages() produced %d lines, want <= %d\n%s", len(lines), height, view)
	}
	for i, line := range lines {
		if got := lipgloss.Width(line); got > width {
			t.Fatalf("line %d width = %d, want <= %d\n%s", i+1, got, width, view)
		}
	}
	if !strings.Contains(view, "↓ 3 new below") {
		t.Fatalf("renderMessages() missing new-message notice\n%s", view)
	}
	if len(renderedComposerBodyLines(view)) < 2 {
		t.Fatalf("renderMessages() did not wrap composer footer\n%s", view)
	}
}

func TestMovingToLatestMessageClearsNewMessagesIndicator(t *testing.T) {
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats: []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{"chat-1": {
				{ID: "m-1", ChatID: "chat-1", Body: "one"},
				{ID: "m-2", ChatID: "chat-1", Body: "two"},
				{ID: "m-3", ChatID: "chat-1", Body: "three"},
			}},
			DraftsByChat: map[string]string{},
			ActiveChatID: "chat-1",
		},
	})
	model.focus = FocusMessages
	model.messageCursor = 0
	model.messageScrollTop = 0
	model.recordNewMessages("chat-1", "m-2")

	model.moveCursor(2)
	if model.messageCursor != 2 {
		t.Fatalf("messageCursor = %d, want 2", model.messageCursor)
	}
	if model.hasNewMessagesBelow("chat-1") {
		t.Fatal("new-messages indicator remained set at latest message")
	}
}

func TestSnapshotPrependDoesNotSetNewMessagesIndicator(t *testing.T) {
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats: []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{"chat-1": {
				{ID: "m-3", ChatID: "chat-1", Body: "three"},
				{ID: "m-4", ChatID: "chat-1", Body: "four"},
			}},
			DraftsByChat: map[string]string{},
			ActiveChatID: "chat-1",
		},
	})
	model.focus = FocusMessages
	model.messageCursor = 0
	model.messageScrollTop = 0

	err := model.applySnapshot(store.Snapshot{
		Chats: []store.Chat{{ID: "chat-1", Title: "Alice"}},
		MessagesByChat: map[string][]store.Message{"chat-1": {
			{ID: "m-1", ChatID: "chat-1", Body: "one"},
			{ID: "m-2", ChatID: "chat-1", Body: "two"},
			{ID: "m-3", ChatID: "chat-1", Body: "three"},
			{ID: "m-4", ChatID: "chat-1", Body: "four"},
		}},
		DraftsByChat: map[string]string{},
		ActiveChatID: "chat-1",
	}, "chat-1", "")
	if err != nil {
		t.Fatalf("applySnapshot() error = %v", err)
	}
	if got := model.currentMessages()[model.messageCursor].ID; got != "m-3" {
		t.Fatalf("focused message = %q, want m-3", got)
	}
	if model.hasNewMessagesBelow("chat-1") {
		t.Fatal("history prepend set new-messages indicator")
	}
}

func TestRenderMessagesShowsNewMessagesDivider(t *testing.T) {
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats: []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{"chat-1": {
				{ID: "m-1", ChatID: "chat-1", Body: "one"},
				{ID: "m-2", ChatID: "chat-1", Body: "two"},
				{ID: "m-3", ChatID: "chat-1", Body: "three"},
				{ID: "m-4", ChatID: "chat-1", Body: "four"},
			}},
			DraftsByChat: map[string]string{},
			ActiveChatID: "chat-1",
		},
	})
	model.focus = FocusMessages
	model.messageCursor = 1
	model.messageScrollTop = 1
	model.recordNewMessages("chat-1", "m-2")

	view := stripANSI(model.renderMessages(70, 12))
	if !strings.Contains(view, "Mensagens novas: 2") {
		t.Fatalf("renderMessages() missing new message divider\n%s", view)
	}
	if !strings.Contains(view, "three") {
		t.Fatalf("renderMessages() did not keep a new message below the divider\n%s", view)
	}
}

func TestNewMessagesStatePersistsUntilConversationBottom(t *testing.T) {
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats: []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{"chat-1": {
				{ID: "m-1", ChatID: "chat-1", Body: "one"},
				{ID: "m-2", ChatID: "chat-1", Body: "two"},
				{ID: "m-3", ChatID: "chat-1", Body: "three"},
				{ID: "m-4", ChatID: "chat-1", Body: "four"},
			}},
			DraftsByChat: map[string]string{},
			ActiveChatID: "chat-1",
		},
	})
	model.focus = FocusMessages
	model.messageCursor = 1
	model.messageScrollTop = 1
	model.recordNewMessages("chat-1", "m-2")

	model.moveCursor(1)
	if !model.hasNewMessagesBelow("chat-1") {
		t.Fatal("new message state cleared before reaching latest message")
	}

	model.moveCursor(1)
	if model.hasNewMessagesBelow("chat-1") {
		t.Fatal("new message state remained after reaching latest message")
	}
}

func TestLiveUpdateRefreshesAreDebounced(t *testing.T) {
	reloads := 0
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats:          []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{"chat-1": nil},
			DraftsByChat:   map[string]string{},
			ActiveChatID:   "chat-1",
		},
		ReloadSnapshot: func(activeChatID string, limit int) (store.Snapshot, error) {
			reloads++
			return store.Snapshot{
				Chats:          []store.Chat{{ID: "chat-1", Title: "Alice"}},
				MessagesByChat: map[string][]store.Message{"chat-1": nil},
				DraftsByChat:   map[string]string{},
				ActiveChatID:   "chat-1",
			}, nil
		},
	})

	updated, _ := model.handleLiveUpdate(LiveUpdate{Refresh: true})
	updated, _ = updated.handleLiveUpdate(LiveUpdate{Refresh: true})
	updated, _ = updated.handleLiveUpdate(LiveUpdate{Refresh: true})
	if !updated.refreshDebouncePending || updated.reloadInFlight {
		t.Fatalf("debounce/inflight = %v/%v, want pending/not inflight", updated.refreshDebouncePending, updated.reloadInFlight)
	}

	debounced, cmd := updated.handleRefreshDebounced()
	if cmd == nil {
		t.Fatal("handleRefreshDebounced() cmd = nil, want reload")
	}
	reloadMsg := cmd().(snapshotReloadedMsg)
	if reloads != 1 {
		t.Fatalf("reloads = %d, want one coalesced reload", reloads)
	}
	refreshed, _ := debounced.handleSnapshotReloaded(reloadMsg)
	if refreshed.refreshQueued {
		t.Fatal("refreshQueued = true after coalesced reload, want false")
	}
}

func TestSyncOverlayRendersAndBlocksInput(t *testing.T) {
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats: []store.Chat{
				{ID: "chat-1", Title: "Alice"},
				{ID: "chat-2", Title: "Bob"},
			},
			MessagesByChat: map[string][]store.Message{},
			DraftsByChat:   map[string]string{},
			ActiveChatID:   "chat-1",
		},
	})
	model.width = 96
	model.height = 24

	updated, _ := model.handleLiveUpdate(LiveUpdate{Sync: &SyncProgressUpdate{
		Active:    true,
		Total:     4,
		Processed: 2,
		Messages:  3,
		Receipts:  1,
	}})
	view := stripANSI(updated.View())
	for _, want := range []string{"Syncing WhatsApp updates", "2/4 events (50%)", "3 messages", "Input is paused"} {
		if !strings.Contains(view, want) {
			t.Fatalf("sync overlay missing %q\n%s", want, view)
		}
	}

	blocked, _ := updated.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	if blocked.(Model).activeChat != 0 {
		t.Fatalf("activeChat = %d, want input blocked at 0", blocked.(Model).activeChat)
	}
}

func TestLiveStartupOverlayRendersAndBlocksInput(t *testing.T) {
	model := NewModel(Options{
		BlockLiveStartup: true,
		Snapshot: store.Snapshot{
			Chats: []store.Chat{
				{ID: "chat-1", Title: "Alice"},
				{ID: "chat-2", Title: "Bob"},
			},
			MessagesByChat: map[string][]store.Message{},
			DraftsByChat:   map[string]string{},
			ActiveChatID:   "chat-1",
		},
	})
	model.width = 96
	model.height = 24

	view := stripANSI(model.View())
	for _, want := range []string{"Connecting to WhatsApp", "Checking for new chats", "Input is paused"} {
		if !strings.Contains(view, want) {
			t.Fatalf("startup overlay missing %q\n%s", want, view)
		}
	}

	blocked, _ := model.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	if blocked.(Model).activeChat != 0 {
		t.Fatalf("activeChat = %d, want input blocked at 0", blocked.(Model).activeChat)
	}
}

func TestLiveStartupOverlayClearsOnEmptySyncUpdate(t *testing.T) {
	model := NewModel(Options{BlockLiveStartup: true})
	model.width = 80
	model.height = 20

	updated, _ := model.handleLiveUpdate(LiveUpdate{Sync: &SyncProgressUpdate{}})
	if updated.syncOverlay.Visible {
		t.Fatal("startup overlay remained visible after empty sync update")
	}
	if view := stripANSI(updated.View()); strings.Contains(view, "Connecting to WhatsApp") {
		t.Fatalf("startup overlay did not clear\n%s", view)
	}
}

func TestSyncOverlayCompletesAndClears(t *testing.T) {
	model := NewModel(Options{})
	model.width = 80
	model.height = 20

	updated, _ := model.handleLiveUpdate(LiveUpdate{Sync: &SyncProgressUpdate{
		Completed: true,
		Total:     8,
		Processed: 8,
	}})
	if view := stripANSI(updated.View()); !strings.Contains(view, "Sync complete") {
		t.Fatalf("completed overlay missing\n%s", view)
	}

	cleared, _ := updated.Update(syncOverlayDoneMsg{Generation: updated.syncOverlay.Generation})
	if view := stripANSI(cleared.(Model).View()); strings.Contains(view, "Sync complete") {
		t.Fatalf("completed overlay did not clear\n%s", view)
	}
}

func TestSnapshotReloadKeepsCurrentChatWhenRequestWasStale(t *testing.T) {
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats: []store.Chat{
				{ID: "chat-1", Title: "Alice"},
				{ID: "chat-2", Title: "Project"},
			},
			MessagesByChat: map[string][]store.Message{
				"chat-1": {{ID: "m-1", ChatID: "chat-1", Sender: "Alice", Body: "one"}},
				"chat-2": {{ID: "m-2", ChatID: "chat-2", Sender: "Project", Body: "two"}},
			},
			DraftsByChat: map[string]string{},
			ActiveChatID: "chat-1",
		},
	})
	model.activeChat = 1

	refreshed, _ := model.handleSnapshotReloaded(snapshotReloadedMsg{
		ActiveChatID: "chat-1",
		Snapshot: store.Snapshot{
			Chats: []store.Chat{
				{ID: "chat-1", Title: "Alice", Unread: 1},
				{ID: "chat-2", Title: "Project"},
			},
			MessagesByChat: map[string][]store.Message{
				"chat-1": {{ID: "m-1", ChatID: "chat-1", Sender: "Alice", Body: "updated"}},
			},
			DraftsByChat: map[string]string{},
			ActiveChatID: "chat-1",
		},
	})
	if refreshed.currentChat().ID != "chat-2" {
		t.Fatalf("current chat after stale reload = %q, want chat-2", refreshed.currentChat().ID)
	}
}

func TestVisibleMessageRangeBoundsLargeLoadedChat(t *testing.T) {
	model := NewModel(Options{})
	model.messageCursor = 2500
	model.messageScrollTop = 2500

	start, end := model.visibleMessageRange(5000, 12)
	if start > model.messageCursor || end <= model.messageCursor {
		t.Fatalf("visible range [%d,%d) does not contain cursor %d", start, end, model.messageCursor)
	}
	if got := end - start; got > maxMessageRenderWindow {
		t.Fatalf("visible range length = %d, want bounded window", got)
	}
	blocks := model.visibleMessageBlocks(numberedMessages(5000), 100, 12, nil)
	if len(blocks) > maxMessageRenderWindow {
		t.Fatalf("visibleMessageBlocks() length = %d, want bounded window", len(blocks))
	}
	if len(blocks) == 0 || blocks[0].messageIndex < start || blocks[len(blocks)-1].messageIndex >= end {
		t.Fatalf("visibleMessageBlocks() indexes outside visible range [%d,%d): %+v", start, end, blocks)
	}
}

func TestMessageViewportClipsTallSelectedBlockFromTop(t *testing.T) {
	blocks := []messageBlock{
		{lines: []string{"older-1", "older-2"}},
		{lines: []string{"selected-top", "selected-sender", "selected-body-1", "selected-body-2", "selected-meta", "selected-bottom"}},
		{lines: []string{"newer-1", "newer-2"}},
	}

	got := messageViewport(blocks, 1, 1, 4)
	want := []string{"selected-top", "selected-sender", "selected-body-1", "selected-body-2"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("messageViewport() = %q, want %q", got, want)
	}
}

func TestMessageViewportBackfillsOlderBlocksAboveCursor(t *testing.T) {
	blocks := []messageBlock{
		{lines: []string{"older-1", "older-2", "older-3", "older-4", "older-5"}},
		{lines: []string{"selected-top", "selected-bottom"}},
		{lines: []string{"newer-top", "newer-bottom"}},
	}

	got := messageViewport(blocks, 1, 1, 6)
	want := []string{"older-4", "older-5", "selected-top", "selected-bottom", "newer-top", "newer-bottom"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("messageViewport() = %q, want %q", got, want)
	}
}

func TestMessageViewportDoesNotSplitOrdinaryBlocks(t *testing.T) {
	blocks := []messageBlock{
		{lines: []string{"selected-top", "selected-bottom"}},
		{lines: []string{"next-top", "next-sender", "next-body", "next-bottom"}},
	}

	got := messageViewport(blocks, 0, 0, 3)
	want := []string{"selected-top", "selected-bottom"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("messageViewport() = %q, want %q", got, want)
	}
}

func TestMessageViewportRefsFollowBlockAwareClipping(t *testing.T) {
	blocks := []messageLayoutBlock{
		{
			lines: []string{"selected-top", "selected-bottom"},
			refs:  []messageLineRef{{}, {}},
		},
		{
			lines: []string{"media-top", "media-row-1", "media-row-2", "media-bottom"},
			refs: []messageLineRef{
				{},
				{target: "preview", row: 0},
				{target: "preview", row: 1},
				{},
			},
		},
	}

	refs := messageViewportRefs(blocks, 0, 0, 3)
	if len(refs) != 2 {
		t.Fatalf("messageViewportRefs() length = %d, want 2", len(refs))
	}
	for _, ref := range refs {
		if ref.target != "" {
			t.Fatalf("messageViewportRefs() included clipped preview ref %+v", ref)
		}
	}
}

func TestLargeMixedChatScrollKeepsViewBounded(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	messages := make([]store.Message, 0, 600)
	for i := 0; i < cap(messages); i++ {
		message := store.Message{
			ID:        fmt.Sprintf("m-%03d", i),
			ChatID:    "chat-1",
			Sender:    "Alice",
			Body:      fmt.Sprintf("message %03d with enough body text to wrap in the message pane while scrolling under load", i),
			Timestamp: now.Add(time.Duration(i) * time.Second),
		}
		if i%9 == 0 {
			message.Media = []store.MediaMetadata{{
				MessageID:     message.ID,
				MIMEType:      "image/jpeg",
				FileName:      fmt.Sprintf("photo-%03d.jpg", i),
				DownloadState: "downloaded",
				LocalPath:     fmt.Sprintf("/tmp/photo-%03d.jpg", i),
				UpdatedAt:     message.Timestamp,
			}}
		}
		messages = append(messages, message)
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
	model.height = 30
	model.focus = FocusMessages
	model.showCurrentChatLatest()

	for i := 0; i < 180; i++ {
		model.moveCursor(-1)
		assertViewWithinBounds(t, model)
	}
	for i := 0; i < 120; i++ {
		model.moveCursor(1)
		assertViewWithinBounds(t, model)
	}
}

func TestLargeOverlayChatScrollKeepsCursorVisibleWhileSyncPending(t *testing.T) {
	localPath := filepath.Join(t.TempDir(), "photo.jpg")
	if err := os.WriteFile(localPath, []byte("fake"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	messages := make([]store.Message, 0, 120)
	for i := 0; i < cap(messages); i++ {
		message := store.Message{
			ID:     fmt.Sprintf("m-%03d", i),
			ChatID: "chat-1",
			Sender: "Alice",
			Body:   fmt.Sprintf("overlay scroll marker %03d with enough body text to wrap in the message pane", i),
		}
		if i%4 == 0 || i == cap(messages)-1 {
			message.Media = []store.MediaMetadata{{
				MessageID:     message.ID,
				MIMEType:      "image/jpeg",
				FileName:      fmt.Sprintf("photo-%03d.jpg", i),
				DownloadState: "downloaded",
				LocalPath:     localPath,
			}}
		}
		messages = append(messages, message)
	}
	model := NewModel(Options{
		Config: configWithPreview(24, 6),
		PreviewReport: media.Report{
			Selected: media.BackendUeberzugPP,
			Reasons: map[media.Backend]string{
				media.BackendUeberzugPP: "available",
			},
		},
		Snapshot: store.Snapshot{
			Chats:          []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{"chat-1": messages},
			DraftsByChat:   map[string]string{},
			ActiveChatID:   "chat-1",
		},
	})
	model.width = 120
	model.height = 24
	model.focus = FocusMessages
	model.overlay = media.NewOverlayManagerForWriter(&bytes.Buffer{})
	model.showCurrentChatLatest()

	for _, message := range model.messagesByChat["chat-1"] {
		if len(message.Media) == 0 {
			continue
		}
		request, ok := model.previewRequestForMedia(message, message.Media[0], 0, 0)
		if !ok {
			t.Fatalf("previewRequestForMedia(%s) returned false", message.ID)
		}
		model.previewCache[media.PreviewKey(request)] = media.Preview{
			Key:             media.PreviewKey(request),
			MessageID:       message.ID,
			Kind:            media.KindImage,
			Backend:         media.BackendUeberzugPP,
			RenderedBackend: media.BackendUeberzugPP,
			Display:         media.PreviewDisplayOverlay,
			SourceKind:      media.SourceLocal,
			SourcePath:      localPath,
			Width:           request.Width,
			Height:          request.Height,
			Lines:           []string{fmt.Sprintf("fallback %s", message.ID)},
		}
	}
	if cmd := model.syncOverlayCmd(); cmd == nil {
		t.Fatal("syncOverlayCmd() = nil, want pending overlay command")
	}
	if !model.overlaySyncPending {
		t.Fatal("overlaySyncPending = false, want pending overlay while scrolling")
	}

	for i := 0; i < 35; i++ {
		updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
		model = updated.(Model)
		marker := fmt.Sprintf("overlay scroll marker %03d", model.messageCursor)
		view := stripANSI(model.View())
		if !strings.Contains(view, marker) {
			t.Fatalf("cursor message is not visible with pending overlays after %d scrolls; cursor=%d scrollTop=%d\n%s", i+1, model.messageCursor, model.messageScrollTop, view)
		}
		assertViewWithinBounds(t, model)
	}
}

func TestLargeGroupChatScrollUpKeepsViewBounded(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	messages := make([]store.Message, 0, 700)
	for i := 0; i < cap(messages); i++ {
		message := store.Message{
			ID:        fmt.Sprintf("m-%03d", i),
			ChatID:    "chat-1",
			ChatJID:   "family@g.us",
			Sender:    fmt.Sprintf("Member %02d 👩🏻‍⚕️", i%17),
			SenderJID: fmt.Sprintf("member-%02d@s.whatsapp.net", i%17),
			Body:      fmt.Sprintf("group message %03d 🙏🏻 with enough wrapped text to exercise sender rows and scrolling through a large conversation 🇧🇷", i),
			Timestamp: now.Add(time.Duration(i) * time.Second),
		}
		if i%6 == 0 {
			message.Body += "\nsecond paragraph to make this group message taller than the direct-chat baseline"
		}
		if i%8 == 0 {
			message.IsOutgoing = true
			message.Sender = "me"
			message.SenderJID = "me"
			message.Status = "sent"
		}
		if i%13 == 0 {
			message.Media = []store.MediaMetadata{{
				MessageID:     message.ID,
				MIMEType:      "image/jpeg",
				FileName:      fmt.Sprintf("group-photo-%03d.jpg", i),
				DownloadState: "remote",
				UpdatedAt:     message.Timestamp,
			}}
		}
		messages = append(messages, message)
	}
	model := NewModel(Options{
		Config: config.Config{EmojiMode: config.EmojiModeFull},
		Snapshot: store.Snapshot{
			Chats: []store.Chat{{
				ID:    "chat-1",
				JID:   "family@g.us",
				Title: "Family",
				Kind:  "group",
			}},
			MessagesByChat: map[string][]store.Message{"chat-1": messages},
			DraftsByChat:   map[string]string{},
			ActiveChatID:   "chat-1",
		},
	})
	model.width = 120
	model.height = 30
	model.focus = FocusMessages
	model.showCurrentChatLatest()

	for i := 0; i < 260; i++ {
		model.moveCursor(-1)
		assertViewWithinBounds(t, model)
	}
}

func TestGroupChatScrollUpShowsOlderMessagesAboveCursor(t *testing.T) {
	messages := make([]store.Message, 0, 40)
	for i := 0; i < cap(messages); i++ {
		messages = append(messages, store.Message{
			ID:      fmt.Sprintf("m-%02d", i),
			ChatID:  "chat-1",
			ChatJID: "group@g.us",
			Sender:  fmt.Sprintf("Member %02d", i%5),
			Body:    fmt.Sprintf("group message %02d marker", i),
		})
	}
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats:          []store.Chat{{ID: "chat-1", JID: "group@g.us", Title: "Group", Kind: "group"}},
			MessagesByChat: map[string][]store.Message{"chat-1": messages},
			DraftsByChat:   map[string]string{},
			ActiveChatID:   "chat-1",
		},
	})
	model.width = 120
	model.height = 24
	model.focus = FocusMessages
	model.showCurrentChatLatest()

	updated, _ := model.updateNormal(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
	model = updated.(Model)

	if model.messageCursor != 38 {
		t.Fatalf("messageCursor = %d, want 38 after one upward move", model.messageCursor)
	}
	view := stripANSI(model.renderMessages(90, 18))
	olderLine := lineIndexContaining(view, "group message 37 marker")
	cursorLine := lineIndexContaining(view, "group message 38 marker")
	if cursorLine == -1 {
		t.Fatalf("cursor message is not visible after scrolling up\n%s", view)
	}
	if olderLine == -1 || olderLine >= cursorLine {
		t.Fatalf("older message should appear above cursor after scrolling up; older=%d cursor=%d\n%s", olderLine, cursorLine, view)
	}
}

func TestMessageScrollUpFromLatestKeepsNewerMessagesVisible(t *testing.T) {
	messages := make([]store.Message, 0, 40)
	for i := 0; i < cap(messages); i++ {
		messages = append(messages, store.Message{
			ID:      fmt.Sprintf("m-%02d", i),
			ChatID:  "chat-1",
			ChatJID: "group@g.us",
			Sender:  fmt.Sprintf("Member %02d", i%5),
			Body:    fmt.Sprintf("group message %02d marker", i),
		})
	}
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats:          []store.Chat{{ID: "chat-1", JID: "group@g.us", Title: "Group", Kind: "group"}},
			MessagesByChat: map[string][]store.Message{"chat-1": messages},
			DraftsByChat:   map[string]string{},
			ActiveChatID:   "chat-1",
		},
	})
	model.width = 120
	model.height = 24
	model.focus = FocusMessages
	model.showCurrentChatLatest()
	initialScrollTop := model.messageScrollTop

	updated, _ := model.updateNormal(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
	model = updated.(Model)

	if model.messageCursor != 38 {
		t.Fatalf("messageCursor = %d, want 38 after one upward move", model.messageCursor)
	}
	if model.messageScrollTop != initialScrollTop {
		t.Fatalf("messageScrollTop = %d, want unchanged %d while cursor remains visible", model.messageScrollTop, initialScrollTop)
	}
	view := stripANSI(model.renderMessages(90, 18))
	cursorLine := lineIndexContaining(view, "group message 38 marker")
	newerLine := lineIndexContaining(view, "group message 39 marker")
	if cursorLine == -1 || newerLine == -1 {
		t.Fatalf("cursor and newer messages should both remain visible; cursor=%d newer=%d\n%s", cursorLine, newerLine, view)
	}
	if newerLine <= cursorLine {
		t.Fatalf("newer message should stay below cursor after one upward move; cursor=%d newer=%d\n%s", cursorLine, newerLine, view)
	}
}

func TestMessageNavigationScrollsOnlyAtViewportBoundary(t *testing.T) {
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats:          []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{"chat-1": numberedMessages(60)},
			DraftsByChat:   map[string]string{},
			ActiveChatID:   "chat-1",
		},
	})
	model.width = 100
	model.height = 18
	model.focus = FocusMessages
	model.showCurrentChatLatest()
	initialScrollTop := model.messageScrollTop

	for i := 0; i < 3; i++ {
		updated, _ := model.updateNormal(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
		model = updated.(Model)
		if model.messageScrollTop != initialScrollTop {
			t.Fatalf("messageScrollTop changed after %d in-viewport upward move(s): got %d want %d", i+1, model.messageScrollTop, initialScrollTop)
		}
	}

	for i := 0; i < 12 && model.messageScrollTop == initialScrollTop; i++ {
		updated, _ := model.updateNormal(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
		model = updated.(Model)
	}
	if model.messageScrollTop == initialScrollTop {
		t.Fatalf("messageScrollTop never moved after cursor crossed the top boundary")
	}
	scrolledTop := model.messageScrollTop

	updated, _ := model.updateNormal(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	model = updated.(Model)
	if model.messageScrollTop != scrolledTop {
		t.Fatalf("messageScrollTop = %d, want unchanged %d after moving down within viewport", model.messageScrollTop, scrolledTop)
	}
}

func TestMessageNavigationIgnoresTerminalOverlayStateWithinViewport(t *testing.T) {
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats:          []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{"chat-1": numberedMessages(60)},
			DraftsByChat:   map[string]string{},
			ActiveChatID:   "chat-1",
		},
	})
	model.width = 100
	model.height = 18
	model.focus = FocusMessages
	model.showCurrentChatLatest()
	model.overlaySignature = "active-overlay"
	model.overlaySyncPending = true
	model.overlayPendingSignature = "pending-overlay"
	initialScrollTop := model.messageScrollTop

	for i := 0; i < 3; i++ {
		updated, _ := model.updateNormal(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
		model = updated.(Model)
		if model.messageScrollTop != initialScrollTop {
			t.Fatalf("messageScrollTop changed after %d in-viewport upward move(s) with overlay state: got %d want %d", i+1, model.messageScrollTop, initialScrollTop)
		}
		view := stripANSI(model.renderMessages(model.messagePaneContentWidth(), model.messagePaneContentHeight()))
		marker := fmt.Sprintf("message %02d", model.messageCursor)
		if !strings.Contains(view, marker) {
			t.Fatalf("selected message %q is not visible with overlay state\n%s", marker, view)
		}
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
	model.width = 100
	model.height = 20
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
	status := stripANSI(got.renderStatus())
	if !strings.Contains(status, "/needle 1/1") {
		t.Fatalf("status missing search count after search: %q", status)
	}
}

func TestSplitSearchSegmentsCaseInsensitiveAndNonOverlapping(t *testing.T) {
	segments := splitSearchSegments("bAnAnAna", "ana")
	want := []searchSegment{
		{text: "b"},
		{text: "AnA", match: true},
		{text: "n"},
		{text: "Ana", match: true},
	}
	if len(segments) != len(want) {
		t.Fatalf("segments = %+v, want %+v", segments, want)
	}
	for i := range want {
		if segments[i] != want[i] {
			t.Fatalf("segments[%d] = %+v, want %+v", i, segments[i], want[i])
		}
	}
}

func TestSplitSearchSegmentsAccentAware(t *testing.T) {
	segments := splitSearchSegments("olá ola", "ola")
	want := []searchSegment{
		{text: "olá", match: true},
		{text: " "},
		{text: "ola", match: true},
	}
	if len(segments) != len(want) {
		t.Fatalf("segments = %+v, want %+v", segments, want)
	}
	for i := range want {
		if segments[i] != want[i] {
			t.Fatalf("segments[%d] = %+v, want %+v", i, segments[i], want[i])
		}
	}

	segments = splitSearchSegments("olá ola", "olá")
	if len(segments) != 2 || !segments[0].match || segments[0].text != "olá" || segments[1].text != " ola" {
		t.Fatalf("accent-sensitive segments = %+v, want only accented match", segments)
	}
}

func TestRenderSearchHighlightedTextUsesMatchAndCurrentStyles(t *testing.T) {
	withANSIStyles(t)

	base := lipgloss.NewStyle().Foreground(primaryFG).Bold(true)

	normal := renderSearchHighlightedText("needle", "needle", base, false)
	normalCodes := sgrCodesBeforeNth(normal, "needle", 0)
	if !hasSGRCode(normalCodes, "3") || !hasSGRCode(normalCodes, "4") {
		t.Fatalf("normal match codes = %v, want italic and underline", normalCodes)
	}
	if hasSGRCode(normalCodes, "48") {
		t.Fatalf("normal match codes = %v, want no background highlight", normalCodes)
	}

	current := renderSearchHighlightedText("needle", "needle", base, true)
	currentCodes := sgrCodesBeforeNth(current, "needle", 0)
	if !hasSGRCode(currentCodes, "3") || !hasSGRCode(currentCodes, "4") || !hasSGRCode(currentCodes, "48") {
		t.Fatalf("current match codes = %v, want italic underline and background highlight", currentCodes)
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

	updated, cmd := model.updateNormal(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	got := runImmediateCmd(t, updated.(Model), cmd)
	if loadedChatID != "chat-2" {
		t.Fatalf("loadedChatID = %q, want chat-2", loadedChatID)
	}
	if got.messagesByChat["chat-2"][0].Body != "loaded on demand" {
		t.Fatalf("messagesByChat[chat-2] = %+v", got.messagesByChat["chat-2"])
	}
}

func TestHistoryFetchLoadsOlderLocalMessagesBeforeRemoteRequest(t *testing.T) {
	requested := false
	base := time.Unix(1_700_000_000, 0)
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats: []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{
				"chat-1": []store.Message{
					{ID: "m-3", ChatID: "chat-1", Sender: "Alice", Body: "three", Timestamp: base.Add(2 * time.Minute)},
					{ID: "m-4", ChatID: "chat-1", Sender: "Alice", Body: "four", Timestamp: base.Add(3 * time.Minute)},
				},
			},
			DraftsByChat: map[string]string{},
			ActiveChatID: "chat-1",
		},
		ConnectionState: ConnectionOnline,
		LoadOlderMessages: func(chatID string, before store.Message, limit int) ([]store.Message, error) {
			if chatID != "chat-1" || before.ID != "m-3" || limit != historyPageSize {
				t.Fatalf("older load args = (%q, %+v, %d)", chatID, before, limit)
			}
			return []store.Message{
				{ID: "m-1", ChatID: "chat-1", Sender: "Alice", Body: "one", Timestamp: base},
				{ID: "m-2", ChatID: "chat-1", Sender: "Alice", Body: "two", Timestamp: base.Add(time.Minute)},
			}, nil
		},
		RequestHistory: func(string) error {
			requested = true
			return nil
		},
	})

	updated, cmd := model.executeCommand("history fetch")
	got := runImmediateCmd(t, updated.(Model), cmd)
	if requested {
		t.Fatal("RequestHistory called even though older local messages were available")
	}
	messages := got.messagesByChat["chat-1"]
	if len(messages) != 4 || messages[0].ID != "m-1" || messages[1].ID != "m-2" {
		t.Fatalf("messages after history fetch = %+v", messages)
	}
	if got.messageCursor != 1 {
		t.Fatalf("messageCursor = %d, want last prepended message", got.messageCursor)
	}
}

func TestScrollAboveLoadedMessagesRequestsRemoteHistory(t *testing.T) {
	var requestedChatID string
	base := time.Unix(1_700_000_000, 0)
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats: []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{
				"chat-1": []store.Message{
					{ID: "m-1", ChatID: "chat-1", Sender: "Alice", Body: "one", Timestamp: base},
				},
			},
			DraftsByChat: map[string]string{},
			ActiveChatID: "chat-1",
		},
		ConnectionState: ConnectionOnline,
		LoadOlderMessages: func(string, store.Message, int) ([]store.Message, error) {
			return nil, nil
		},
		RequestHistory: func(chatID string) error {
			requestedChatID = chatID
			return nil
		},
	})
	model.focus = FocusMessages
	model.messageCursor = 0

	updated, cmd := model.updateNormal(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
	got := runImmediateCmd(t, updated.(Model), cmd)
	if requestedChatID != "chat-1" {
		t.Fatalf("requestedChatID = %q, want chat-1", requestedChatID)
	}
	if !strings.Contains(got.status, "requested older history") {
		t.Fatalf("status = %q, want requested older history", got.status)
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
	model.width = 110
	model.height = 32

	helped, _ := model.updateNormal(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")})
	got := helped.(Model)
	if !got.helpVisible {
		t.Fatal("helpVisible = false, want true")
	}
	view := stripANSI(got.View())
	for _, want := range []string{
		"vimwhat help",
		"Key labels follow your config",
		"Navigation",
		"Messages and Media",
		"Modes",
		"Commands",
		"j/k",
		"r/l",
		"pick recent sticker",
		"forward selected messages",
		"retry failed media",
		"retry-message|retry",
		"delete-message-everybody",
		"state: mode=normal focus=chats",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("help view missing %q\n%s", want, view)
		}
	}
}

func TestHelpOverlayUsesConfiguredKeyLabels(t *testing.T) {
	keymap := config.DefaultKeymap()
	keymap.HelpClose = "ctrl+g"
	keymap.NormalReply = "leader r"
	keymap.NormalOpenMedia = "m"
	keymap.NormalSaveMedia = "leader m"
	keymap.NormalPickSticker = "leader t"
	model := NewModel(Options{
		Config: config.Config{
			LeaderKey: ",",
			Keymap:    keymap,
		},
		Snapshot: store.Snapshot{
			Chats:          []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{"chat-1": nil},
			DraftsByChat:   map[string]string{},
			ActiveChatID:   "chat-1",
		},
	})
	model.width = 110
	model.height = 32

	helped, _ := model.updateNormal(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")})
	got := helped.(Model)
	view := stripANSI(got.View())
	for _, want := range []string{",r", "m", ",m", ",t", "ctrl+g/?"} {
		if !strings.Contains(view, want) {
			t.Fatalf("help view missing configured binding %q\n%s", want, view)
		}
	}
}

func TestHelpOverlayFitsNarrowWidth(t *testing.T) {
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats:          []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{"chat-1": nil},
			DraftsByChat:   map[string]string{},
			ActiveChatID:   "chat-1",
		},
	})
	model.width = 42
	model.height = 16

	helped, _ := model.updateNormal(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")})
	got := helped.(Model)
	assertViewWithinBounds(t, got)

	view := stripANSI(got.View())
	for _, want := range []string{"vimwhat help", "close help"} {
		if !strings.Contains(view, want) {
			t.Fatalf("narrow help view missing %q\n%s", want, view)
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
	model.width = 100
	model.height = 20
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

func TestEscapeInSearchModeClearsActiveSearch(t *testing.T) {
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats: []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{
				"chat-1": []store.Message{
					{ID: "m-1", ChatID: "chat-1", Sender: "Alice", Body: "needle first"},
					{ID: "m-2", ChatID: "chat-1", Sender: "Alice", Body: "needle second"},
				},
			},
			DraftsByChat: map[string]string{},
			ActiveChatID: "chat-1",
		},
	})
	model.width = 100
	model.height = 20
	model.focus = FocusMessages
	model.mode = ModeSearch
	model.searchLine = "needle"

	searched, _ := model.updateSearch(tea.KeyMsg{Type: tea.KeyEnter})
	searchModel := searched.(Model)
	if status := stripANSI(searchModel.renderStatus()); !strings.Contains(status, "/needle 1/2") {
		t.Fatalf("status before escape = %q, want active search count", status)
	}
	searchModel.mode = ModeSearch
	searchModel.searchLine = "replacement"

	escaped, _ := searchModel.updateSearch(tea.KeyMsg{Type: tea.KeyEsc})
	got := escaped.(Model)
	if got.mode != ModeNormal {
		t.Fatalf("mode = %s, want normal", got.mode)
	}
	if got.activeSearch != "" || got.lastSearch != "" || got.searchLine != "" || len(got.searchMatches) != 0 || got.searchIndex != -1 || got.lastSearchFocus != "" || got.searchChatSource != nil {
		t.Fatalf("search state after escape = active %q last %q line %q matches %v index %d focus %q source %v", got.activeSearch, got.lastSearch, got.searchLine, got.searchMatches, got.searchIndex, got.lastSearchFocus, got.searchChatSource)
	}
	if status := stripANSI(got.renderStatus()); strings.Contains(status, "/needle") || strings.Contains(status, "/replacement") {
		t.Fatalf("status after escape retained search state: %q", status)
	}
}

func TestEscapeInNormalModeClearsActiveSearch(t *testing.T) {
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats: []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{
				"chat-1": []store.Message{
					{ID: "m-1", ChatID: "chat-1", Sender: "Alice", Body: "needle first"},
					{ID: "m-2", ChatID: "chat-1", Sender: "Alice", Body: "needle second"},
				},
			},
			DraftsByChat: map[string]string{},
			ActiveChatID: "chat-1",
		},
	})
	model.width = 100
	model.height = 20
	model.focus = FocusMessages
	model.mode = ModeSearch
	model.searchLine = "needle"

	searched, _ := model.updateSearch(tea.KeyMsg{Type: tea.KeyEnter})
	searchModel := searched.(Model)
	if searchModel.mode != ModeNormal {
		t.Fatalf("mode after search = %s, want normal", searchModel.mode)
	}
	if status := stripANSI(searchModel.renderStatus()); !strings.Contains(status, "/needle 1/2") {
		t.Fatalf("status before normal escape = %q, want active search count", status)
	}

	escaped, _ := searchModel.Update(tea.KeyMsg{Type: tea.KeyEsc})
	got := escaped.(Model)
	if got.activeSearch != "" || got.lastSearch != "" || len(got.searchMatches) != 0 || got.searchIndex != -1 {
		t.Fatalf("search state after normal escape = active %q last %q matches %v index %d", got.activeSearch, got.lastSearch, got.searchMatches, got.searchIndex)
	}
	if status := stripANSI(got.renderStatus()); strings.Contains(status, "/needle") {
		t.Fatalf("status after normal escape retained search count: %q", status)
	}
}

func TestEscapeInSearchModeClearsUnsubmittedSearch(t *testing.T) {
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats:          []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{"chat-1": nil},
			DraftsByChat:   map[string]string{},
			ActiveChatID:   "chat-1",
		},
	})
	model.mode = ModeSearch
	model.searchLine = "needle"

	escaped, _ := model.updateSearch(tea.KeyMsg{Type: tea.KeyEsc})
	got := escaped.(Model)
	if got.mode != ModeNormal || got.searchLine != "" || got.activeSearch != "" || got.lastSearch != "" {
		t.Fatalf("search state after escape = mode %s line %q active %q last %q", got.mode, got.searchLine, got.activeSearch, got.lastSearch)
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
	model.width = 100
	model.height = 20
	model.focus = FocusMessages
	model.mode = ModeSearch
	model.searchLine = "needle"

	searched, _ := model.updateSearch(tea.KeyMsg{Type: tea.KeyEnter})
	got := searched.(Model)
	if got.messageCursor != 0 {
		t.Fatalf("first search cursor = %d, want 0", got.messageCursor)
	}
	if status := stripANSI(got.renderStatus()); !strings.Contains(status, "/needle 1/2") {
		t.Fatalf("status after first search = %q, want /needle 1/2", status)
	}

	next, _ := got.updateNormal(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	got = next.(Model)
	if got.messageCursor != 2 {
		t.Fatalf("n cursor = %d, want 2", got.messageCursor)
	}
	if status := stripANSI(got.renderStatus()); !strings.Contains(status, "/needle 2/2") {
		t.Fatalf("status after n = %q, want /needle 2/2", status)
	}

	prev, _ := got.updateNormal(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("N")})
	got = prev.(Model)
	if got.messageCursor != 0 {
		t.Fatalf("N cursor = %d, want 0", got.messageCursor)
	}
	if status := stripANSI(got.renderStatus()); !strings.Contains(status, "/needle 1/2") {
		t.Fatalf("status after N = %q, want /needle 1/2", status)
	}
	if len(got.messagesByChat["chat-1"]) != 3 {
		t.Fatalf("search navigation filtered messages: %+v", got.messagesByChat["chat-1"])
	}
}

func TestSearchStatusShowsZeroMatches(t *testing.T) {
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats: []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{
				"chat-1": []store.Message{{ID: "m-1", ChatID: "chat-1", Sender: "Alice", Body: "plain"}},
			},
			DraftsByChat: map[string]string{},
			ActiveChatID: "chat-1",
		},
	})
	model.width = 100
	model.height = 20
	model.focus = FocusMessages
	model.mode = ModeSearch
	model.searchLine = "needle"

	searched, _ := model.updateSearch(tea.KeyMsg{Type: tea.KeyEnter})
	got := searched.(Model)
	if got.activeSearch != "needle" || len(got.searchMatches) != 0 {
		t.Fatalf("search state = active %q matches %v, want needle and zero matches", got.activeSearch, got.searchMatches)
	}
	if status := stripANSI(got.renderStatus()); !strings.Contains(status, "/needle 0/0") {
		t.Fatalf("status = %q, want /needle 0/0", status)
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

	escaped, cmd := model.updateInsert(tea.KeyMsg{Type: tea.KeyEsc})
	normal := runImmediateCmd(t, escaped.(Model), cmd)
	normal.focus = FocusChats
	switched, cmd := normal.updateNormal(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	got := runImmediateCmd(t, switched.(Model), cmd)

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

func TestReserveLastColumnKeepsRenderedFrameOffTerminalEdge(t *testing.T) {
	model := NewModel(Options{
		ReserveLastColumn: true,
		Snapshot: store.Snapshot{
			Chats: []store.Chat{
				{ID: "chat-1", Title: "Alice", Unread: 9, Pinned: true},
				{ID: "chat-2", Title: "Project Maybewhats"},
			},
			MessagesByChat: map[string][]store.Message{
				"chat-1": {{ID: "m-1", ChatID: "chat-1", Sender: "Alice", Body: strings.Repeat("message body ", 20)}},
			},
			DraftsByChat: map[string]string{},
			ActiveChatID: "chat-1",
		},
		ConnectionState: ConnectionOnline,
	})
	model.width = 80
	model.height = 18
	model.mode = ModeInsert
	model.focus = FocusMessages
	model.status = strings.Repeat("status ", 20)
	model.composer = strings.Repeat("composer ", 20)

	view := model.View()
	for i, line := range strings.Split(view, "\n") {
		if width := lipgloss.Width(line); width > model.width-lastColumnGuardMargin {
			t.Fatalf("line %d width = %d, want <= %d with last-column guard\n%s", i+1, width, model.width-lastColumnGuardMargin, stripANSI(view))
		}
	}
}

func TestFinalFrameClampTruncatesOverwideANSILines(t *testing.T) {
	content := lipgloss.NewStyle().Foreground(accentFG).Render(strings.Repeat("wide", 20)) + "\n" +
		lipgloss.NewStyle().Bold(true).Render(strings.Repeat("status", 20)) + "\n" +
		"extra"
	clamped := stripANSI(clampFrame(content, 17, 2))
	lines := strings.Split(clamped, "\n")
	if len(lines) != 2 {
		t.Fatalf("clampFrame() produced %d lines, want 2\n%s", len(lines), clamped)
	}
	for i, line := range lines {
		if width := lipgloss.Width(line); width > 17 {
			t.Fatalf("line %d width = %d, want <= 17\n%s", i+1, width, clamped)
		}
	}
}

func TestReserveLastColumnUsesGuardedGeometry(t *testing.T) {
	model := NewModel(Options{
		ReserveLastColumn: true,
		Snapshot: store.Snapshot{
			Chats: []store.Chat{
				{ID: "chat-1", Title: "Alice"},
				{ID: "chat-2", Title: "Bob"},
			},
			MessagesByChat: map[string][]store.Message{
				"chat-1": {{ID: "m-1", ChatID: "chat-1", Sender: "Alice", Body: "hello"}},
			},
			ActiveChatID: "chat-1",
		},
	})
	model.width = 80
	model.height = 20
	model.focus = FocusMessages

	if got, want := model.layoutWidth(), 78; got != want {
		t.Fatalf("layoutWidth() = %d, want %d", got, want)
	}
	geometry, ok := model.messagePaneGeometry()
	if !ok {
		t.Fatal("messagePaneGeometry() returned false")
	}
	if right := geometry.x + geometry.width; right > model.width-lastColumnGuardMargin {
		t.Fatalf("message geometry right edge = %d, want <= %d", right, model.width-lastColumnGuardMargin)
	}

	model.infoPaneVisible = true
	geometry, ok = model.messagePaneGeometry()
	if !ok {
		t.Fatal("messagePaneGeometry() with info pane returned false")
	}
	if right := geometry.x + geometry.width; right > model.width-lastColumnGuardMargin {
		t.Fatalf("message geometry with info right edge = %d, want <= %d", right, model.width-lastColumnGuardMargin)
	}
}

func TestTerminalSizePolledMsgAppliesResize(t *testing.T) {
	model := NewModel(Options{
		PollTerminalSize: true,
		Snapshot: store.Snapshot{
			Chats:          []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{"chat-1": nil},
			ActiveChatID:   "chat-1",
		},
	})
	model.width = 140
	model.height = 40
	model.infoPaneVisible = true
	model.focus = FocusPreview

	updated, _ := model.Update(terminalSizePolledMsg{Width: 90, Height: 24, OK: true})
	next := updated.(Model)
	if next.width != 90 || next.height != 24 {
		t.Fatalf("size = %dx%d, want 90x24", next.width, next.height)
	}
	if !next.compactLayout || next.focus != FocusMessages {
		t.Fatalf("compact/focus = %v/%s, want compact messages", next.compactLayout, next.focus)
	}
}

func TestKeyTextUsesRunesForAltAndPasteInput(t *testing.T) {
	if got := keyText(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("é"), Alt: true}); got != "é" {
		t.Fatalf("alt rune text = %q, want é", got)
	}
	if got := keyText(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("pasted text"), Paste: true}); got != "pasted text" {
		t.Fatalf("pasted rune text = %q, want pasted text", got)
	}
	if got := keyText(tea.KeyMsg{Type: tea.KeySpace}); got != " " {
		t.Fatalf("space text = %q, want space", got)
	}
}

func TestInsertAltRuneAppendsCharacterNotKeyName(t *testing.T) {
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats:          []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{"chat-1": nil},
			ActiveChatID:   "chat-1",
		},
	})
	model.mode = ModeInsert
	model.focus = FocusMessages

	updated, _ := model.updateInsert(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("é"), Alt: true})
	model = updated.(Model)
	if model.composer != "é" {
		t.Fatalf("composer = %q, want é", model.composer)
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

func TestPanelStyleUsesStrongBorderOnlyForFocusedPane(t *testing.T) {
	model := NewModel(Options{})

	model.focus = FocusChats
	chatPanel := stripANSI(model.renderPanel(FocusChats, 30, 5, "chats"))
	messagePanel := stripANSI(model.renderPanel(FocusMessages, 30, 5, "messages"))
	if !strings.Contains(chatPanel, "┏") || !strings.Contains(chatPanel, "┗") {
		t.Fatalf("focused chat panel did not use strong border\n%s", chatPanel)
	}
	if strings.Contains(messagePanel, "┏") || strings.Contains(messagePanel, "┗") {
		t.Fatalf("unfocused message panel used strong border\n%s", messagePanel)
	}

	model.focus = FocusMessages
	chatPanel = stripANSI(model.renderPanel(FocusChats, 30, 5, "chats"))
	messagePanel = stripANSI(model.renderPanel(FocusMessages, 30, 5, "messages"))
	if strings.Contains(chatPanel, "┏") || strings.Contains(chatPanel, "┗") {
		t.Fatalf("unfocused chat panel used strong border\n%s", chatPanel)
	}
	if !strings.Contains(messagePanel, "┏") || !strings.Contains(messagePanel, "┗") {
		t.Fatalf("focused message panel did not use strong border\n%s", messagePanel)
	}
}

func TestWideViewHighlightsOnlyFocusedPanelBorder(t *testing.T) {
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats: []store.Chat{
				{ID: "chat-1", Title: "Alice"},
				{ID: "chat-2", Title: "Bob"},
			},
			MessagesByChat: map[string][]store.Message{
				"chat-1": {{ID: "m-1", ChatID: "chat-1", Body: "hello"}},
				"chat-2": nil,
			},
			DraftsByChat: map[string]string{},
			ActiveChatID: "chat-1",
		},
	})
	model.width = 120
	model.height = 20
	model.compactLayout = false

	model.focus = FocusChats
	chatsBody := stripANSI(model.renderBody(12))
	chatsTop := strings.Split(chatsBody, "\n")[0]
	if !strings.HasPrefix(chatsTop, "┏") || strings.Count(chatsTop, "┏") != 1 {
		t.Fatalf("chat-focused top border = %q, want only chat pane strong", chatsTop)
	}

	model.focus = FocusMessages
	messagesBody := stripANSI(model.renderBody(12))
	messagesTop := strings.Split(messagesBody, "\n")[0]
	if strings.HasPrefix(messagesTop, "┏") || strings.Count(messagesTop, "┏") != 1 {
		t.Fatalf("message-focused top border = %q, want only message pane strong", messagesTop)
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
	for _, want := range []string{"Project", "[PMDG]", "3", "draft: draft reply"} {
		if !strings.Contains(view, want) {
			t.Fatalf("chat list missing %q\n%s", want, view)
		}
	}
	if !strings.Contains(view, "┏") || !strings.Contains(view, "┗") {
		t.Fatalf("chat list did not render bordered cells\n%s", view)
	}
}

func TestUnreadChatBadgeUsesHighlightStyle(t *testing.T) {
	withANSIStyles(t)

	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats: []store.Chat{
				{ID: "chat-1", Title: "Alice", Unread: 3},
			},
			DraftsByChat: map[string]string{},
			ActiveChatID: "chat-1",
		},
	})

	view := model.renderChatCell(model.chats[0], false, 40)
	plain := stripANSI(view)
	if !strings.Contains(plain, "Alice") || !strings.Contains(plain, "3") {
		t.Fatalf("chat cell missing title or unread count\n%s", plain)
	}
	if strings.Contains(plain, "┏") || strings.Contains(plain, "┗") {
		t.Fatalf("inactive unread chat used thick cursor border\n%s", plain)
	}
	codes := sgrCodesBeforeNth(view, "3", 0)
	if !hasSGRCode(codes, "1") || !hasSGRCode(codes, "48") {
		t.Fatalf("unread badge codes = %v, want bold background style", codes)
	}
}

func TestUnreadChatBadgeCapsLargeCounts(t *testing.T) {
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats: []store.Chat{
				{ID: "chat-1", Title: "Alice", Unread: 100},
			},
			DraftsByChat: map[string]string{},
			ActiveChatID: "chat-1",
		},
	})

	view := stripANSI(model.renderChatCell(model.chats[0], false, 40))
	if !strings.Contains(view, "99+") || strings.Contains(view, "100") {
		t.Fatalf("chat cell unread cap rendered incorrectly\n%s", view)
	}
}

func TestActiveUnreadChatKeepsCursorBorderAndBadge(t *testing.T) {
	withANSIStyles(t)

	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats: []store.Chat{
				{ID: "chat-1", Title: "Alice", Unread: 4},
			},
			DraftsByChat: map[string]string{},
			ActiveChatID: "chat-1",
		},
	})

	view := model.renderChatCell(model.chats[0], true, 40)
	plain := stripANSI(view)
	if !strings.Contains(plain, "┏") || !strings.Contains(plain, "┗") {
		t.Fatalf("active unread chat did not keep thick cursor border\n%s", plain)
	}
	codes := sgrCodesBeforeNth(view, "4", 0)
	if !hasSGRCode(codes, "1") || !hasSGRCode(codes, "48") {
		t.Fatalf("active unread badge codes = %v, want bold background style", codes)
	}
}

func TestUnreadChatBadgeDoesNotOverflowNarrowCell(t *testing.T) {
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats: []store.Chat{
				{
					ID:       "chat-1",
					Title:    "Very Long Chat Title",
					Kind:     "group",
					Unread:   250,
					Pinned:   true,
					Muted:    true,
					HasDraft: true,
				},
			},
			DraftsByChat: map[string]string{"chat-1": "draft reply"},
			ActiveChatID: "chat-1",
		},
	})

	const width = 22
	view := stripANSI(model.renderChatCell(model.chats[0], false, width))
	for i, line := range strings.Split(view, "\n") {
		if got := lipgloss.Width(line); got > width {
			t.Fatalf("line %d width = %d, want <= %d\n%s", i+1, got, width, view)
		}
	}
	if !strings.Contains(view, "99+") {
		t.Fatalf("narrow unread chat missing capped badge\n%s", view)
	}
}

func TestGroupChatWeakTitleRendersAsPlaceholder(t *testing.T) {
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats: []store.Chat{{
				ID:          "12345-678@g.us",
				JID:         "12345-678@g.us",
				Title:       "12345-678",
				TitleSource: store.ChatTitleSourceJID,
				Kind:        "group",
			}},
			MessagesByChat: map[string][]store.Message{"12345-678@g.us": nil},
			DraftsByChat:   map[string]string{},
			ActiveChatID:   "12345-678@g.us",
		},
	})
	view := stripANSI(model.renderChats(40, 8))
	if strings.Contains(view, "12345-678") || !strings.Contains(view, "Unnamed group") {
		t.Fatalf("chat list = %q, want placeholder without numeric group id", view)
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

func TestChatSearchHighlightsTitlesOnlyAndHoveredChat(t *testing.T) {
	withANSIStyles(t)

	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats: []store.Chat{
				{ID: "chat-1", Title: "needle alpha", LastPreview: "needle preview"},
				{ID: "chat-2", Title: "needle beta", LastPreview: "needle preview"},
			},
			DraftsByChat: map[string]string{},
			ActiveChatID: "chat-2",
		},
	})
	model.width = 120
	model.height = 14
	model.focus = FocusChats
	model.activeChat = 1
	model.activeSearch = "needle"
	model.lastSearchFocus = FocusChats

	inactiveView := model.renderChatCell(model.chats[0], false, 40)
	inactiveTitleCodes := sgrCodesBeforeNth(inactiveView, "needle", 0)
	inactivePreviewCodes := sgrCodesBeforeNth(inactiveView, "needle", 1)
	if !hasSGRCode(inactiveTitleCodes, "3") || !hasSGRCode(inactiveTitleCodes, "4") || hasSGRCode(inactiveTitleCodes, "48") {
		t.Fatalf("inactive title codes = %v, want italic underline without current highlight", inactiveTitleCodes)
	}
	if hasSGRCode(inactivePreviewCodes, "3") || hasSGRCode(inactivePreviewCodes, "4") || hasSGRCode(inactivePreviewCodes, "48") {
		t.Fatalf("inactive preview codes = %v, want no search styling", inactivePreviewCodes)
	}

	activeView := model.renderChatCell(model.chats[1], true, 40)
	activeTitleCodes := sgrCodesBeforeNth(activeView, "needle", 0)
	activePreviewCodes := sgrCodesBeforeNth(activeView, "needle", 1)
	if !hasSGRCode(activeTitleCodes, "3") || !hasSGRCode(activeTitleCodes, "4") || !hasSGRCode(activeTitleCodes, "48") {
		t.Fatalf("active title codes = %v, want current match highlight", activeTitleCodes)
	}
	if hasSGRCode(activePreviewCodes, "3") || hasSGRCode(activePreviewCodes, "4") || hasSGRCode(activePreviewCodes, "48") {
		t.Fatalf("active preview codes = %v, want no search styling", activePreviewCodes)
	}
}

func TestActiveChatCellUsesStrongerCursorBorder(t *testing.T) {
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats: []store.Chat{
				{ID: "chat-1", Title: "Alice"},
				{ID: "chat-2", Title: "Bob"},
			},
			DraftsByChat: map[string]string{},
			ActiveChatID: "chat-1",
		},
	})

	inactiveView := stripANSI(model.renderChatCell(model.chats[1], false, 40))
	if !strings.Contains(inactiveView, "┌") || !strings.Contains(inactiveView, "└") {
		t.Fatalf("inactive chat cell did not keep normal border\n%s", inactiveView)
	}
	if strings.Contains(inactiveView, "┏") || strings.Contains(inactiveView, "┗") {
		t.Fatalf("inactive chat cell used cursor border\n%s", inactiveView)
	}

	activeView := stripANSI(model.renderChatCell(model.chats[0], true, 40))
	if !strings.Contains(activeView, "┏") || !strings.Contains(activeView, "┗") {
		t.Fatalf("active chat cell did not use thick cursor border\n%s", activeView)
	}
}

func TestChatAvatarPreviewRequestsUseOverlayBackendWhenSelected(t *testing.T) {
	avatarPath := filepath.Join(t.TempDir(), "avatar.jpg")
	model := NewModel(Options{
		Config: configWithPreview(24, 6),
		Paths:  testPaths(t),
		PreviewReport: media.Report{
			Selected: media.BackendUeberzugPP,
			Reasons: map[media.Backend]string{
				media.BackendUeberzugPP: "available",
				media.BackendChafa:      "available",
			},
		},
		Snapshot: store.Snapshot{
			Chats: []store.Chat{{
				ID:              "chat-1",
				Title:           "Alice",
				AvatarPath:      avatarPath,
				AvatarThumbPath: avatarPath,
			}},
			DraftsByChat: map[string]string{},
			ActiveChatID: "chat-1",
		},
	})
	model.width = 120
	model.height = 20

	requests := model.requestedAvatarPreviewRequests()
	if len(requests) != 1 {
		t.Fatalf("requestedAvatarPreviewRequests() = %+v, want one request", requests)
	}
	if requests[0].Backend != media.BackendUeberzugPP || requests[0].Width != chatAvatarPreviewWidth || requests[0].Height != chatAvatarPreviewHeight || !requests[0].Compact {
		t.Fatalf("avatar request = %+v, want compact ueberzug++ 4x2", requests[0])
	}
}

func TestChatAvatarPreviewRequestsCoverAllVisibleChats(t *testing.T) {
	tempDir := t.TempDir()
	chats := make([]store.Chat, 0, 12)
	messages := map[string][]store.Message{}
	for i := 0; i < 12; i++ {
		id := fmt.Sprintf("chat-%02d", i)
		chats = append(chats, store.Chat{
			ID:         id,
			Title:      fmt.Sprintf("Chat %02d", i),
			AvatarPath: filepath.Join(tempDir, id+".jpg"),
		})
		messages[id] = nil
	}
	model := NewModel(Options{
		Config: configWithPreview(24, 6),
		Paths:  testPaths(t),
		PreviewReport: media.Report{
			Selected: media.BackendUeberzugPP,
			Reasons: map[media.Backend]string{
				media.BackendUeberzugPP: "available",
			},
		},
		Snapshot: store.Snapshot{
			Chats:          chats,
			MessagesByChat: messages,
			DraftsByChat:   map[string]string{},
			ActiveChatID:   "chat-00",
		},
	})
	model.width = 120
	model.height = 80
	model.focus = FocusChats

	visible := len(model.visibleChatIDs())
	if visible != len(chats) {
		t.Fatalf("visible chat setup = %d, want %d", visible, len(chats))
	}
	requests := model.requestedAvatarPreviewRequests()
	if len(requests) != visible {
		t.Fatalf("requestedAvatarPreviewRequests() = %d request(s), want %d", len(requests), visible)
	}
}

func TestChatAvatarPreviewRendersCachedInlinePreview(t *testing.T) {
	avatarPath := filepath.Join(t.TempDir(), "avatar.jpg")
	model := NewModel(Options{
		Paths: testPaths(t),
		PreviewReport: media.Report{
			Selected: media.BackendUeberzugPP,
			Reasons: map[media.Backend]string{
				media.BackendUeberzugPP: "available",
				media.BackendChafa:      "available",
			},
		},
		Snapshot: store.Snapshot{
			Chats: []store.Chat{{
				ID:         "chat-1",
				Title:      "Alice",
				AvatarPath: avatarPath,
			}},
			DraftsByChat: map[string]string{},
			ActiveChatID: "chat-1",
		},
	})
	request, ok := model.chatAvatarPreviewRequest(model.chats[0])
	if !ok {
		t.Fatal("chatAvatarPreviewRequest() returned false")
	}
	model.previewCache[media.PreviewKey(request)] = media.Preview{
		Key:             media.PreviewKey(request),
		MessageID:       request.MessageID,
		Kind:            media.KindImage,
		Backend:         media.BackendChafa,
		RenderedBackend: media.BackendChafa,
		Display:         media.PreviewDisplayText,
		SourceKind:      media.SourceLocal,
		SourcePath:      avatarPath,
		Width:           request.Width,
		Height:          request.Height,
		Lines:           []string{"@@", "##"},
	}

	view := stripANSI(model.renderChatCell(model.chats[0], false, 40))
	if !strings.Contains(view, "@@") || !strings.Contains(view, "##") {
		t.Fatalf("renderChatCell() missing cached avatar preview\n%s", view)
	}
	if strings.Contains(view, "[A]") {
		t.Fatalf("renderChatCell() used initials despite cached avatar preview\n%s", view)
	}
	if countLines(view) != chatCellHeight {
		t.Fatalf("renderChatCell() line count = %d, want %d\n%s", countLines(view), chatCellHeight, view)
	}
}

func TestExternalPreviewPromptsBeforeChafaAvatarFallback(t *testing.T) {
	prev := inlineFallbackAllowed
	inlineFallbackAllowed = func() bool { return true }
	t.Cleanup(func() { inlineFallbackAllowed = prev })

	avatarPath := filepath.Join(t.TempDir(), "avatar.jpg")
	model := NewModel(Options{
		Paths: testPaths(t),
		PreviewReport: media.Report{
			Selected: media.BackendExternal,
			Reasons: map[media.Backend]string{
				media.BackendExternal: "available",
				media.BackendChafa:    "available",
			},
		},
		Snapshot: store.Snapshot{
			Chats: []store.Chat{{
				ID:         "chat-1",
				Title:      "Alice",
				AvatarPath: avatarPath,
			}},
			DraftsByChat: map[string]string{},
			ActiveChatID: "chat-1",
		},
	})
	model.width = 120
	model.height = 20

	if requests := model.requestedAvatarPreviewRequests(); len(requests) != 0 {
		t.Fatalf("requestedAvatarPreviewRequests() before fallback = %+v, want none", requests)
	}
	model.maybePromptInlineFallback()
	if !model.inlineFallbackPrompt {
		t.Fatal("inlineFallbackPrompt = false, want prompt for visible avatar fallback")
	}

	accepted, _ := model.handleInlineFallbackPrompt(tea.KeyMsg{Type: tea.KeyEnter})
	model = accepted.(Model)
	requests := model.requestedAvatarPreviewRequests()
	if len(requests) != 1 || requests[0].Backend != media.BackendChafa {
		t.Fatalf("requestedAvatarPreviewRequests() after fallback = %+v, want chafa request", requests)
	}
}

func TestPreviewNoneDoesNotPromptForChafaFallback(t *testing.T) {
	prev := inlineFallbackAllowed
	inlineFallbackAllowed = func() bool { return true }
	t.Cleanup(func() { inlineFallbackAllowed = prev })

	model := NewModel(Options{
		Paths: testPaths(t),
		PreviewReport: media.Report{
			Selected: media.BackendNone,
			Reasons: map[media.Backend]string{
				media.BackendChafa: "available",
			},
		},
		Snapshot: store.Snapshot{
			Chats: []store.Chat{{
				ID:         "chat-1",
				Title:      "Alice",
				AvatarPath: filepath.Join(t.TempDir(), "avatar.jpg"),
			}},
			DraftsByChat: map[string]string{},
			ActiveChatID: "chat-1",
		},
	})
	model.width = 120
	model.height = 20

	model.maybePromptInlineFallback()
	if model.inlineFallbackPrompt {
		t.Fatal("inlineFallbackPrompt = true, want no prompt when previews are disabled")
	}
}

func TestChatAvatarOverlayRendersFallbackUntilPlacementIsActive(t *testing.T) {
	avatarPath := filepath.Join(t.TempDir(), "avatar.jpg")
	model := NewModel(Options{
		Paths: testPaths(t),
		PreviewReport: media.Report{
			Selected: media.BackendUeberzugPP,
			Reasons: map[media.Backend]string{
				media.BackendUeberzugPP: "available",
			},
		},
		Snapshot: store.Snapshot{
			Chats: []store.Chat{{
				ID:         "chat-1",
				Title:      "Alice",
				AvatarPath: avatarPath,
			}},
			DraftsByChat: map[string]string{},
			ActiveChatID: "chat-1",
		},
	})
	model.width = 120
	model.height = 20
	request, ok := model.chatAvatarPreviewRequest(model.chats[0])
	if !ok {
		t.Fatal("chatAvatarPreviewRequest() returned false")
	}
	model.previewCache[media.PreviewKey(request)] = media.Preview{
		Key:             media.PreviewKey(request),
		MessageID:       request.MessageID,
		Kind:            media.KindImage,
		Backend:         media.BackendUeberzugPP,
		RenderedBackend: media.BackendUeberzugPP,
		Display:         media.PreviewDisplayOverlay,
		SourceKind:      media.SourceLocal,
		SourcePath:      avatarPath,
		Width:           request.Width,
		Height:          request.Height,
		Lines:           []string{"aa", "bb"},
	}

	fallbackView := stripANSI(model.renderChatCell(model.chats[0], false, 40))
	if !strings.Contains(fallbackView, "aa") || !strings.Contains(fallbackView, "bb") {
		t.Fatalf("renderChatCell() missing overlay fallback lines\n%s", fallbackView)
	}

	placements := model.visibleChatAvatarPlacements()
	if len(placements) != 1 {
		t.Fatalf("visibleChatAvatarPlacements() = %+v, want one placement", placements)
	}
	model.overlaySignature = overlayPlacementsSignature(placements)
	overlayView := stripANSI(model.renderChatCell(model.chats[0], false, 40))
	if strings.Contains(overlayView, "aa") || strings.Contains(overlayView, "bb") || strings.Contains(overlayView, "[A]") {
		t.Fatalf("renderChatCell() did not reserve blank avatar space for active overlay\n%s", overlayView)
	}
	if countLines(overlayView) != chatCellHeight {
		t.Fatalf("renderChatCell() line count = %d, want %d\n%s", countLines(overlayView), chatCellHeight, overlayView)
	}
}

func TestChatAvatarOverlayPlacementUsesChatCellCoordinates(t *testing.T) {
	avatarPath := filepath.Join(t.TempDir(), "avatar.jpg")
	model := NewModel(Options{
		Paths: testPaths(t),
		PreviewReport: media.Report{
			Selected: media.BackendUeberzugPP,
			Reasons: map[media.Backend]string{
				media.BackendUeberzugPP: "available",
			},
		},
		Snapshot: store.Snapshot{
			Chats: []store.Chat{{
				ID:         "chat-1",
				Title:      "Alice",
				AvatarPath: avatarPath,
			}},
			DraftsByChat: map[string]string{},
			ActiveChatID: "chat-1",
		},
	})
	model.width = 120
	model.height = 20
	request, ok := model.chatAvatarPreviewRequest(model.chats[0])
	if !ok {
		t.Fatal("chatAvatarPreviewRequest() returned false")
	}
	model.previewCache[media.PreviewKey(request)] = media.Preview{
		Key:             media.PreviewKey(request),
		MessageID:       request.MessageID,
		Kind:            media.KindImage,
		Backend:         media.BackendUeberzugPP,
		RenderedBackend: media.BackendUeberzugPP,
		Display:         media.PreviewDisplayOverlay,
		SourceKind:      media.SourceLocal,
		SourcePath:      avatarPath,
		Width:           request.Width,
		Height:          request.Height,
	}

	placements := model.visibleChatAvatarPlacements()
	if len(placements) != 1 {
		t.Fatalf("visibleChatAvatarPlacements() = %+v, want one placement", placements)
	}
	placement := placements[0]
	if placement.X != 4 || placement.Y != 3 || placement.MaxWidth != chatAvatarPreviewWidth || placement.MaxHeight != chatAvatarPreviewHeight || placement.Path != avatarPath {
		t.Fatalf("chat avatar placement = %+v, want x=4 y=3 size=4x2 path=%s", placement, avatarPath)
	}
}

func TestChatAvatarOverlayPlacementsSkipHiddenCompactChatPane(t *testing.T) {
	avatarPath := filepath.Join(t.TempDir(), "avatar.jpg")
	model := NewModel(Options{
		Paths: testPaths(t),
		PreviewReport: media.Report{
			Selected: media.BackendUeberzugPP,
			Reasons: map[media.Backend]string{
				media.BackendUeberzugPP: "available",
			},
		},
		Snapshot: store.Snapshot{
			Chats: []store.Chat{{
				ID:         "chat-1",
				Title:      "Alice",
				AvatarPath: avatarPath,
			}},
			MessagesByChat: map[string][]store.Message{"chat-1": nil},
			DraftsByChat:   map[string]string{},
			ActiveChatID:   "chat-1",
		},
	})
	model.width = 80
	model.height = 20
	model.compactLayout = true
	model.focus = FocusMessages
	request, ok := model.chatAvatarPreviewRequest(model.chats[0])
	if !ok {
		t.Fatal("chatAvatarPreviewRequest() returned false")
	}
	model.previewCache[media.PreviewKey(request)] = media.Preview{
		Key:             media.PreviewKey(request),
		MessageID:       request.MessageID,
		Kind:            media.KindImage,
		Backend:         media.BackendUeberzugPP,
		RenderedBackend: media.BackendUeberzugPP,
		Display:         media.PreviewDisplayOverlay,
		SourceKind:      media.SourceLocal,
		SourcePath:      avatarPath,
		Width:           request.Width,
		Height:          request.Height,
	}

	if placements := model.visibleChatAvatarPlacements(); len(placements) != 0 {
		t.Fatalf("visibleChatAvatarPlacements() = %+v, want none while compact chat pane is hidden", placements)
	}
}

func TestChatAvatarOverlayStaysActiveWhenFocusMovesWithinVisibleChats(t *testing.T) {
	avatarPath := filepath.Join(t.TempDir(), "avatar.jpg")
	model := NewModel(Options{
		Paths: testPaths(t),
		PreviewReport: media.Report{
			Selected: media.BackendUeberzugPP,
			Reasons: map[media.Backend]string{
				media.BackendUeberzugPP: "available",
			},
		},
		Snapshot: store.Snapshot{
			Chats: []store.Chat{
				{ID: "chat-1", Title: "Alice", AvatarPath: avatarPath},
				{ID: "chat-2", Title: "Bob"},
				{ID: "chat-3", Title: "Carol"},
			},
			MessagesByChat: map[string][]store.Message{
				"chat-1": nil,
				"chat-2": nil,
				"chat-3": nil,
			},
			DraftsByChat: map[string]string{},
			ActiveChatID: "chat-1",
		},
	})
	model.width = 120
	model.height = 20
	model.focus = FocusChats
	cacheChatAvatarOverlayPreview(t, &model, model.chats[0], avatarPath, "low-avatar-1", "low-avatar-2")

	cmd := model.syncOverlayCmd()
	if cmd == nil {
		t.Fatal("syncOverlayCmd() = nil, want avatar overlay command")
	}
	model = applyOverlayCmd(t, model, cmd)

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	model = updated.(Model)
	if model.avatarOverlayPaused {
		t.Fatal("avatarOverlayPaused = true, want avatar overlay kept while visible chat window is unchanged")
	}
	if model.overlaySignature == "" {
		t.Fatal("overlaySignature is empty after moving within visible chat list")
	}
	view := stripANSI(model.View())
	if strings.Contains(view, "low-avatar") {
		t.Fatalf("View() rendered low-resolution avatar fallback while overlay remained active\n%s", view)
	}
}

func TestPendingChatAvatarOverlayDoesNotBlankFallback(t *testing.T) {
	avatarPath := filepath.Join(t.TempDir(), "avatar.jpg")
	model := NewModel(Options{
		Paths: testPaths(t),
		PreviewReport: media.Report{
			Selected: media.BackendUeberzugPP,
			Reasons: map[media.Backend]string{
				media.BackendUeberzugPP: "available",
			},
		},
		Snapshot: store.Snapshot{
			Chats: []store.Chat{{
				ID:         "chat-1",
				Title:      "Alice",
				AvatarPath: avatarPath,
			}},
			DraftsByChat: map[string]string{},
			ActiveChatID: "chat-1",
		},
	})
	model.width = 120
	model.height = 20
	model.focus = FocusChats
	cacheChatAvatarOverlayPreview(t, &model, model.chats[0], avatarPath, "low-avatar-1", "low-avatar-2")

	cmd := model.syncOverlayCmd()
	if cmd == nil {
		t.Fatal("syncOverlayCmd() = nil, want avatar overlay command")
	}
	pendingView := stripANSI(model.renderChatCell(model.chats[0], false, 40))
	if !strings.Contains(pendingView, "low-avatar-1") {
		t.Fatalf("pending avatar overlay blanked low-resolution fallback\n%s", pendingView)
	}

	model = applyOverlayCmd(t, model, cmd)
	appliedView := stripANSI(model.renderChatCell(model.chats[0], false, 40))
	if strings.Contains(appliedView, "low-avatar") {
		t.Fatalf("applied avatar overlay still rendered low-resolution fallback\n%s", appliedView)
	}
}

func TestChatAvatarOverlayPausesBlankWhenChatListScrolls(t *testing.T) {
	avatarPath := filepath.Join(t.TempDir(), "avatar.jpg")
	chats := make([]store.Chat, 0, 8)
	messages := map[string][]store.Message{}
	for i := 0; i < 8; i++ {
		id := fmt.Sprintf("chat-%d", i)
		chat := store.Chat{ID: id, Title: fmt.Sprintf("Chat %d", i)}
		if i == 4 {
			chat.AvatarPath = avatarPath
		}
		chats = append(chats, chat)
		messages[id] = nil
	}
	model := NewModel(Options{
		Paths: testPaths(t),
		PreviewReport: media.Report{
			Selected: media.BackendUeberzugPP,
			Reasons: map[media.Backend]string{
				media.BackendUeberzugPP: "available",
			},
		},
		Snapshot: store.Snapshot{
			Chats:          chats,
			MessagesByChat: messages,
			DraftsByChat:   map[string]string{},
			ActiveChatID:   "chat-3",
		},
	})
	model.width = 120
	model.height = 20
	model.focus = FocusChats
	model.activeChat = 3
	cacheChatAvatarOverlayPreview(t, &model, model.chats[4], avatarPath, "low-avatar-1", "low-avatar-2")

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("scroll command = nil, want clear/resume overlay commands")
	}
	if !model.avatarOverlayPaused {
		t.Fatal("avatarOverlayPaused = false, want paused after chat list scroll")
	}
	view := stripANSI(model.View())
	if strings.Contains(view, "low-avatar") {
		t.Fatalf("View() rendered low-resolution avatar fallback while avatar overlay was paused\n%s", view)
	}
}

func TestMessageSearchHighlightsBodiesOnlyAndHoveredMessage(t *testing.T) {
	withANSIStyles(t)

	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats: []store.Chat{{ID: "chat-1", JID: "group@g.us", Title: "Group", Kind: "group"}},
			MessagesByChat: map[string][]store.Message{
				"chat-1": {
					{ID: "m-1", ChatID: "chat-1", ChatJID: "group@g.us", Sender: "Alice", Body: "needle first"},
					{ID: "m-2", ChatID: "chat-1", ChatJID: "group@g.us", Sender: "needle sender", Body: "needle second"},
				},
			},
			DraftsByChat: map[string]string{},
			ActiveChatID: "chat-1",
		},
	})
	model.focus = FocusMessages
	model.messageCursor = 1
	model.messageScrollTop = 1
	model.activeSearch = "needle"
	model.lastSearchFocus = FocusMessages

	inactiveBubble := model.renderMessageBubbleForViewport(model.currentMessages()[0], 70, false, false, nil)
	inactiveBodyCodes := sgrCodesBeforeNth(inactiveBubble, "needle", 0)
	if !hasSGRCode(inactiveBodyCodes, "3") || !hasSGRCode(inactiveBodyCodes, "4") || hasSGRCode(inactiveBodyCodes, "48") {
		t.Fatalf("inactive body codes = %v, want italic underline without current highlight", inactiveBodyCodes)
	}

	activeBubble := model.renderMessageBubbleForViewport(model.currentMessages()[1], 70, true, false, nil)
	senderCodes := sgrCodesBeforeNth(activeBubble, "needle", 0)
	activeBodyCodes := sgrCodesBeforeNth(activeBubble, "needle", 1)
	if hasSGRCode(senderCodes, "3") || hasSGRCode(senderCodes, "4") || hasSGRCode(senderCodes, "48") {
		t.Fatalf("sender codes = %v, want no search styling", senderCodes)
	}
	if !hasSGRCode(activeBodyCodes, "3") || !hasSGRCode(activeBodyCodes, "4") || !hasSGRCode(activeBodyCodes, "48") {
		t.Fatalf("active body codes = %v, want current match highlight", activeBodyCodes)
	}
}

func TestActiveMessageBubbleUsesStrongerCursorBorder(t *testing.T) {
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats:          []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{"chat-1": nil},
			DraftsByChat:   map[string]string{},
			ActiveChatID:   "chat-1",
		},
	})
	message := store.Message{ID: "m-1", ChatID: "chat-1", Body: "hello"}

	inactiveBubble := stripANSI(model.renderMessageBubbleForViewport(message, 70, false, false, nil))
	if !strings.Contains(inactiveBubble, "╭") || !strings.Contains(inactiveBubble, "╰") {
		t.Fatalf("inactive message bubble did not keep rounded border\n%s", inactiveBubble)
	}
	if strings.Contains(inactiveBubble, "┏") || strings.Contains(inactiveBubble, "┗") {
		t.Fatalf("inactive message bubble used cursor border\n%s", inactiveBubble)
	}

	activeBubble := stripANSI(model.renderMessageBubbleForViewport(message, 70, true, false, nil))
	if !strings.Contains(activeBubble, "┏") || !strings.Contains(activeBubble, "┗") {
		t.Fatalf("active message bubble did not use thick cursor border\n%s", activeBubble)
	}

	selectedBubble := stripANSI(model.renderMessageBubbleForViewport(message, 70, false, true, nil))
	if !strings.Contains(selectedBubble, "┏") || !strings.Contains(selectedBubble, "┗") {
		t.Fatalf("selected message bubble did not use thick visual border\n%s", selectedBubble)
	}

	activeSelectedBubble := stripANSI(model.renderMessageBubbleForViewport(message, 70, true, true, nil))
	if !strings.Contains(activeSelectedBubble, "┏") || !strings.Contains(activeSelectedBubble, "┗") {
		t.Fatalf("active selected message bubble did not keep thick cursor border\n%s", activeSelectedBubble)
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

func TestGroupSenderLabelSanitizesControlText(t *testing.T) {
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats:          []store.Chat{{ID: "chat-1", JID: "group@g.us", Title: "Group", Kind: "group"}},
			MessagesByChat: map[string][]store.Message{"chat-1": nil},
			DraftsByChat:   map[string]string{},
			ActiveChatID:   "chat-1",
		},
	})
	bubble := model.renderMessageBubble(store.Message{
		ID:      "m-1",
		ChatID:  "chat-1",
		ChatJID: "group@g.us",
		Sender:  "Alice\nInjected\r\x1b[2J",
		Body:    "hello",
	}, 80, false, false)
	plain := stripANSI(bubble)

	if strings.Contains(bubble, "\x1b[2J") || strings.Contains(plain, "\r") || strings.Contains(plain, "\nInjected") {
		t.Fatalf("group sender label was not sanitized\nraw:\n%q\nplain:\n%s", bubble, plain)
	}
	if !strings.Contains(plain, "Alice Injected") {
		t.Fatalf("sanitized sender label missing expected text\n%s", plain)
	}
}

func TestMessageBubblePreservesFullEmojiSequences(t *testing.T) {
	model := NewModel(Options{
		Config: config.Config{EmojiMode: config.EmojiModeFull},
		Snapshot: store.Snapshot{
			Chats:          []store.Chat{{ID: "chat-1", JID: "group@g.us", Title: "Group", Kind: "group"}},
			MessagesByChat: map[string][]store.Message{"chat-1": nil},
			DraftsByChat:   map[string]string{},
			ActiveChatID:   "chat-1",
		},
	})
	bubble := model.renderMessageBubble(store.Message{
		ID:      "m-1",
		ChatID:  "chat-1",
		ChatJID: "group@g.us",
		Sender:  "Doctor 👩🏻‍⚕️",
		Body:    "tem que ter um desse na bolsa🙏🏻",
	}, 80, false, false)
	plain := stripANSI(bubble)

	if !strings.Contains(plain, "Doctor 👩🏻‍⚕️") || !strings.Contains(plain, "bolsa🙏🏻") {
		t.Fatalf("rendered bubble did not preserve full emoji sequences\n%s", plain)
	}
}

func TestMessageBubbleCompatEmojiModeStripsTerminalHostileModifiers(t *testing.T) {
	model := NewModel(Options{
		Config: config.Config{EmojiMode: config.EmojiModeCompat},
		Snapshot: store.Snapshot{
			Chats:          []store.Chat{{ID: "chat-1", JID: "group@g.us", Title: "Group", Kind: "group"}},
			MessagesByChat: map[string][]store.Message{"chat-1": nil},
			DraftsByChat:   map[string]string{},
			ActiveChatID:   "chat-1",
		},
	})
	bubble := model.renderMessageBubble(store.Message{
		ID:      "m-1",
		ChatID:  "chat-1",
		ChatJID: "group@g.us",
		Sender:  "Doctor 👩🏻‍⚕️",
		Body:    "tem que ter um desse na bolsa🙏🏻",
	}, 80, false, false)
	plain := stripANSI(bubble)

	for _, unsafe := range []rune{'\u200d', '\ufe0f', '\U0001f3fb'} {
		if strings.ContainsRune(plain, unsafe) {
			t.Fatalf("rendered bubble kept terminal-hostile rune %U\n%s", unsafe, plain)
		}
	}
	if !strings.Contains(plain, "Doctor 👩⚕") || !strings.Contains(plain, "bolsa🙏") {
		t.Fatalf("rendered bubble lost sanitized emoji bases\n%s", plain)
	}
}

func TestDisplayHelpersKeepEmojiClustersIntact(t *testing.T) {
	clusters := []string{
		"🙏🏻",
		"👩🏻‍⚕️",
		"👨‍👩‍👧‍👦",
		"🇧🇷",
		"e\u0301",
	}

	for _, cluster := range clusters {
		t.Run(cluster, func(t *testing.T) {
			prefix, rest := splitDisplayWidth(cluster+"x", 1)
			if prefix != cluster || rest != "x" {
				t.Fatalf("splitDisplayWidth() = %q/%q, want %q/%q", prefix, rest, cluster, "x")
			}

			width := displayWidth(cluster) + 1
			truncated := truncateDisplay(cluster+" tail", width)
			if !strings.Contains(truncated, cluster) {
				t.Fatalf("truncateDisplay() split or dropped emoji cluster: %q", truncated)
			}
		})
	}
}

func TestWrapComposerTextPreservesTextAndHardNewlines(t *testing.T) {
	tests := []struct {
		name  string
		value string
		width int
		want  []string
	}{
		{name: "empty", value: "", width: 8, want: []string{""}},
		{name: "hard newlines", value: "one\n\ntwo", width: 8, want: []string{"one", "", "two"}},
		{name: "long word", value: "abcdefghij", width: 4, want: []string{"abcd", "efgh", "ij"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := wrapComposerText(tt.value, tt.width)
			if !slices.Equal(got, tt.want) {
				t.Fatalf("wrapComposerText() = %#v, want %#v", got, tt.want)
			}
			if strings.Join(got, "\n") == tt.value && strings.Contains(tt.value, "\n") {
				return
			}
			if strings.Join(got, "") != strings.ReplaceAll(tt.value, "\n", "") {
				t.Fatalf("wrapComposerText() lost text: got %#v from %q", got, tt.value)
			}
		})
	}
}

func TestWrapComposerTextKeepsDisplayWidth(t *testing.T) {
	value := "olá José 👩‍⚕️ abcdefghij"
	lines := wrapComposerText(value, 7)
	for _, line := range lines {
		if width := displayWidth(line); width > 7 {
			t.Fatalf("wrapped line width = %d, want <= 7: %#v", width, lines)
		}
	}
	if got := strings.Join(lines, ""); got != value {
		t.Fatalf("wrapped text = %q, want %q", got, value)
	}
}

func TestComposerWrapsLongInputInsideFooterWidth(t *testing.T) {
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats:          []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{"chat-1": nil},
			DraftsByChat:   map[string]string{},
			ActiveChatID:   "chat-1",
		},
	})
	model.mode = ModeInsert
	model.composer = "hello world from composer"

	const width = 18
	footer := stripANSI(model.renderComposer(width))
	if strings.Contains(footer, "~") {
		t.Fatalf("composer footer truncated long input:\n%s", footer)
	}
	var bodyLines []string
	for i, line := range strings.Split(footer, "\n") {
		if lineWidth := lipgloss.Width(line); lineWidth > width {
			t.Fatalf("line %d width = %d, want <= %d\n%s", i+1, lineWidth, width, footer)
		}
		if strings.HasPrefix(line, "> ") {
			body := strings.TrimPrefix(line, "> ")
			body = strings.TrimRight(body, " ")
			body = strings.TrimSuffix(body, "▌")
			bodyLines = append(bodyLines, body)
		}
	}
	if len(bodyLines) < 2 {
		t.Fatalf("composer body did not wrap: %#v\n%s", bodyLines, footer)
	}
	if got := strings.Join(bodyLines, ""); got != model.composer {
		t.Fatalf("rendered composer body = %q, want %q\n%s", got, model.composer, footer)
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
		"read":       "[✓✓]",
		"played":     "[✓✓]",
		"custom":     "✓",
	}
	for status, want := range tests {
		if got := messageStatusTicks(status); got != want {
			t.Fatalf("messageStatusTicks(%q) = %q, want %q", status, got, want)
		}
	}
}

func TestPresenceHeaderTypingOnlineAndLastSeen(t *testing.T) {
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats: []store.Chat{{ID: "chat-1", JID: "chat-1@s.whatsapp.net", Title: "Alice", Kind: "direct", LastPreview: "latest message"}},
			MessagesByChat: map[string][]store.Message{
				"chat-1": {{ID: "m-1", ChatID: "chat-1", Sender: "Alice", Body: "latest message", Timestamp: time.Unix(1_700_000_000, 0)}},
			},
			DraftsByChat: map[string]string{},
			ActiveChatID: "chat-1",
		},
	})

	model, _ = model.handleLiveUpdate(LiveUpdate{Presence: PresenceUpdate{
		ChatID:              "chat-1",
		Sender:              "Alice",
		AvailabilityChanged: true,
		Online:              true,
	}})
	view := stripANSI(model.renderMessages(70, 8))
	if !strings.Contains(view, "Alice") || !strings.Contains(view, "online") {
		t.Fatalf("presence header missing title/online\n%s", view)
	}
	if preview := model.chatPreview(model.currentChat()); preview != "latest message" {
		t.Fatalf("chat preview = %q, want message preview while online", preview)
	}

	expiresAt := time.Now().Add(time.Minute)
	model, _ = model.handleLiveUpdate(LiveUpdate{Presence: PresenceUpdate{
		ChatID:        "chat-1",
		Sender:        "Alice",
		TypingChanged: true,
		Typing:        true,
		ExpiresAt:     expiresAt,
	}})
	view = stripANSI(model.renderMessages(70, 8))
	if !strings.Contains(view, "Alice typing...") || strings.Contains(view, "online") {
		t.Fatalf("typing presence should override online\n%s", view)
	}
	if preview := model.chatPreview(model.currentChat()); preview != "Alice typing..." {
		t.Fatalf("chat preview = %q, want typing preview", preview)
	}

	expired, _ := model.Update(presenceExpiredMsg{ChatID: "chat-1", At: expiresAt})
	model = expired.(Model)
	view = stripANSI(model.renderMessages(70, 8))
	if strings.Contains(view, "typing") || !strings.Contains(view, "online") {
		t.Fatalf("typing expiry should preserve online state\n%s", view)
	}

	lastSeen := time.Now().Add(-2 * time.Hour)
	model, _ = model.handleLiveUpdate(LiveUpdate{Presence: PresenceUpdate{
		ChatID:              "chat-1",
		Sender:              "Alice",
		AvailabilityChanged: true,
		Online:              false,
		LastSeen:            lastSeen,
	}})
	view = stripANSI(model.renderMessages(70, 8))
	if !strings.Contains(view, "last seen") {
		t.Fatalf("presence header missing last seen\n%s", view)
	}
}

func TestLastSeenFormatting(t *testing.T) {
	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.Local)
	tests := map[string]struct {
		lastSeen time.Time
		want     string
	}{
		"today":      {lastSeen: time.Date(2026, 4, 30, 9, 5, 0, 0, time.Local), want: "last seen 09:05"},
		"yesterday":  {lastSeen: time.Date(2026, 4, 29, 22, 15, 0, 0, time.Local), want: "last seen yesterday 22:15"},
		"same year":  {lastSeen: time.Date(2026, 2, 3, 8, 0, 0, 0, time.Local), want: "last seen Feb 3 08:00"},
		"prior year": {lastSeen: time.Date(2025, 12, 31, 23, 59, 0, 0, time.Local), want: "last seen Dec 31 2025 23:59"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := formatLastSeen(tt.lastSeen, now); got != tt.want {
				t.Fatalf("formatLastSeen() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRenderMessagesWithPresenceHeaderStaysWithinHeight(t *testing.T) {
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats: []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{
				"chat-1": numberedMessages(12),
			},
			DraftsByChat: map[string]string{},
			ActiveChatID: "chat-1",
		},
	})
	model.presenceByChat["chat-1"] = PresenceUpdate{
		ChatID:              "chat-1",
		AvailabilityChanged: true,
		Online:              true,
	}

	const height = 6
	view := stripANSI(model.renderMessages(70, height))
	if got := len(strings.Split(view, "\n")); got > height {
		t.Fatalf("renderMessages produced %d lines, want <= %d\n%s", got, height, view)
	}
	if !strings.Contains(view, "online") {
		t.Fatalf("renderMessages missing online header\n%s", view)
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
	if got := lipgloss.Width(strings.TrimLeft(bodyLine, " ")); got > 15 {
		t.Fatalf("short outgoing bubble line width = %d, want compact <= 15\n%s", got, view)
	}
	if !strings.Contains(view, "20:59 [✓✓]") {
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
	if strings.Contains(prompt, "[COMMAND]") || !strings.Contains(prompt, ":help") || !strings.Contains(prompt, "enter run") {
		t.Fatalf("command prompt missing workflow: %q", prompt)
	}

	model.mode = ModeSearch
	model.searchLine = "needle"
	prompt = stripANSI(model.renderInput())
	if strings.Contains(prompt, "[SEARCH]") || !strings.Contains(prompt, "/needle") || !strings.Contains(prompt, "empty clears") {
		t.Fatalf("search prompt missing workflow: %q", prompt)
	}

	model.mode = ModeConfirm
	model.confirmLine = "Y"
	prompt = stripANSI(model.renderInput())
	if !strings.Contains(prompt, "type Y") || !strings.Contains(prompt, "Y") || !strings.Contains(prompt, "enter confirm") {
		t.Fatalf("confirm prompt missing workflow: %q", prompt)
	}

	commandStatus := model.renderStatus()
	model.mode = ModeInsert
	insertStatus := model.renderStatus()
	if commandStatus == insertStatus {
		t.Fatal("statusbar styling did not change between command and insert modes")
	}
	if model.modeStatusColor(ModeInsert) != uiTheme.InsertModeBG {
		t.Fatalf("insert mode status color = %q, want %q", model.modeStatusColor(ModeInsert), uiTheme.InsertModeBG)
	}
}

func TestModeStatusColorUsesConfiguredIndicators(t *testing.T) {
	model := NewModel(Options{
		Config: config.Config{
			IndicatorInsert: "#112233",
			IndicatorSearch: "#445566",
		},
		Snapshot: store.Snapshot{
			Chats:          []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{"chat-1": nil},
			DraftsByChat:   map[string]string{},
			ActiveChatID:   "chat-1",
		},
	})

	if got := model.modeStatusColor(ModeInsert); got != lipgloss.Color("#112233") {
		t.Fatalf("insert mode color = %q, want #112233", got)
	}
	if got := model.modeStatusColor(ModeSearch); got != lipgloss.Color("#445566") {
		t.Fatalf("search mode color = %q, want #445566", got)
	}
	if got := model.modeStatusColor(ModeNormal); got != accentFG {
		t.Fatalf("normal mode color = %q, want default accent %q", got, accentFG)
	}
}

func TestModeTransitionsDoNotWriteRedundantModeStatus(t *testing.T) {
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
	model.width = 100
	model.height = 20
	model.focus = FocusMessages
	model.status = "ready"

	for _, key := range []string{"i", "v", ":", "/"} {
		updated, _ := model.updateNormal(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
		got := updated.(Model)
		status := stripANSI(got.renderStatus())
		for _, redundant := range []string{"normal mode", "insert mode", "visual mode", "command mode", "search mode"} {
			if strings.Contains(strings.ToLower(status), redundant) {
				t.Fatalf("status for key %q contains redundant mode text %q: %q", key, redundant, status)
			}
		}
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

func TestVisualSelectionRangeUsesStrongerBorders(t *testing.T) {
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats: []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{
				"chat-1": []store.Message{
					{ID: "m-1", ChatID: "chat-1", Sender: "Alice", Body: "first"},
					{ID: "m-2", ChatID: "chat-1", Sender: "Alice", Body: "second"},
					{ID: "m-3", ChatID: "chat-1", Sender: "Alice", Body: "third"},
				},
			},
			DraftsByChat: map[string]string{},
			ActiveChatID: "chat-1",
		},
	})
	model.mode = ModeVisual
	model.focus = FocusMessages
	model.visualAnchor = 0
	model.messageCursor = 2
	model.messageScrollTop = 0

	view := stripANSI(model.renderMessages(70, 14))
	if got := strings.Count(view, "┏"); got != 3 {
		t.Fatalf("visual selected thick border count = %d, want 3\n%s", got, view)
	}
	if strings.Contains(view, "╭") || strings.Contains(view, "╰") {
		t.Fatalf("visual selected range kept rounded borders\n%s", view)
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

func TestNormalYankCopiesFocusedMessageToRegister(t *testing.T) {
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats: []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{
				"chat-1": {
					{ID: "m-1", ChatID: "chat-1", Sender: "Alice", Body: "first"},
					{ID: "m-2", ChatID: "chat-1", Sender: "Alice", Body: "second"},
				},
			},
			DraftsByChat: map[string]string{},
			ActiveChatID: "chat-1",
		},
	})
	model.mode = ModeNormal
	model.focus = FocusMessages
	model.messageCursor = 1

	updated, cmd := model.updateNormal(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	got := updated.(Model)
	if cmd != nil {
		t.Fatal("normal yank returned clipboard command without CopyToClipboard")
	}
	if got.yankRegister != "second" {
		t.Fatalf("yankRegister = %q, want second", got.yankRegister)
	}
	if !strings.Contains(got.status, "register") {
		t.Fatalf("status = %q, want register yank status", got.status)
	}
}

func TestNormalYankCopiesFocusedMessageToClipboard(t *testing.T) {
	var copied string
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats: []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{
				"chat-1": {
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
	model.mode = ModeNormal
	model.focus = FocusPreview
	model.messageCursor = 1

	updated, cmd := model.updateNormal(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	got := updated.(Model)
	if got.yankRegister != "second" {
		t.Fatalf("yankRegister = %q, want second", got.yankRegister)
	}
	if cmd == nil {
		t.Fatal("normal yank did not return clipboard command")
	}
	final, _ := got.Update(cmd())
	got = final.(Model)
	if copied != "second" {
		t.Fatalf("copied = %q, want second", copied)
	}
	if !strings.Contains(got.status, "clipboard") {
		t.Fatalf("status = %q, want clipboard copy result", got.status)
	}
}

func TestNormalYankReportsNoMessage(t *testing.T) {
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats:          []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{"chat-1": nil},
			DraftsByChat:   map[string]string{},
			ActiveChatID:   "chat-1",
		},
	})
	model.mode = ModeNormal
	model.focus = FocusMessages

	updated, cmd := model.updateNormal(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	got := updated.(Model)
	if cmd != nil {
		t.Fatal("normal yank returned command without a focused message")
	}
	if !strings.Contains(got.status, "no message selected") {
		t.Fatalf("status = %q, want no message selected", got.status)
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

func TestNormalLeaderPickStickerSendsWithoutChangingComposer(t *testing.T) {
	var sentSticker store.RecentSticker
	var sentChatID string
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats:          []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{"chat-1": nil},
			DraftsByChat:   map[string]string{"chat-1": "draft caption"},
			ActiveChatID:   "chat-1",
		},
		ConnectionState:      ConnectionOnline,
		RequireOnlineForSend: true,
		PickSticker: func() tea.Cmd {
			return func() tea.Msg {
				return StickerPickedMsg{Sticker: store.RecentSticker{
					ID:        "sticker-1",
					LocalPath: "/tmp/sticker.webp",
					FileName:  "sticker.webp",
					MIMEType:  "image/webp",
				}}
			}
		},
		SendSticker: func(chatID string, sticker store.RecentSticker) (store.Message, error) {
			sentChatID = chatID
			sentSticker = sticker
			return store.Message{
				ID:         "chat-1/remote-1",
				RemoteID:   "remote-1",
				ChatID:     chatID,
				Sender:     "me",
				Timestamp:  time.Unix(1_700_000_000, 0),
				IsOutgoing: true,
				Status:     "sending",
				Media: []store.MediaMetadata{{
					MessageID: "chat-1/remote-1",
					Kind:      "sticker",
					MIMEType:  "image/webp",
					FileName:  "sticker.webp",
					LocalPath: "/tmp/sticker.webp",
				}},
			}, nil
		},
	})
	model.mode = ModeNormal
	model.focus = FocusMessages
	model.composer = "draft caption"
	model.attachments = []Attachment{{LocalPath: "/tmp/photo.jpg", FileName: "photo.jpg", MIMEType: "image/jpeg"}}

	leader, _ := model.updateNormal(tea.KeyMsg{Type: tea.KeySpace})
	model = leader.(Model)
	picking, cmd := model.updateNormal(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("t")})
	model = picking.(Model)
	if cmd == nil {
		t.Fatal("<leader>t command = nil, want sticker picker")
	}
	final, sendCmd := model.Update(cmd())
	got := runImmediateCmd(t, final.(Model), sendCmd)
	if sentChatID != "chat-1" || sentSticker.ID != "sticker-1" {
		t.Fatalf("sent sticker chat=%q sticker=%+v", sentChatID, sentSticker)
	}
	if got.composer != "draft caption" || len(got.attachments) != 1 {
		t.Fatalf("composer/attachments changed = %q/%+v", got.composer, got.attachments)
	}
	if len(got.messagesByChat["chat-1"]) != 1 || got.messagesByChat["chat-1"][0].Media[0].Kind != "sticker" {
		t.Fatalf("messages after sticker send = %+v", got.messagesByChat["chat-1"])
	}
}

func TestInsertPasteImageStagesClipboardImageInComposer(t *testing.T) {
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats:          []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{"chat-1": nil},
			DraftsByChat:   map[string]string{},
			ActiveChatID:   "chat-1",
		},
		PasteAttachmentFromClipboard: func() tea.Cmd {
			return func() tea.Msg {
				return ClipboardAttachmentPastedMsg{Attachment: Attachment{
					LocalPath:     "/tmp/clipboard.png",
					FileName:      "clipboard.png",
					MIMEType:      "image/png",
					SizeBytes:     1024,
					DownloadState: "local_pending",
				}}
			}
		},
	})
	model.mode = ModeInsert
	model.focus = FocusMessages
	model.composer = "caption"

	updated, cmd := model.updateInsert(tea.KeyMsg{Type: tea.KeyCtrlV})
	got := updated.(Model)
	if cmd == nil {
		t.Fatal("ctrl+v did not start clipboard attachment paste")
	}
	final, _ := got.Update(cmd())
	got = final.(Model)
	if got.mode != ModeInsert || got.composer != "caption" {
		t.Fatalf("mode/composer = %s/%q, want insert/caption", got.mode, got.composer)
	}
	if len(got.attachments) != 1 || got.attachments[0].FileName != "clipboard.png" {
		t.Fatalf("attachments = %+v", got.attachments)
	}
}

func TestPasteImageCommandStagesClipboardImage(t *testing.T) {
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats:          []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{"chat-1": nil},
			DraftsByChat:   map[string]string{},
			ActiveChatID:   "chat-1",
		},
		PasteAttachmentFromClipboard: func() tea.Cmd {
			return func() tea.Msg {
				return ClipboardAttachmentPastedMsg{Attachment: Attachment{
					LocalPath:     "/tmp/clipboard.png",
					FileName:      "clipboard.png",
					MIMEType:      "image/png",
					DownloadState: "local_pending",
				}}
			}
		},
	})

	updated, cmd := model.executeCommand("paste-attachment")
	got := updated.(Model)
	if got.mode != ModeInsert || cmd == nil {
		t.Fatalf("mode=%s cmd nil=%v, want insert and paste command", got.mode, cmd == nil)
	}
	final, _ := got.Update(cmd())
	got = final.(Model)
	if len(got.attachments) != 1 || got.attachments[0].MIMEType != "image/png" {
		t.Fatalf("attachments = %+v", got.attachments)
	}
}

func TestInsertPasteAttachmentReplacesStagedAttachment(t *testing.T) {
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats:          []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{"chat-1": nil},
			DraftsByChat:   map[string]string{},
			ActiveChatID:   "chat-1",
		},
		PasteAttachmentFromClipboard: func() tea.Cmd {
			return func() tea.Msg {
				return ClipboardAttachmentPastedMsg{Attachment: Attachment{
					LocalPath:     "/tmp/report.pdf",
					FileName:      "report.pdf",
					MIMEType:      "application/pdf",
					DownloadState: "local_pending",
				}}
			}
		},
	})
	model.mode = ModeInsert
	model.focus = FocusMessages
	model.composer = "caption"
	model.attachments = []Attachment{{
		LocalPath:     "/tmp/photo.png",
		FileName:      "photo.png",
		MIMEType:      "image/png",
		DownloadState: "local_pending",
	}}

	updated, cmd := model.updateInsert(tea.KeyMsg{Type: tea.KeyCtrlV})
	if cmd == nil {
		t.Fatal("ctrl+v did not start clipboard attachment paste")
	}
	final, _ := updated.(Model).Update(cmd())
	got := final.(Model)
	if got.mode != ModeInsert || got.composer != "caption" {
		t.Fatalf("mode/composer = %s/%q, want insert/caption", got.mode, got.composer)
	}
	if len(got.attachments) != 1 || got.attachments[0].FileName != "report.pdf" {
		t.Fatalf("attachments = %+v", got.attachments)
	}
}

func TestAttachmentOnlySendPersistsMedia(t *testing.T) {
	attachmentPath := filepath.Join(t.TempDir(), "report.pdf")
	if err := os.WriteFile(attachmentPath, []byte("pdf-bytes"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	var sentAttachments []Attachment
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats:          []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{"chat-1": nil},
			DraftsByChat:   map[string]string{},
			ActiveChatID:   "chat-1",
		},
		PersistMessage: func(outgoing OutgoingMessage) (store.Message, error) {
			sentAttachments = outgoing.Attachments
			return store.Message{
				ID:         "local-1",
				ChatID:     outgoing.ChatID,
				Sender:     "me",
				Body:       outgoing.Body,
				IsOutgoing: true,
				Media: []store.MediaMetadata{{
					MessageID:     "local-1",
					FileName:      outgoing.Attachments[0].FileName,
					MIMEType:      outgoing.Attachments[0].MIMEType,
					SizeBytes:     outgoing.Attachments[0].SizeBytes,
					DownloadState: outgoing.Attachments[0].DownloadState,
				}},
			}, nil
		},
		SaveDraft: func(chatID, body string) error { return nil },
	})
	model.mode = ModeInsert
	model.attachments = []Attachment{{
		LocalPath:     attachmentPath,
		FileName:      "report.pdf",
		MIMEType:      "application/pdf",
		SizeBytes:     1024,
		DownloadState: "local_pending",
	}}

	updated, cmd := model.updateInsert(tea.KeyMsg{Type: tea.KeyEnter})
	got := runImmediateCmd(t, updated.(Model), cmd)
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

func TestEnterOnMediaQueuesPreviewRequest(t *testing.T) {
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
	model.focus = FocusMessages

	if requests := model.requestedPreviewRequests(); len(requests) != 0 {
		t.Fatalf("requestedPreviewRequests() before activation = %+v, want none", requests)
	}

	activated, _ := model.activateFocusedMediaPreview()
	model = activated.(Model)

	requests := model.requestedPreviewRequests()
	if len(requests) != 1 {
		t.Fatalf("requestedPreviewRequests() = %+v, want one request", requests)
	}
	if requests[0].Width != 24 || requests[0].Height != 6 || requests[0].Backend != media.BackendChafa {
		t.Fatalf("request sizing/backend = %+v", requests[0])
	}

	queued, cmd := model.queueRequestedPreviewCmd()
	if cmd == nil {
		t.Fatal("queueRequestedPreviewCmd() returned nil command")
	}
	if !queued.previewInflight[media.PreviewKey(requests[0])] {
		t.Fatalf("previewInflight = %+v, want request key marked", queued.previewInflight)
	}
}

func TestEnterOnExternalMediaPromptsForChafaFallback(t *testing.T) {
	prev := inlineFallbackAllowed
	inlineFallbackAllowed = func() bool { return true }
	t.Cleanup(func() { inlineFallbackAllowed = prev })

	model := NewModel(Options{
		Config: configWithPreview(24, 6),
		Paths:  testPaths(t),
		PreviewReport: media.Report{
			Selected: media.BackendExternal,
			Reasons: map[media.Backend]string{
				media.BackendExternal: "available",
				media.BackendChafa:    "available",
			},
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
	model.focus = FocusMessages

	activated, cmd := model.activateFocusedMediaPreview()
	model = activated.(Model)
	if cmd != nil {
		t.Fatalf("activateFocusedMediaPreview() cmd = %T, want prompt without command", cmd)
	}
	if !model.inlineFallbackPrompt {
		t.Fatal("inlineFallbackPrompt = false, want prompt")
	}
	if requests := model.requestedPreviewRequests(); len(requests) != 0 {
		t.Fatalf("requestedPreviewRequests() before fallback accepted = %+v, want none", requests)
	}

	accepted, _ := model.handleInlineFallbackPrompt(tea.KeyMsg{Type: tea.KeyEnter})
	model = accepted.(Model)
	requests := model.requestedPreviewRequests()
	if len(requests) != 1 || requests[0].Backend != media.BackendChafa {
		t.Fatalf("requestedPreviewRequests() after fallback = %+v, want chafa request", requests)
	}
}

func TestDecliningChafaFallbackPreservesExternalOpen(t *testing.T) {
	prev := inlineFallbackAllowed
	inlineFallbackAllowed = func() bool { return true }
	t.Cleanup(func() { inlineFallbackAllowed = prev })

	var opened store.MediaMetadata
	model := NewModel(Options{
		Config: configWithPreview(24, 6),
		Paths:  testPaths(t),
		PreviewReport: media.Report{
			Selected: media.BackendExternal,
			Reasons: map[media.Backend]string{
				media.BackendExternal: "available",
				media.BackendChafa:    "available",
			},
		},
		OpenMedia: func(media store.MediaMetadata) tea.Cmd {
			opened = media
			return func() tea.Msg {
				return MediaOpenFinishedMsg{Path: media.LocalPath}
			}
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
	model.focus = FocusMessages

	prompting, _ := model.activateFocusedMediaPreview()
	model = prompting.(Model)
	declined, _ := model.handleInlineFallbackPrompt(tea.KeyMsg{Type: tea.KeyEsc})
	model = declined.(Model)
	if !model.inlineFallbackDeclined {
		t.Fatal("inlineFallbackDeclined = false, want declined")
	}

	openedModel, cmd := model.activateFocusedMediaPreview()
	model = openedModel.(Model)
	if cmd == nil {
		t.Fatal("activateFocusedMediaPreview() after decline returned nil command, want external open")
	}
	if opened.LocalPath != "/tmp/photo.jpg" {
		t.Fatalf("opened media = %+v, want local photo", opened)
	}
	if requests := model.requestedPreviewRequests(); len(requests) != 0 {
		t.Fatalf("requestedPreviewRequests() after decline = %+v, want none", requests)
	}
}

func TestShiftEnterOpensFocusedMediaDetached(t *testing.T) {
	var opened store.MediaMetadata
	var foregroundOpened store.MediaMetadata
	model := NewModel(Options{
		Config: configWithPreview(24, 6),
		Paths:  testPaths(t),
		PreviewReport: media.Report{
			Selected: media.BackendChafa,
		},
		OpenMedia: func(item store.MediaMetadata) tea.Cmd {
			foregroundOpened = item
			return func() tea.Msg {
				return MediaOpenFinishedMsg{Path: item.LocalPath}
			}
		},
		OpenMediaDetached: func(item store.MediaMetadata) tea.Cmd {
			opened = item
			return func() tea.Msg {
				return MediaOpenFinishedMsg{Path: item.LocalPath}
			}
		},
		Snapshot: store.Snapshot{
			Chats: []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{
				"chat-1": {{
					ID:     "m-1",
					ChatID: "chat-1",
					Sender: "Alice",
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
	model.focus = FocusMessages

	updated, cmd := model.Update(rawStringMsg("\x1b[13;2u"))
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("Shift+Enter command = nil, want detached open command")
	}
	if opened.LocalPath != "/tmp/photo.jpg" {
		t.Fatalf("detached opened media = %+v, want focused photo", opened)
	}
	if foregroundOpened.LocalPath != "" {
		t.Fatalf("foreground opened media = %+v, want unchanged", foregroundOpened)
	}
	if !strings.Contains(model.status, "opening media externally") {
		t.Fatalf("status = %q, want detached opening status", model.status)
	}
}

func TestNormalOpenMediaShortcutStillUsesForegroundOpen(t *testing.T) {
	var opened store.MediaMetadata
	var detachedOpened store.MediaMetadata
	model := mediaTestModel("/tmp/photo.jpg", media.BackendChafa)
	model.openMedia = func(item store.MediaMetadata) tea.Cmd {
		opened = item
		return func() tea.Msg {
			return MediaOpenFinishedMsg{Path: item.LocalPath}
		}
	}
	model.openMediaDetached = func(item store.MediaMetadata) tea.Cmd {
		detachedOpened = item
		return func() tea.Msg {
			return MediaOpenFinishedMsg{Path: item.LocalPath}
		}
	}

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("o")})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("open media command = nil, want foreground open command")
	}
	if opened.LocalPath != "/tmp/photo.jpg" {
		t.Fatalf("opened media = %+v, want focused photo", opened)
	}
	if detachedOpened.LocalPath != "" {
		t.Fatalf("detached opened media = %+v, want unchanged", detachedOpened)
	}
}

func TestShiftEnterRemoteMediaDownloadsThenOpensDetached(t *testing.T) {
	localPath := filepath.Join(t.TempDir(), "photo.jpg")
	if err := os.WriteFile(localPath, []byte("fake"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	var openedPath string
	var saved store.MediaMetadata
	model := NewModel(Options{
		Config: configWithPreview(24, 6),
		Paths:  testPaths(t),
		PreviewReport: media.Report{
			Selected: media.BackendChafa,
		},
		SaveMedia: func(item store.MediaMetadata) error {
			saved = item
			return nil
		},
		DownloadMedia: func(message store.Message, item store.MediaMetadata) (store.MediaMetadata, error) {
			item.MessageID = message.ID
			item.LocalPath = localPath
			item.DownloadState = "downloaded"
			return item, nil
		},
		OpenMediaDetached: func(item store.MediaMetadata) tea.Cmd {
			return func() tea.Msg {
				openedPath = item.LocalPath
				return MediaOpenFinishedMsg{Path: item.LocalPath}
			}
		},
		Snapshot: store.Snapshot{
			Chats: []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{
				"chat-1": {{
					ID:     "m-1",
					ChatID: "chat-1",
					Sender: "Alice",
					Media: []store.MediaMetadata{{
						MessageID:     "m-1",
						FileName:      "photo.jpg",
						MIMEType:      "image/jpeg",
						DownloadState: "remote",
					}},
				}},
			},
			DraftsByChat: map[string]string{},
			ActiveChatID: "chat-1",
		},
	})
	model.width = 100
	model.height = 20
	model.focus = FocusMessages

	updated, cmd := model.Update(rawStringMsg("\x1b[13;2u"))
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("Shift+Enter command = nil, want download/open command")
	}
	raw := cmd()
	msg, ok := raw.(MediaOpenFinishedMsg)
	if !ok {
		t.Fatalf("cmd() = %T, want MediaOpenFinishedMsg", raw)
	}
	updated, _ = model.Update(msg)
	model = updated.(Model)

	if openedPath != localPath {
		t.Fatalf("openedPath = %q, want %q", openedPath, localPath)
	}
	if got := model.messagesByChat["chat-1"][0].Media[0].LocalPath; got != localPath {
		t.Fatalf("loaded media local path = %q, want %q", got, localPath)
	}
	if saved.LocalPath != localPath {
		t.Fatalf("saved media = %+v, want local path", saved)
	}
	if model.mediaDownloadInflight["m-1"] {
		t.Fatalf("mediaDownloadInflight = %+v, want cleared", model.mediaDownloadInflight)
	}
}

func TestEnterOnRemoteMediaShowsDownloadNotImplemented(t *testing.T) {
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
					Media: []store.MediaMetadata{{
						MessageID:     "m-1",
						FileName:      "photo.jpg",
						MIMEType:      "image/jpeg",
						DownloadState: "remote",
					}},
				}},
			},
			DraftsByChat: map[string]string{},
			ActiveChatID: "chat-1",
		},
	})
	model.width = 100
	model.height = 20
	model.focus = FocusMessages

	updated, _ := model.activateFocusedMediaPreview()
	got := updated.(Model)
	if !strings.Contains(got.status, "not downloaded yet") || !strings.Contains(got.status, "not implemented") {
		t.Fatalf("status = %q, want visible remote download limitation", got.status)
	}
	if requests := got.requestedPreviewRequests(); len(requests) != 0 {
		t.Fatalf("requestedPreviewRequests() = %+v, want none for remote media", requests)
	}
}

func TestEnterOnThumbnailOnlyOverlayMediaDoesNotPreviewLowResolutionThumbnail(t *testing.T) {
	model := NewModel(Options{
		Config: configWithPreview(24, 6),
		Paths:  testPaths(t),
		PreviewReport: media.Report{
			Selected: media.BackendUeberzugPP,
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
						FileName:      "photo.jpg",
						MIMEType:      "image/jpeg",
						ThumbnailPath: "/tmp/wa-thumb.jpg",
						DownloadState: "remote",
					}},
				}},
			},
			DraftsByChat: map[string]string{},
			ActiveChatID: "chat-1",
		},
	})
	model.width = 100
	model.height = 20
	model.focus = FocusMessages

	updated, _ := model.activateFocusedMediaPreview()
	got := updated.(Model)
	if !strings.Contains(got.status, "only has a thumbnail") || !strings.Contains(got.status, "not implemented") {
		t.Fatalf("status = %q, want visible full-media limitation", got.status)
	}
	if requests := got.requestedPreviewRequests(); len(requests) != 0 {
		t.Fatalf("requestedPreviewRequests() = %+v, want no thumbnail-only overlay request", requests)
	}
	if state := got.mediaAttachmentState(got.messagesByChat["chat-1"][0], got.messagesByChat["chat-1"][0].Media[0]); state != "thumbnail only" {
		t.Fatalf("mediaAttachmentState() = %q, want thumbnail only", state)
	}
}

func TestRequestedPreviewRequestsSkipsStaleThumbnailOnlyOverlayRequest(t *testing.T) {
	model := mediaTestModel("", media.BackendUeberzugPP)
	model.messagesByChat["chat-1"][0].Media[0].ThumbnailPath = "/tmp/wa-thumb.jpg"
	message := model.messagesByChat["chat-1"][0]
	item := message.Media[0]
	model.previewRequested = map[string]bool{mediaActivationKey(message, item): true}

	if requests := model.requestedPreviewRequests(); len(requests) != 0 {
		t.Fatalf("requestedPreviewRequests() = %+v, want no stale thumbnail-only overlay request", requests)
	}
}

func TestReplaceMessageMediaPreservesExistingLocalPathWhenUpdateOnlyHasThumbnail(t *testing.T) {
	messages := []store.Message{{
		ID:     "m-1",
		ChatID: "chat-1",
		Sender: "me",
		Media: []store.MediaMetadata{{
			MessageID:     "m-1",
			FileName:      "photo.jpg",
			MIMEType:      "image/jpeg",
			SizeBytes:     2048,
			LocalPath:     "/home/me/photo.jpg",
			DownloadState: "downloaded",
		}},
	}}

	replaced, ok, message := replaceMessageMedia(messages, "m-1", store.MediaMetadata{
		MessageID:     "m-1",
		ThumbnailPath: "/home/me/thumb.jpg",
		DownloadState: "remote",
	})
	if !ok {
		t.Fatal("replaceMessageMedia() ok = false")
	}
	mediaItem := message.Media[0]
	if mediaItem.LocalPath != "/home/me/photo.jpg" || mediaItem.ThumbnailPath != "/home/me/thumb.jpg" || mediaItem.DownloadState != "downloaded" {
		t.Fatalf("message media = %+v, want local path preserved and thumbnail added", mediaItem)
	}
	if replaced[0].Media[0] != mediaItem {
		t.Fatalf("replaced messages = %+v, want same merged media", replaced)
	}
}

func TestEnterOnRemoteMediaDownloadCallbackQueuesPreview(t *testing.T) {
	localPath := filepath.Join(t.TempDir(), "photo.jpg")
	if err := os.WriteFile(localPath, []byte("fake"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
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
		DownloadMedia: func(message store.Message, media store.MediaMetadata) (store.MediaMetadata, error) {
			media.MessageID = message.ID
			media.LocalPath = localPath
			media.DownloadState = "downloaded"
			return media, nil
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
						FileName:      "photo.jpg",
						MIMEType:      "image/jpeg",
						DownloadState: "remote",
					}},
				}},
			},
			DraftsByChat: map[string]string{},
			ActiveChatID: "chat-1",
		},
	})
	model.width = 100
	model.height = 20
	model.focus = FocusMessages

	updated, cmd := model.activateFocusedMediaPreview()
	if cmd == nil {
		t.Fatal("activateFocusedMediaPreview() returned nil command")
	}
	msg := cmd()
	downloaded, ok := msg.(mediaDownloadedMsg)
	if !ok {
		t.Fatalf("cmd() = %T, want mediaDownloadedMsg", msg)
	}
	updated, _ = updated.(Model).handleMediaDownloaded(downloaded)
	got := updated.(Model)
	if got.messagesByChat["chat-1"][0].Media[0].LocalPath != localPath {
		t.Fatalf("media after download = %+v, want local path", got.messagesByChat["chat-1"][0].Media[0])
	}
	if saved.LocalPath != localPath {
		t.Fatalf("saved media = %+v, want local path", saved)
	}
	if requests := got.requestedPreviewRequests(); len(requests) != 1 || requests[0].LocalPath != localPath {
		t.Fatalf("requestedPreviewRequests() = %+v, want downloaded media preview", requests)
	}
}

func TestRemoteMediaDownloadSuppressesDuplicateCommand(t *testing.T) {
	model := NewModel(Options{
		Config: configWithPreview(24, 6),
		Paths:  testPaths(t),
		PreviewReport: media.Report{
			Selected: media.BackendChafa,
		},
		DownloadMedia: func(message store.Message, media store.MediaMetadata) (store.MediaMetadata, error) {
			t.Fatal("DownloadMedia should not be executed in duplicate suppression test")
			return store.MediaMetadata{}, nil
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
						FileName:      "photo.jpg",
						MIMEType:      "image/jpeg",
						DownloadState: "remote",
					}},
				}},
			},
			DraftsByChat: map[string]string{},
			ActiveChatID: "chat-1",
		},
	})
	model.width = 100
	model.height = 20
	model.focus = FocusMessages

	started, cmd := model.activateFocusedMediaPreview()
	model = started.(Model)
	if cmd == nil {
		t.Fatal("first activateFocusedMediaPreview() command = nil, want download command")
	}
	duplicate, duplicateCmd := model.activateFocusedMediaPreview()
	model = duplicate.(Model)
	if duplicateCmd != nil {
		t.Fatalf("duplicate activateFocusedMediaPreview() command = %T, want nil", duplicateCmd)
	}
	if !strings.Contains(model.status, "downloading media") {
		t.Fatalf("status = %q, want downloading media", model.status)
	}
	if !model.mediaDownloadInflight["m-1"] {
		t.Fatalf("mediaDownloadInflight = %+v, want m-1", model.mediaDownloadInflight)
	}

	cleared, _ := model.handleMediaDownloaded(mediaDownloadedMsg{
		MessageID: "m-1",
		Media: store.MediaMetadata{
			MessageID:     "m-1",
			LocalPath:     filepath.Join(t.TempDir(), "missing.jpg"),
			DownloadState: "downloaded",
		},
		Err: fmt.Errorf("download failed"),
	})
	model = cleared.(Model)
	if model.mediaDownloadInflight["m-1"] {
		t.Fatalf("mediaDownloadInflight after completion = %+v, want cleared", model.mediaDownloadInflight)
	}
}

func TestRemoteOpenDownloadUpdatesLoadedMediaAndClearsInflight(t *testing.T) {
	localPath := filepath.Join(t.TempDir(), "photo.jpg")
	if err := os.WriteFile(localPath, []byte("fake"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	var openedPath string
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
		DownloadMedia: func(message store.Message, media store.MediaMetadata) (store.MediaMetadata, error) {
			media.MessageID = message.ID
			media.LocalPath = localPath
			media.DownloadState = "downloaded"
			return media, nil
		},
		OpenMedia: func(media store.MediaMetadata) tea.Cmd {
			return func() tea.Msg {
				openedPath = media.LocalPath
				return MediaOpenFinishedMsg{Path: media.LocalPath}
			}
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
						FileName:      "photo.jpg",
						MIMEType:      "image/jpeg",
						DownloadState: "remote",
					}},
				}},
			},
			DraftsByChat: map[string]string{},
			ActiveChatID: "chat-1",
		},
	})
	model.width = 100
	model.height = 20
	model.focus = FocusMessages

	opening, cmd := model.openFocusedMedia()
	model = opening.(Model)
	if cmd == nil {
		t.Fatal("openFocusedMedia() command = nil, want download/open command")
	}
	raw := cmd()
	msg, ok := raw.(MediaOpenFinishedMsg)
	if !ok {
		t.Fatalf("cmd() = %T, want MediaOpenFinishedMsg", raw)
	}
	opened, _ := model.Update(msg)
	model = opened.(Model)

	if openedPath != localPath {
		t.Fatalf("openedPath = %q, want %q", openedPath, localPath)
	}
	if got := model.messagesByChat["chat-1"][0].Media[0].LocalPath; got != localPath {
		t.Fatalf("loaded media local path = %q, want %q", got, localPath)
	}
	if saved.LocalPath != localPath {
		t.Fatalf("saved media = %+v, want local path", saved)
	}
	if model.mediaDownloadInflight["m-1"] {
		t.Fatalf("mediaDownloadInflight = %+v, want cleared", model.mediaDownloadInflight)
	}
}

func TestActivateFocusedMediaPreviewRepairsStaleManagedCacheAndQueuesDownload(t *testing.T) {
	paths := testPaths(t)
	localPath := filepath.Join(t.TempDir(), "downloaded-photo.jpg")
	if err := os.WriteFile(localPath, []byte("fake"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	stalePath := filepath.Join(paths.MediaDir, "missing-photo.jpg")
	var saved store.MediaMetadata
	model := NewModel(Options{
		Config: configWithPreview(24, 6),
		Paths:  paths,
		PreviewReport: media.Report{
			Selected: media.BackendChafa,
		},
		SaveMedia: func(media store.MediaMetadata) error {
			saved = media
			return nil
		},
		DownloadMedia: func(message store.Message, media store.MediaMetadata) (store.MediaMetadata, error) {
			media.MessageID = message.ID
			media.LocalPath = localPath
			media.DownloadState = "downloaded"
			return media, nil
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
						FileName:      "photo.jpg",
						MIMEType:      "image/jpeg",
						LocalPath:     stalePath,
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
	model.focus = FocusMessages

	updated, cmd := model.activateFocusedMediaPreview()
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("activateFocusedMediaPreview() command = nil, want download command")
	}
	msg := cmd()
	downloaded, ok := msg.(mediaDownloadedMsg)
	if !ok {
		t.Fatalf("cmd() = %T, want mediaDownloadedMsg", msg)
	}
	updated, _ = model.handleMediaDownloaded(downloaded)
	model = updated.(Model)

	if got := model.messagesByChat["chat-1"][0].Media[0].LocalPath; got != localPath {
		t.Fatalf("loaded media local path = %q, want %q", got, localPath)
	}
	if saved.LocalPath != localPath {
		t.Fatalf("saved media = %+v, want repaired downloaded local path", saved)
	}
	if requests := model.requestedPreviewRequests(); len(requests) != 1 || requests[0].LocalPath != localPath {
		t.Fatalf("requestedPreviewRequests() = %+v, want downloaded preview request", requests)
	}
}

func TestOpenFocusedMediaRepairsStaleManagedCacheAndDownloadsAgain(t *testing.T) {
	paths := testPaths(t)
	localPath := filepath.Join(t.TempDir(), "downloaded-photo.jpg")
	if err := os.WriteFile(localPath, []byte("fake"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	stalePath := filepath.Join(paths.MediaDir, "missing-photo.jpg")
	var openedPath string
	var saved store.MediaMetadata
	model := NewModel(Options{
		Config: configWithPreview(24, 6),
		Paths:  paths,
		PreviewReport: media.Report{
			Selected: media.BackendChafa,
		},
		SaveMedia: func(media store.MediaMetadata) error {
			saved = media
			return nil
		},
		DownloadMedia: func(message store.Message, media store.MediaMetadata) (store.MediaMetadata, error) {
			media.MessageID = message.ID
			media.LocalPath = localPath
			media.DownloadState = "downloaded"
			return media, nil
		},
		OpenMedia: func(media store.MediaMetadata) tea.Cmd {
			return func() tea.Msg {
				openedPath = media.LocalPath
				return MediaOpenFinishedMsg{Path: media.LocalPath}
			}
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
						FileName:      "photo.jpg",
						MIMEType:      "image/jpeg",
						LocalPath:     stalePath,
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
	model.focus = FocusMessages

	opening, cmd := model.openFocusedMedia()
	model = opening.(Model)
	if cmd == nil {
		t.Fatal("openFocusedMedia() command = nil, want download/open command")
	}
	raw := cmd()
	msg, ok := raw.(MediaOpenFinishedMsg)
	if !ok {
		t.Fatalf("cmd() = %T, want MediaOpenFinishedMsg", raw)
	}
	opened, _ := model.Update(msg)
	model = opened.(Model)

	if openedPath != localPath {
		t.Fatalf("openedPath = %q, want %q", openedPath, localPath)
	}
	if got := model.messagesByChat["chat-1"][0].Media[0].LocalPath; got != localPath {
		t.Fatalf("loaded media local path = %q, want %q", got, localPath)
	}
	if saved.LocalPath != localPath {
		t.Fatalf("saved media = %+v, want repaired downloaded local path", saved)
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
	message := model.messagesByChat["chat-1"][0]
	request, ok := model.previewRequestForMedia(message, message.Media[0], 0, 0)
	if !ok {
		t.Fatal("previewRequestForMedia() returned false")
	}
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

func TestFailedMediaPreviewRendersInlineStateAndInfo(t *testing.T) {
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
	message := model.messagesByChat["chat-1"][0]
	request, ok := model.previewRequestForMedia(message, message.Media[0], 0, 0)
	if !ok {
		t.Fatal("previewRequestForMedia() returned false")
	}
	model.previewCache[media.PreviewKey(request)] = media.Preview{
		Key:       media.PreviewKey(request),
		MessageID: "m-1",
		Kind:      media.KindImage,
		Backend:   media.BackendChafa,
		Err:       fmt.Errorf("chafa failed"),
	}

	view := stripANSI(model.renderMessages(80, 12))
	if !strings.Contains(view, "preview failed") {
		t.Fatalf("renderMessages missing failed preview state\n%s", view)
	}
	info := stripANSI(model.renderInfo(80))
	if !strings.Contains(info, "error: chafa failed") {
		t.Fatalf("renderInfo missing preview error\n%s", info)
	}
}

func TestFailedChatAvatarPreviewDoesNotOverwriteStatus(t *testing.T) {
	avatarPath := filepath.Join(t.TempDir(), "avatar.jpg")
	model := NewModel(Options{
		Config: configWithPreview(24, 6),
		Paths:  testPaths(t),
		PreviewReport: media.Report{
			Selected: media.BackendUeberzugPP,
			Reasons: map[media.Backend]string{
				media.BackendUeberzugPP: "available",
				media.BackendChafa:      "available",
			},
		},
		Snapshot: store.Snapshot{
			Chats: []store.Chat{{
				ID:         "chat-1",
				Title:      "Alice",
				AvatarPath: avatarPath,
			}},
			DraftsByChat: map[string]string{},
			ActiveChatID: "chat-1",
		},
	})
	model.status = "keep this status"
	request, ok := model.chatAvatarPreviewRequest(model.chats[0])
	if !ok {
		t.Fatal("chatAvatarPreviewRequest() returned false")
	}
	key := media.PreviewKey(request)
	model.previewInflight[key] = true

	updated, _ := model.handleMediaPreviewReady(mediaPreviewReadyMsg{
		Key:        key,
		Generation: model.previewGeneration,
		Request:    request,
		Preview: media.Preview{
			Key:       key,
			MessageID: request.MessageID,
			Kind:      media.KindImage,
			Backend:   media.BackendChafa,
			Err:       fmt.Errorf("chafa failed"),
		},
	})
	got := updated.(Model)
	if got.status != "keep this status" {
		t.Fatalf("status = %q, want unchanged", got.status)
	}
	if got.previewInflight[key] {
		t.Fatalf("previewInflight[%q] remained set", key)
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
	message := model.messagesByChat["chat-1"][0]
	request, ok := model.previewRequestForMedia(message, message.Media[0], 0, 0)
	if !ok {
		t.Fatal("previewRequestForMedia() returned false")
	}
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

func TestEnterPreviewsThenOpensFocusedMedia(t *testing.T) {
	localPath := filepath.Join(t.TempDir(), "photo.jpg")
	if err := os.WriteFile(localPath, []byte("fake"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	var openedPath string
	model := mediaTestModel(localPath, media.BackendChafa)
	model.openMedia = func(item store.MediaMetadata) tea.Cmd {
		return func() tea.Msg {
			openedPath = item.LocalPath
			return MediaOpenFinishedMsg{Path: item.LocalPath}
		}
	}

	previewing, cmd := model.activateFocusedMediaPreview()
	model = previewing.(Model)
	if cmd != nil {
		t.Fatalf("first Enter command = %T, want nil preview queue through model state", cmd)
	}
	requests := model.requestedPreviewRequests()
	if len(requests) != 1 {
		t.Fatalf("requestedPreviewRequests() = %+v, want one preview", requests)
	}
	model.previewCache[media.PreviewKey(requests[0])] = media.Preview{
		Key:             media.PreviewKey(requests[0]),
		MessageID:       "m-1",
		Kind:            media.KindImage,
		Backend:         media.BackendChafa,
		RenderedBackend: media.BackendChafa,
		Display:         media.PreviewDisplayText,
		SourcePath:      localPath,
		Width:           requests[0].Width,
		Height:          requests[0].Height,
		Lines:           []string{"preview"},
	}

	opened, cmd := model.activateFocusedMediaPreview()
	model = opened.(Model)
	if cmd == nil {
		t.Fatal("second Enter command = nil, want open command")
	}
	if msg := cmd(); msg == nil {
		t.Fatal("open command returned nil message")
	}
	if openedPath != localPath {
		t.Fatalf("openedPath = %q, want %q", openedPath, localPath)
	}
}

func TestCopyFocusedImageCopiesLocalMedia(t *testing.T) {
	localPath := filepath.Join(t.TempDir(), "photo.jpg")
	if err := os.WriteFile(localPath, []byte("image"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	model := mediaTestModel(localPath, media.BackendChafa)
	var copiedPath string
	model.copyImageToClipboard = func(item store.MediaMetadata) tea.Cmd {
		return func() tea.Msg {
			copiedPath = item.LocalPath
			return ClipboardImageCopiedMsg{Media: item}
		}
	}

	updated, cmd := model.copyFocusedImage()
	got := updated.(Model)
	if cmd == nil {
		t.Fatal("copyFocusedImage() cmd = nil, want clipboard copy command")
	}
	final, _ := got.Update(cmd())
	got = final.(Model)
	if copiedPath != localPath {
		t.Fatalf("copiedPath = %q, want %q", copiedPath, localPath)
	}
	if !strings.Contains(got.status, "copied image") {
		t.Fatalf("status = %q, want copied image", got.status)
	}
}

func TestCopyFocusedImageDownloadsRemoteMediaBeforeCopy(t *testing.T) {
	localPath := filepath.Join(t.TempDir(), "photo.jpg")
	if err := os.WriteFile(localPath, []byte("image"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	model := mediaTestModel("", media.BackendChafa)
	model.downloadMedia = func(message store.Message, item store.MediaMetadata) (store.MediaMetadata, error) {
		item.LocalPath = localPath
		item.DownloadState = "downloaded"
		return item, nil
	}
	var copiedPath string
	model.copyImageToClipboard = func(item store.MediaMetadata) tea.Cmd {
		return func() tea.Msg {
			copiedPath = item.LocalPath
			return ClipboardImageCopiedMsg{Media: item}
		}
	}

	updated, cmd := model.copyFocusedImage()
	got := updated.(Model)
	if cmd == nil || !got.mediaDownloadInflight["m-1"] {
		t.Fatalf("cmd nil=%v inflight=%v, want download before copy", cmd == nil, got.mediaDownloadInflight)
	}
	final, _ := got.Update(cmd())
	got = final.(Model)
	if copiedPath != localPath {
		t.Fatalf("copiedPath = %q, want %q", copiedPath, localPath)
	}
	if got.mediaDownloadInflight["m-1"] {
		t.Fatalf("mediaDownloadInflight not cleared: %+v", got.mediaDownloadInflight)
	}
	if got.messagesByChat["chat-1"][0].Media[0].LocalPath != localPath {
		t.Fatalf("downloaded local path not applied: %+v", got.messagesByChat["chat-1"][0].Media[0])
	}
}

func TestCopyFocusedImageRejectsNonImageMedia(t *testing.T) {
	model := mediaTestModel("/tmp/report.pdf", media.BackendChafa)
	model.messagesByChat["chat-1"][0].Media[0].MIMEType = "application/pdf"
	model.messagesByChat["chat-1"][0].Media[0].FileName = "report.pdf"

	updated, cmd := model.copyFocusedImage()
	got := updated.(Model)
	if cmd != nil {
		t.Fatal("copyFocusedImage() cmd != nil for non-image media")
	}
	if !strings.Contains(got.status, "not an image") {
		t.Fatalf("status = %q, want non-image status", got.status)
	}
}

func TestCopyFocusedImageReportsNoMedia(t *testing.T) {
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats: []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{
				"chat-1": {{ID: "m-1", ChatID: "chat-1", Body: "hello"}},
			},
			DraftsByChat: map[string]string{},
			ActiveChatID: "chat-1",
		},
	})
	model.focus = FocusMessages

	updated, cmd := model.copyFocusedImage()
	got := updated.(Model)
	if cmd != nil {
		t.Fatal("copyFocusedImage() cmd != nil without media")
	}
	if !strings.Contains(got.status, "no media") {
		t.Fatalf("status = %q, want no media status", got.status)
	}
}

func TestOOpensLocalMediaDirectly(t *testing.T) {
	localPath := filepath.Join(t.TempDir(), "photo.jpg")
	if err := os.WriteFile(localPath, []byte("fake"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	var openedPath string
	model := mediaTestModel(localPath, media.BackendChafa)
	model.openMedia = func(item store.MediaMetadata) tea.Cmd {
		return func() tea.Msg {
			openedPath = item.LocalPath
			return MediaOpenFinishedMsg{Path: item.LocalPath}
		}
	}

	updated, cmd := model.updateNormal(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("o")})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("o command = nil, want open command")
	}
	_ = cmd()
	if openedPath != localPath {
		t.Fatalf("openedPath = %q, want %q", openedPath, localPath)
	}
}

func TestAudioMessageRendersCompactPlayerRow(t *testing.T) {
	model := audioTestModel("/tmp/voice.ogg")

	view := stripANSI(model.renderMessages(80, 12))
	if !strings.Contains(view, "[aud] voice.ogg") || !strings.Contains(view, "enter play") {
		t.Fatalf("audio row missing compact player state\n%s", view)
	}
}

func TestEnterStartsAndStopsFocusedAudio(t *testing.T) {
	var started []*fakeAudioProcess
	model := audioTestModel("/tmp/voice.ogg")
	model.startAudio = func(item store.MediaMetadata) (AudioProcess, error) {
		process := &fakeAudioProcess{}
		started = append(started, process)
		return process, nil
	}

	starting, cmd := model.activateFocusedMediaPreview()
	model = starting.(Model)
	if cmd == nil {
		t.Fatal("activateFocusedMediaPreview() command = nil, want audio start command")
	}
	msg := cmd()
	startedMsg, ok := msg.(audioStartedMsg)
	if !ok {
		t.Fatalf("audio start command = %T, want audioStartedMsg", msg)
	}
	startedModel, waitCmd := model.handleAudioStarted(startedMsg)
	model = startedModel.(Model)
	if waitCmd == nil {
		t.Fatal("handleAudioStarted() wait command = nil")
	}
	if len(started) != 1 || model.audioProcess == nil || !strings.Contains(model.status, "playing audio") {
		t.Fatalf("audio state = process %v status %q started %d", model.audioProcess, model.status, len(started))
	}
	view := stripANSI(model.renderMessages(80, 12))
	if !strings.Contains(view, "playing; enter stop") {
		t.Fatalf("audio row missing playing state\n%s", view)
	}

	stopped, _ := model.activateFocusedMediaPreview()
	model = stopped.(Model)
	if !started[0].stopped {
		t.Fatal("focused audio was not stopped")
	}
	if model.audioProcess != nil || !strings.Contains(model.status, "audio stopped") {
		t.Fatalf("audio after stop = process %v status %q", model.audioProcess, model.status)
	}

	msg = waitCmd()
	finished, ok := msg.(audioFinishedMsg)
	if !ok {
		t.Fatalf("wait command = %T, want audioFinishedMsg", msg)
	}
	handled, _ := model.handleAudioFinished(finished)
	model = handled.(Model)
	if !strings.Contains(model.status, "audio stopped") {
		t.Fatalf("stale finish changed status to %q", model.status)
	}
}

func TestStartingAnotherAudioStopsPreviousProcess(t *testing.T) {
	firstPath := "/tmp/voice-1.ogg"
	secondPath := "/tmp/voice-2.ogg"
	var started []*fakeAudioProcess
	model := audioTestModel(firstPath)
	model.messagesByChat["chat-1"] = append(model.messagesByChat["chat-1"], store.Message{
		ID:     "m-2",
		ChatID: "chat-1",
		Sender: "Alice",
		Media: []store.MediaMetadata{{
			MessageID:     "m-2",
			FileName:      "voice-2.ogg",
			MIMEType:      "audio/ogg",
			LocalPath:     secondPath,
			DownloadState: "downloaded",
		}},
	})
	model.startAudio = func(item store.MediaMetadata) (AudioProcess, error) {
		process := &fakeAudioProcess{}
		started = append(started, process)
		return process, nil
	}

	starting, cmd := model.activateFocusedMediaPreview()
	model = starting.(Model)
	startedModel, _ := model.handleAudioStarted(cmd().(audioStartedMsg))
	model = startedModel.(Model)
	model.messageCursor = 1
	model.messageScrollTop = 1

	starting, cmd = model.activateFocusedMediaPreview()
	model = starting.(Model)
	if len(started) != 1 || !started[0].stopped {
		t.Fatalf("previous process stopped = %v started=%d", len(started) == 1 && started[0].stopped, len(started))
	}
	startedModel, _ = model.handleAudioStarted(cmd().(audioStartedMsg))
	model = startedModel.(Model)
	if len(started) != 2 || model.audioProcess != started[1] {
		t.Fatalf("second audio state = process %v started %d", model.audioProcess, len(started))
	}
}

func TestAudioCompletionClearsPlaybackState(t *testing.T) {
	model := audioTestModel("/tmp/voice.ogg")
	model.startAudio = func(item store.MediaMetadata) (AudioProcess, error) {
		return &fakeAudioProcess{}, nil
	}

	starting, cmd := model.activateFocusedMediaPreview()
	model = starting.(Model)
	startedModel, waitCmd := model.handleAudioStarted(cmd().(audioStartedMsg))
	model = startedModel.(Model)

	msg := waitCmd()
	finished, ok := msg.(audioFinishedMsg)
	if !ok {
		t.Fatalf("wait command = %T, want audioFinishedMsg", msg)
	}
	handled, _ := model.handleAudioFinished(finished)
	model = handled.(Model)
	if model.audioProcess != nil || model.audioMediaKey != "" || !strings.Contains(model.status, "audio finished") {
		t.Fatalf("audio completion state = process %v key %q status %q", model.audioProcess, model.audioMediaKey, model.status)
	}
}

func TestRemoteAudioEnterShowsDownloadLimitation(t *testing.T) {
	model := audioTestModel("")

	updated, cmd := model.activateFocusedMediaPreview()
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("activateFocusedMediaPreview() command = %T, want nil", cmd)
	}
	if !strings.Contains(got.status, "not downloaded yet") || !strings.Contains(got.status, "not implemented") {
		t.Fatalf("status = %q, want visible remote download limitation", got.status)
	}
}

func TestRemoteAudioDownloadThenStartsPlayback(t *testing.T) {
	localPath := filepath.Join(t.TempDir(), "voice.ogg")
	if err := os.WriteFile(localPath, []byte("audio"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	var saved store.MediaMetadata
	var startedPath string
	model := audioTestModel("")
	model.saveMedia = func(media store.MediaMetadata) error {
		saved = media
		return nil
	}
	model.downloadMedia = func(message store.Message, media store.MediaMetadata) (store.MediaMetadata, error) {
		media.MessageID = message.ID
		media.LocalPath = localPath
		media.DownloadState = "downloaded"
		return media, nil
	}
	model.startAudio = func(item store.MediaMetadata) (AudioProcess, error) {
		startedPath = item.LocalPath
		return &fakeAudioProcess{}, nil
	}

	starting, cmd := model.activateFocusedMediaPreview()
	model = starting.(Model)
	if cmd == nil {
		t.Fatal("activateFocusedMediaPreview() command = nil, want download/start command")
	}
	startedModel, _ := model.handleAudioStarted(cmd().(audioStartedMsg))
	model = startedModel.(Model)

	if got := model.messagesByChat["chat-1"][0].Media[0].LocalPath; got != localPath {
		t.Fatalf("loaded media local path = %q, want %q", got, localPath)
	}
	if saved.LocalPath != localPath {
		t.Fatalf("saved media = %+v, want local path", saved)
	}
	if startedPath != localPath || model.audioProcess == nil {
		t.Fatalf("started path = %q process=%v", startedPath, model.audioProcess)
	}
}

func TestLeaderSavesFocusedAudioWithCollisionSafeName(t *testing.T) {
	dir := t.TempDir()
	localPath := filepath.Join(dir, "source.ogg")
	if err := os.WriteFile(localPath, []byte("audio"), 0o644); err != nil {
		t.Fatalf("WriteFile(source) error = %v", err)
	}
	downloads := filepath.Join(dir, "downloads")
	if err := os.MkdirAll(downloads, 0o755); err != nil {
		t.Fatalf("MkdirAll(downloads) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(downloads, "voice.ogg"), []byte("existing"), 0o644); err != nil {
		t.Fatalf("WriteFile(existing) error = %v", err)
	}
	model := audioTestModel(localPath)
	model.config.DownloadsDir = downloads

	leader, _ := model.updateNormal(tea.KeyMsg{Type: tea.KeySpace})
	model = leader.(Model)
	saved, cmd := model.updateNormal(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	model = saved.(Model)
	if cmd == nil {
		t.Fatal("<leader>s command = nil, want save command")
	}
	msg := cmd()
	savedMsg, ok := msg.(mediaSavedMsg)
	if !ok {
		t.Fatalf("save command = %T, want mediaSavedMsg", msg)
	}
	handled, _ := model.handleMediaSaved(savedMsg)
	model = handled.(Model)

	want := filepath.Join(downloads, "voice-1.ogg")
	if savedMsg.Path != want {
		t.Fatalf("saved path = %q, want %q", savedMsg.Path, want)
	}
	data, err := os.ReadFile(want)
	if err != nil {
		t.Fatalf("ReadFile(saved) error = %v", err)
	}
	if string(data) != "audio" || !strings.Contains(model.status, "saved media") {
		t.Fatalf("saved data/status = %q/%q", data, model.status)
	}
}

func TestLeaderSavesFocusedMediaWithCollisionSafeName(t *testing.T) {
	dir := t.TempDir()
	localPath := filepath.Join(dir, "source.jpg")
	if err := os.WriteFile(localPath, []byte("image"), 0o644); err != nil {
		t.Fatalf("WriteFile(source) error = %v", err)
	}
	downloads := filepath.Join(dir, "downloads")
	if err := os.MkdirAll(downloads, 0o755); err != nil {
		t.Fatalf("MkdirAll(downloads) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(downloads, "photo.jpg"), []byte("existing"), 0o644); err != nil {
		t.Fatalf("WriteFile(existing) error = %v", err)
	}
	model := mediaTestModel(localPath, media.BackendChafa)
	model.config.DownloadsDir = downloads

	leader, _ := model.updateNormal(tea.KeyMsg{Type: tea.KeySpace})
	model = leader.(Model)
	if !model.leaderPending || !strings.Contains(model.status, "leader") {
		t.Fatalf("leader state = pending %v status %q", model.leaderPending, model.status)
	}
	saved, cmd := model.updateNormal(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	model = saved.(Model)
	if cmd == nil {
		t.Fatal("<leader>s command = nil, want save command")
	}
	msg, ok := cmd().(mediaSavedMsg)
	if !ok {
		t.Fatalf("save cmd message = %T, want mediaSavedMsg", cmd())
	}
	handled, _ := model.handleMediaSaved(msg)
	model = handled.(Model)

	want := filepath.Join(downloads, "photo-1.jpg")
	if msg.Path != want {
		t.Fatalf("saved path = %q, want %q", msg.Path, want)
	}
	data, err := os.ReadFile(want)
	if err != nil {
		t.Fatalf("ReadFile(saved) error = %v", err)
	}
	if string(data) != "image" {
		t.Fatalf("saved data = %q, want image", data)
	}
	if !strings.Contains(model.status, "saved media") {
		t.Fatalf("status = %q, want saved media", model.status)
	}
}

func TestLeaderHFClearsLoadedMediaPreviews(t *testing.T) {
	localPath := filepath.Join(t.TempDir(), "photo.jpg")
	if err := os.WriteFile(localPath, []byte("image"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	model := mediaTestModel(localPath, media.BackendUeberzugPP)
	cacheOverlayPreview(t, &model, localPath)
	var overlay bytes.Buffer
	model.overlay = media.NewOverlayManagerForWriter(&overlay)
	staleOverlayCmd := model.syncOverlayCmd()
	if staleOverlayCmd == nil {
		t.Fatal("syncOverlayCmd() = nil, want stale overlay add command")
	}
	message := model.messagesByChat["chat-1"][0]
	item := message.Media[0]
	model.previewRequested[mediaActivationKey(message, item)] = true
	request, ok := model.previewRequestForMedia(message, item, 0, 0)
	if !ok {
		t.Fatal("previewRequestForMedia() returned false")
	}
	staleGeneration := model.previewGeneration
	staleKey := media.PreviewKey(request)
	model.previewInflight[staleKey] = true

	leader, _ := model.updateNormal(tea.KeyMsg{Type: tea.KeySpace})
	model = leader.(Model)
	prefix, _ := model.updateNormal(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("h")})
	model = prefix.(Model)
	if !model.leaderPending || model.leaderSequence != "h" {
		t.Fatalf("leader prefix = pending %v sequence %q", model.leaderPending, model.leaderSequence)
	}
	cleared, _ := model.updateNormal(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f")})
	model = cleared.(Model)

	if model.leaderPending || model.leaderSequence != "" {
		t.Fatalf("leader after clear = pending %v sequence %q", model.leaderPending, model.leaderSequence)
	}
	if len(model.previewCache) != 0 || len(model.previewInflight) != 0 || len(model.previewRequested) != 0 {
		t.Fatalf("preview state after <leader>hf = cache %d inflight %d requested %d, want empty", len(model.previewCache), len(model.previewInflight), len(model.previewRequested))
	}
	if !strings.Contains(model.status, "unloaded") {
		t.Fatalf("status = %q, want unloaded status", model.status)
	}

	updated, _ := model.handleMediaPreviewReady(mediaPreviewReadyMsg{
		Key:        staleKey,
		Generation: staleGeneration,
		Request:    request,
		Preview: media.Preview{
			Key:             staleKey,
			MessageID:       "m-1",
			Kind:            media.KindImage,
			Backend:         media.BackendUeberzugPP,
			RenderedBackend: media.BackendUeberzugPP,
			Display:         media.PreviewDisplayOverlay,
			SourceKind:      media.SourceLocal,
			SourcePath:      localPath,
			Width:           request.Width,
			Height:          request.Height,
		},
	})
	model = updated.(Model)
	if len(model.previewCache) != 0 || strings.Contains(model.status, "preview ready") {
		t.Fatalf("stale preview completion restored preview state: cache=%d status=%q", len(model.previewCache), model.status)
	}

	if msg := staleOverlayCmd(); msg == nil {
		t.Fatal("stale overlay command returned nil message")
	}
	if strings.Contains(overlay.String(), `"action":"add"`) {
		t.Fatalf("stale overlay command re-added preview:\n%s", overlay.String())
	}
}

func TestSyncOverlayCmdSkipsUnchangedPlacements(t *testing.T) {
	localPath := filepath.Join(t.TempDir(), "photo.jpg")
	if err := os.WriteFile(localPath, []byte("image"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	model := mediaTestModel(localPath, media.BackendUeberzugPP)
	cacheOverlayPreview(t, &model, localPath)
	var overlay bytes.Buffer
	model.overlay = media.NewOverlayManagerForWriter(&overlay)

	first := model.syncOverlayCmd()
	if first == nil {
		t.Fatal("first syncOverlayCmd() = nil, want overlay add command")
	}
	if !model.overlaySyncPending || model.overlayPendingSignature == "" {
		t.Fatal("overlay sync was not marked pending after first syncOverlayCmd()")
	}
	second := model.syncOverlayCmd()
	if second != nil {
		t.Fatal("second syncOverlayCmd() returned a command for unchanged placements")
	}

	model = applyOverlayCmd(t, model, first)
	if !strings.Contains(overlay.String(), `"action":"add"`) {
		t.Fatalf("first overlay command did not add preview:\n%s", overlay.String())
	}
	overlay.Reset()
	third := model.syncOverlayCmd()
	if third != nil {
		t.Fatal("third syncOverlayCmd() returned a command after unchanged placement was active")
	}
	if overlay.Len() != 0 {
		t.Fatalf("unchanged overlay sync wrote output:\n%s", overlay.String())
	}
}

func TestPausedOverlayClearDoesNotRaceAfterResume(t *testing.T) {
	localPath := filepath.Join(t.TempDir(), "photo.jpg")
	if err := os.WriteFile(localPath, []byte("image"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	model := mediaTestModel(localPath, media.BackendUeberzugPP)
	cacheOverlayPreview(t, &model, localPath)
	var overlay bytes.Buffer
	model.overlay = media.NewOverlayManagerForWriter(&overlay)

	initial := model.syncOverlayCmd()
	if initial == nil {
		t.Fatal("initial syncOverlayCmd() = nil, want add command")
	}
	model = applyOverlayCmd(t, model, initial)
	overlay.Reset()

	model.pauseOverlays(true, false)
	staleClear := model.syncOverlayCmd()
	if staleClear == nil {
		t.Fatal("paused syncOverlayCmd() = nil, want epoch-scoped clear command")
	}
	resumed, resumeCmd := model.Update(overlayResumeMsg{Generation: model.overlayPauseGeneration})
	model = resumed.(Model)
	if !model.mediaOverlayPaused {
		t.Fatal("mediaOverlayPaused = false before pause clear applied")
	}
	if resumeCmd != nil {
		t.Fatal("resume command queued before pause clear applied")
	}

	msg := staleClear()
	clearMsg, ok := msg.(mediaOverlayMsg)
	if !ok {
		t.Fatalf("stale clear command message = %T, want mediaOverlayMsg", msg)
	}
	updated, resumeCmd := model.Update(clearMsg)
	model = updated.(Model)
	if model.overlaySyncPending {
		t.Fatal("overlaySyncPending = true after pause clear applied")
	}
	if resumeCmd == nil {
		t.Fatal("resume command = nil after pause clear applied")
	}
	resumed, _ = model.Update(overlayResumeMsg{Generation: model.overlayPauseGeneration})
	model = resumed.(Model)
	if model.mediaOverlayPaused {
		t.Fatal("mediaOverlayPaused = true after serialized resume")
	}
	overlay.Reset()

	if msg := staleClear(); msg == nil {
		t.Fatal("stale clear command returned nil message")
	}
	if overlay.Len() != 0 {
		t.Fatalf("stale pause clear wrote overlay commands after resume:\n%s", overlay.String())
	}
}

func TestOverlayResumeResyncsWhenPauseClearAppliedFirst(t *testing.T) {
	localPath := filepath.Join(t.TempDir(), "photo.jpg")
	if err := os.WriteFile(localPath, []byte("image"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	model := mediaTestModel(localPath, media.BackendUeberzugPP)
	cacheOverlayPreview(t, &model, localPath)
	var overlay bytes.Buffer
	model.overlay = media.NewOverlayManagerForWriter(&overlay)

	initial := model.syncOverlayCmd()
	if initial == nil {
		t.Fatal("initial syncOverlayCmd() = nil, want add command")
	}
	model = applyOverlayCmd(t, model, initial)
	overlay.Reset()

	model.pauseOverlays(true, false)
	clearCmd := model.syncOverlayCmd()
	if clearCmd == nil {
		t.Fatal("paused syncOverlayCmd() = nil, want clear command")
	}
	model = applyOverlayCmd(t, model, clearCmd)
	if !strings.Contains(overlay.String(), `"action":"remove"`) {
		t.Fatalf("pause clear did not remove overlay:\n%s", overlay.String())
	}
	if model.overlaySignature != "" {
		t.Fatalf("overlaySignature = %q, want cleared after applied pause clear", model.overlaySignature)
	}
	overlay.Reset()

	resumed, resumeCmd := model.Update(overlayResumeMsg{Generation: model.overlayPauseGeneration})
	model = resumed.(Model)
	if resumeCmd == nil {
		t.Fatal("resume command = nil, want overlay add after applied pause clear")
	}
	model = applyOverlayCmd(t, model, resumeCmd)
	if !strings.Contains(overlay.String(), `"action":"add"`) {
		t.Fatalf("resume did not re-add overlay:\n%s", overlay.String())
	}
	if model.overlaySignature == "" {
		t.Fatal("overlaySignature is empty after applied resume")
	}
}

func TestRepeatedOverlayPauseDoesNotInvalidatePendingClear(t *testing.T) {
	localPath := filepath.Join(t.TempDir(), "photo.jpg")
	if err := os.WriteFile(localPath, []byte("image"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	model := mediaTestModel(localPath, media.BackendUeberzugPP)
	cacheOverlayPreview(t, &model, localPath)
	var overlay bytes.Buffer
	model.overlay = media.NewOverlayManagerForWriter(&overlay)

	initial := model.syncOverlayCmd()
	if initial == nil {
		t.Fatal("initial syncOverlayCmd() = nil, want add command")
	}
	model = applyOverlayCmd(t, model, initial)
	overlay.Reset()

	model.pauseOverlays(true, false)
	clearCmd := model.syncOverlayCmd()
	if clearCmd == nil {
		t.Fatal("paused syncOverlayCmd() = nil, want clear command")
	}
	model.pauseOverlays(true, false)
	if msg := clearCmd(); msg == nil {
		t.Fatal("clear command returned nil message")
	}
	if !strings.Contains(overlay.String(), `"action":"remove"`) {
		t.Fatalf("repeated pause invalidated pending clear:\n%s", overlay.String())
	}
}

func TestSyncOverlayCmdSkipsEmptyPlacementsWithoutManager(t *testing.T) {
	model := mediaTestModel(filepath.Join(t.TempDir(), "photo.jpg"), media.BackendUeberzugPP)

	if cmd := model.syncOverlayCmd(); cmd != nil {
		t.Fatal("syncOverlayCmd() returned a command without visible placements")
	}
	if model.overlay != nil {
		t.Fatal("syncOverlayCmd() created an overlay manager without visible placements")
	}
	if model.overlaySignature != "" {
		t.Fatalf("overlaySignature = %q, want empty", model.overlaySignature)
	}
}

func TestSyncOverlayCmdIncludesChatAvatarPlacements(t *testing.T) {
	avatarPath := filepath.Join(t.TempDir(), "avatar.jpg")
	model := NewModel(Options{
		Paths: testPaths(t),
		PreviewReport: media.Report{
			Selected: media.BackendUeberzugPP,
			Reasons: map[media.Backend]string{
				media.BackendUeberzugPP: "available",
			},
		},
		Snapshot: store.Snapshot{
			Chats: []store.Chat{{
				ID:         "chat-1",
				Title:      "Alice",
				AvatarPath: avatarPath,
			}},
			DraftsByChat: map[string]string{},
			ActiveChatID: "chat-1",
		},
	})
	model.width = 120
	model.height = 20
	request, ok := model.chatAvatarPreviewRequest(model.chats[0])
	if !ok {
		t.Fatal("chatAvatarPreviewRequest() returned false")
	}
	model.previewCache[media.PreviewKey(request)] = media.Preview{
		Key:             media.PreviewKey(request),
		MessageID:       request.MessageID,
		Kind:            media.KindImage,
		Backend:         media.BackendUeberzugPP,
		RenderedBackend: media.BackendUeberzugPP,
		Display:         media.PreviewDisplayOverlay,
		SourceKind:      media.SourceLocal,
		SourcePath:      avatarPath,
		Width:           request.Width,
		Height:          request.Height,
	}
	var overlay bytes.Buffer
	model.overlay = media.NewOverlayManagerForWriter(&overlay)

	cmd := model.syncOverlayCmd()
	if cmd == nil {
		t.Fatal("syncOverlayCmd() = nil, want avatar overlay command")
	}
	msg := cmd()
	if _, ok := msg.(mediaOverlayMsg); !ok {
		t.Fatalf("overlay command message = %T, want mediaOverlayMsg", msg)
	}
	if !strings.Contains(overlay.String(), strconv.Quote(avatarPath)) || !strings.Contains(overlay.String(), `"max_width":4`) || !strings.Contains(overlay.String(), `"max_height":2`) {
		t.Fatalf("avatar overlay command missing avatar placement:\n%s", overlay.String())
	}
}

func TestOverlayErrorResetsManagerAndQueuesSingleRetry(t *testing.T) {
	localPath := filepath.Join(t.TempDir(), "photo.jpg")
	if err := os.WriteFile(localPath, []byte("image"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	model := mediaTestModel(localPath, media.BackendUeberzugPP)
	cacheOverlayPreview(t, &model, localPath)
	failedOverlay := model.overlay

	cmd := model.syncOverlayCmd()
	if cmd == nil {
		t.Fatal("syncOverlayCmd() = nil, want overlay command")
	}
	pendingSignature := model.overlayPendingSignature
	updated, retryCmd := model.Update(mediaOverlayMsg{
		Signature: pendingSignature,
		Err:       errors.New("ueberzug timeout"),
	})
	model = updated.(Model)
	if retryCmd == nil {
		t.Fatal("overlay error retry command = nil, want cleanup plus retry")
	}
	if model.overlay == nil || model.overlay == failedOverlay {
		t.Fatal("overlay manager was not replaced after sync failure")
	}
	if !model.overlaySyncPending || model.overlayPendingSignature == "" {
		t.Fatalf("overlay pending = %v signature %q, want queued retry", model.overlaySyncPending, model.overlayPendingSignature)
	}
	if model.overlayConsecutiveFailures != 1 {
		t.Fatalf("overlayConsecutiveFailures = %d, want 1", model.overlayConsecutiveFailures)
	}
	if !strings.Contains(model.status, "overlay failed") {
		t.Fatalf("status = %q, want overlay failure", model.status)
	}
}

func TestSyncOverlayCmdClearsWhileSyncOverlayVisible(t *testing.T) {
	localPath := filepath.Join(t.TempDir(), "photo.jpg")
	if err := os.WriteFile(localPath, []byte("image"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	model := mediaTestModel(localPath, media.BackendUeberzugPP)
	cacheOverlayPreview(t, &model, localPath)
	var overlay bytes.Buffer
	model.overlay = media.NewOverlayManagerForWriter(&overlay)
	first := model.syncOverlayCmd()
	if first == nil {
		t.Fatal("syncOverlayCmd() = nil, want initial add command")
	}
	model = applyOverlayCmd(t, model, first)
	overlay.Reset()
	model.syncOverlay.Visible = true

	clearCmd := model.syncOverlayCmd()
	if clearCmd == nil {
		t.Fatal("syncOverlayCmd() = nil, want clear command while sync overlay is visible")
	}
	model = applyOverlayCmd(t, model, clearCmd)
	if !strings.Contains(overlay.String(), `"action":"remove"`) {
		t.Fatalf("clear overlay command did not remove overlays:\n%s", overlay.String())
	}
	if model.overlaySignature != "" {
		t.Fatalf("overlaySignature = %q, want empty", model.overlaySignature)
	}
}

func TestLeaderPrefixConsumesHWithoutMovingFocus(t *testing.T) {
	model := mediaTestModel(filepath.Join(t.TempDir(), "photo.jpg"), media.BackendChafa)
	model.focus = FocusMessages

	leaderModel, _ := model.Update(tea.KeyMsg{Type: tea.KeySpace})
	model = leaderModel.(Model)
	prefixModel, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("h")})
	model = prefixModel.(Model)

	if model.focus != FocusMessages {
		t.Fatalf("focus = %s, want messages after <leader>h", model.focus)
	}
	if !model.leaderPending || model.leaderSequence != "h" {
		t.Fatalf("leader after h = pending %v sequence %q, want pending h", model.leaderPending, model.leaderSequence)
	}

	cancelled, _ := model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = cancelled.(Model)
	if model.leaderPending || model.leaderSequence != "" || !strings.Contains(model.status, "cancelled") {
		t.Fatalf("leader after esc = pending %v sequence %q status %q", model.leaderPending, model.leaderSequence, model.status)
	}
}

func TestCustomNormalMovementAndOpenKeys(t *testing.T) {
	keymap := config.DefaultKeymap()
	keymap.NormalMoveDown = "x"
	keymap.NormalMoveUp = "z"
	keymap.NormalOpen = "e"
	model := NewModel(Options{
		Config: config.Config{LeaderKey: "space", Keymap: keymap},
		Snapshot: store.Snapshot{
			Chats: []store.Chat{
				{ID: "chat-1", Title: "Alice"},
				{ID: "chat-2", Title: "Bob"},
			},
			MessagesByChat: map[string][]store.Message{
				"chat-1": nil,
				"chat-2": nil,
			},
		},
	})

	unchanged, _ := model.updateNormal(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	model = unchanged.(Model)
	if model.activeChat != 0 {
		t.Fatalf("default j moved active chat with custom keymap: %d", model.activeChat)
	}

	moved, _ := model.updateNormal(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	model = moved.(Model)
	if model.activeChat != 1 {
		t.Fatalf("activeChat after x = %d, want 1", model.activeChat)
	}

	up, _ := model.updateNormal(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("z")})
	model = up.(Model)
	if model.activeChat != 0 {
		t.Fatalf("activeChat after z = %d, want 0", model.activeChat)
	}

	opened, _ := model.updateNormal(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	model = opened.(Model)
	if model.focus != FocusMessages || !strings.Contains(model.status, "opened Alice") {
		t.Fatalf("open state = focus %s status %q, want messages/opened", model.focus, model.status)
	}
}

func TestCustomLeaderSequenceClearsMediaPreviews(t *testing.T) {
	localPath := filepath.Join(t.TempDir(), "photo.jpg")
	if err := os.WriteFile(localPath, []byte("image"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	model := mediaTestModel(localPath, media.BackendChafa)
	model.config.Keymap.NormalUnloadPreviews = "leader u p"
	cacheOverlayPreview(t, &model, localPath)
	if len(model.previewCache) == 0 {
		t.Fatal("preview cache is empty before custom unload key")
	}

	leader, _ := model.updateNormal(tea.KeyMsg{Type: tea.KeySpace})
	model = leader.(Model)
	prefix, _ := model.updateNormal(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("u")})
	model = prefix.(Model)
	if !model.leaderPending || model.leaderSequence != "u" {
		t.Fatalf("leader prefix = pending %v sequence %q, want pending u", model.leaderPending, model.leaderSequence)
	}
	cleared, _ := model.updateNormal(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("p")})
	model = cleared.(Model)
	if len(model.previewCache) != 0 || !strings.Contains(model.status, "unloaded") {
		t.Fatalf("custom leader clear = cache %d status %q", len(model.previewCache), model.status)
	}
}

func TestLeaderSequenceCanRunAnyNormalAction(t *testing.T) {
	keymap := config.DefaultKeymap()
	keymap.NormalOpen = "leader e"
	model := NewModel(Options{
		Config: config.Config{LeaderKey: "space", Keymap: keymap},
		Snapshot: store.Snapshot{
			Chats:          []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{"chat-1": nil},
		},
	})

	leader, _ := model.updateNormal(tea.KeyMsg{Type: tea.KeySpace})
	model = leader.(Model)
	opened, _ := model.updateNormal(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	model = opened.(Model)
	if model.focus != FocusMessages || !strings.Contains(model.status, "opened Alice") {
		t.Fatalf("leader open = focus %s status %q", model.focus, model.status)
	}
}

func TestCustomInsertSendAndNewlineKeys(t *testing.T) {
	keymap := config.DefaultKeymap()
	keymap.InsertSend = "ctrl+s"
	keymap.InsertNewline = "ctrl+n"
	model := NewModel(Options{
		Config: config.Config{LeaderKey: "space", Keymap: keymap},
		Snapshot: store.Snapshot{
			Chats:          []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{"chat-1": nil},
			DraftsByChat:   map[string]string{},
			ActiveChatID:   "chat-1",
		},
	})
	model.mode = ModeInsert
	model.composer = "hello"

	enter, _ := model.updateInsert(tea.KeyMsg{Type: tea.KeyEnter})
	model = enter.(Model)
	if len(model.messagesByChat["chat-1"]) != 0 || model.composer != "hello" {
		t.Fatalf("enter sent or changed composer with custom send key: messages=%d composer=%q", len(model.messagesByChat["chat-1"]), model.composer)
	}

	newlined, _ := model.updateInsert(tea.KeyMsg{Type: tea.KeyCtrlN})
	model = newlined.(Model)
	if model.composer != "hello\n" {
		t.Fatalf("composer after ctrl+n = %q, want newline", model.composer)
	}

	sent, _ := model.updateInsert(tea.KeyMsg{Type: tea.KeyCtrlS})
	model = sent.(Model)
	if len(model.messagesByChat["chat-1"]) != 1 || strings.TrimSpace(model.composer) != "" {
		t.Fatalf("ctrl+s send = messages=%d composer=%q", len(model.messagesByChat["chat-1"]), model.composer)
	}
}

func TestShiftEnterAddsComposerNewline(t *testing.T) {
	tests := []struct {
		name string
		msg  tea.Msg
	}{
		{name: "kitty csi u", msg: rawStringMsg("\x1b[13;2u")},
		{name: "tilde sequence", msg: rawStringMsg("\x1b[13;2~")},
		{name: "modify other keys", msg: rawStringMsg("\x1b[27;2;13~")},
		{name: "bubble tea unknown csi u", msg: rawStringMsg("?CSI[49 51 59 50 117]?")},
		{name: "bubble tea unknown tilde", msg: rawStringMsg("?CSI[49 51 59 50 126]?")},
		{name: "bubble tea unknown modify other keys", msg: rawStringMsg("?CSI[50 55 59 50 59 49 51 126]?")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model := NewModel(Options{
				Snapshot: store.Snapshot{
					Chats:          []store.Chat{{ID: "chat-1", Title: "Alice"}},
					MessagesByChat: map[string][]store.Message{"chat-1": nil},
					DraftsByChat:   map[string]string{},
					ActiveChatID:   "chat-1",
				},
			})
			model.mode = ModeInsert
			model.composer = "hello"

			updated, _ := model.Update(tt.msg)
			model = updated.(Model)
			if model.composer != "hello\n" {
				t.Fatalf("composer after shift+enter = %q, want newline", model.composer)
			}
			if got := len(model.messagesByChat["chat-1"]); got != 0 {
				t.Fatalf("shift+enter sent %d messages, want 0", got)
			}
		})
	}
}

func TestAltEnterCanBeConfiguredForComposerNewline(t *testing.T) {
	keymap := config.DefaultKeymap()
	keymap.InsertNewlineAlt = "alt+enter"
	model := NewModel(Options{
		Config: config.Config{LeaderKey: "space", Keymap: keymap},
		Snapshot: store.Snapshot{
			Chats:          []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{"chat-1": nil},
			DraftsByChat:   map[string]string{},
			ActiveChatID:   "chat-1",
		},
	})
	model.mode = ModeInsert
	model.composer = "hello"

	updated, _ := model.updateInsert(tea.KeyMsg{Type: tea.KeyEnter, Alt: true})
	model = updated.(Model)
	if model.composer != "hello\n" {
		t.Fatalf("composer after alt+enter = %q, want newline", model.composer)
	}
	if got := len(model.messagesByChat["chat-1"]); got != 0 {
		t.Fatalf("alt+enter sent %d messages, want 0", got)
	}
}

func TestPlainEnterStillSendsComposer(t *testing.T) {
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats:          []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{"chat-1": nil},
			DraftsByChat:   map[string]string{},
			ActiveChatID:   "chat-1",
		},
	})
	model.mode = ModeInsert
	model.composer = "hello"

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if got := len(model.messagesByChat["chat-1"]); got != 1 {
		t.Fatalf("plain enter sent %d messages, want 1", got)
	}
	if strings.TrimSpace(model.composer) != "" {
		t.Fatalf("composer after plain enter = %q, want empty", model.composer)
	}
}

func TestCustomVisualCommandAndSearchKeys(t *testing.T) {
	keymap := config.DefaultKeymap()
	keymap.VisualMoveDown = "x"
	keymap.VisualMoveUp = "z"
	keymap.VisualYank = "c"
	keymap.CommandRun = "!"
	keymap.SearchRun = "!"
	model := NewModel(Options{
		Config: config.Config{LeaderKey: "space", Keymap: keymap},
		Snapshot: store.Snapshot{
			Chats: []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{"chat-1": {
				{ID: "m-1", ChatID: "chat-1", Body: "hello one"},
				{ID: "m-2", ChatID: "chat-1", Body: "hello two"},
			}},
			ActiveChatID: "chat-1",
		},
	})
	model.mode = ModeVisual
	model.focus = FocusMessages
	model.visualAnchor = 0

	moved, _ := model.updateVisual(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	model = moved.(Model)
	if model.messageCursor != 1 {
		t.Fatalf("visual cursor after x = %d, want 1", model.messageCursor)
	}
	yanked, _ := model.updateVisual(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	model = yanked.(Model)
	if model.mode != ModeNormal || !strings.Contains(model.yankRegister, "hello two") {
		t.Fatalf("visual yank = mode %s register %q", model.mode, model.yankRegister)
	}

	model.mode = ModeCommand
	model.commandLine = "help"
	commanded, _ := model.updateCommand(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("!")})
	model = commanded.(Model)
	if !model.helpVisible {
		t.Fatal("custom command run key did not execute :help")
	}

	model.helpVisible = false
	model.mode = ModeSearch
	model.focus = FocusMessages
	model.searchLine = "two"
	searched, _ := model.updateSearch(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("!")})
	model = searched.(Model)
	if model.mode != ModeNormal || model.activeSearch != "two" || model.messageCursor != 1 {
		t.Fatalf("custom search run = mode %s active %q cursor %d", model.mode, model.activeSearch, model.messageCursor)
	}
}

func TestVisualForwardPickerSelectsRecipientsAndSends(t *testing.T) {
	var queued ForwardMessagesRequest
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats: []store.Chat{
				{ID: "chat-1", Title: "Alice"},
				{ID: "chat-2", Title: "Bob"},
				{ID: "chat-3", Title: "Carol"},
			},
			MessagesByChat: map[string][]store.Message{"chat-1": {
				{ID: "m-1", ChatID: "chat-1", Body: "one"},
				{ID: "m-2", ChatID: "chat-1", Body: "two"},
			}},
			DraftsByChat: map[string]string{},
			ActiveChatID: "chat-1",
		},
		ForwardMessages: func(request ForwardMessagesRequest) tea.Cmd {
			queued = request
			return func() tea.Msg {
				return ForwardMessagesFinishedMsg{Sent: len(request.Messages) * len(request.Recipients)}
			}
		},
	})
	model.mode = ModeVisual
	model.focus = FocusMessages
	model.visualAnchor = 0
	model.messageCursor = 1

	forwarding, cmd := model.updateVisual(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f")})
	model = forwarding.(Model)
	if cmd != nil || model.mode != ModeForward || len(model.forwardSourceMessages) != 2 {
		t.Fatalf("forward picker state = mode %s sources %d cmd %T", model.mode, len(model.forwardSourceMessages), cmd)
	}
	moved, _ := model.updateForward(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	model = moved.(Model)
	toggled, _ := model.updateForward(tea.KeyMsg{Type: tea.KeySpace})
	model = toggled.(Model)
	submitted, cmd := model.updateForward(tea.KeyMsg{Type: tea.KeyEnter})
	model = submitted.(Model)
	if cmd == nil || model.mode != ModeNormal {
		t.Fatalf("forward submit = cmd %T mode %s", cmd, model.mode)
	}
	if len(queued.Messages) != 2 || len(queued.Recipients) != 1 || queued.Recipients[0].ID != "chat-2" {
		t.Fatalf("queued forward = %+v", queued)
	}
	handled, _ := model.Update(cmd())
	model = handled.(Model)
	if !strings.Contains(model.status, "forwarded 2 message") {
		t.Fatalf("forward finished status = %q", model.status)
	}
}

func TestCustomVisualForwardKeyStartsPicker(t *testing.T) {
	keymap := config.DefaultKeymap()
	keymap.VisualForward = "F"
	model := NewModel(Options{
		Config: config.Config{LeaderKey: "space", Keymap: keymap},
		Snapshot: store.Snapshot{
			Chats: []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{"chat-1": {
				{ID: "m-1", ChatID: "chat-1", Body: "one"},
			}},
			DraftsByChat: map[string]string{},
			ActiveChatID: "chat-1",
		},
		ForwardMessages: func(request ForwardMessagesRequest) tea.Cmd {
			return nil
		},
	})
	model.mode = ModeVisual
	model.focus = FocusMessages
	model.visualAnchor = 0

	unchanged, _ := model.updateVisual(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f")})
	model = unchanged.(Model)
	if model.mode != ModeVisual {
		t.Fatalf("default visual forward key started picker with custom binding: mode %s", model.mode)
	}
	forwarding, cmd := model.updateVisual(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("F")})
	model = forwarding.(Model)
	if cmd != nil || model.mode != ModeForward || len(model.forwardSourceMessages) != 1 {
		t.Fatalf("custom visual forward key state = mode %s sources %d cmd %T", model.mode, len(model.forwardSourceMessages), cmd)
	}
}

func TestForwardPickerUsesSlashSearchSubmode(t *testing.T) {
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats: []store.Chat{
				{ID: "chat-1", Title: "Alice"},
				{ID: "chat-2", Title: "Bob"},
				{ID: "chat-3", Title: "Carol"},
			},
			MessagesByChat: map[string][]store.Message{"chat-1": {
				{ID: "m-1", ChatID: "chat-1", Body: "one"},
			}},
			DraftsByChat: map[string]string{},
			ActiveChatID: "chat-1",
		},
		ForwardMessages: func(request ForwardMessagesRequest) tea.Cmd {
			return nil
		},
	})
	model.mode = ModeVisual
	model.focus = FocusMessages
	model.visualAnchor = 0

	forwarding, _ := model.updateVisual(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f")})
	model = forwarding.(Model)
	typed, _ := model.updateForward(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("b")})
	model = typed.(Model)
	if model.forwardQuery != "" || len(model.forwardCandidates) != 3 {
		t.Fatalf("typing in forward picker changed query %q candidates %d", model.forwardQuery, len(model.forwardCandidates))
	}

	searching, _ := model.updateForward(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	model = searching.(Model)
	if !model.forwardSearchActive {
		t.Fatal("slash did not enter forward search")
	}
	filtered, _ := model.updateForward(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("b")})
	model = filtered.(Model)
	if model.forwardQuery != "b" || len(model.forwardCandidates) != 1 || model.forwardCandidates[0].Title != "Bob" {
		t.Fatalf("forward search state = query %q candidates %+v", model.forwardQuery, model.forwardCandidates)
	}
	exited, cmd := model.updateForward(tea.KeyMsg{Type: tea.KeyEnter})
	model = exited.(Model)
	if cmd != nil || model.forwardSearchActive || model.mode != ModeForward {
		t.Fatalf("forward search enter = active %v mode %s cmd %T", model.forwardSearchActive, model.mode, cmd)
	}
	cleared, _ := model.updateForward(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	model = cleared.(Model)
	cleared, _ = model.updateForward(tea.KeyMsg{Type: tea.KeyEsc})
	model = cleared.(Model)
	if model.forwardSearchActive || model.forwardQuery != "" || len(model.forwardCandidates) != 3 {
		t.Fatalf("forward search escape = active %v query %q candidates %d", model.forwardSearchActive, model.forwardQuery, len(model.forwardCandidates))
	}
}

func TestRemoteOpenAndSaveShowDownloadNotImplemented(t *testing.T) {
	model := mediaTestModel("", media.BackendChafa)

	opened, _ := model.openFocusedMedia()
	got := opened.(Model)
	if !strings.Contains(got.status, "not downloaded yet") || !strings.Contains(got.status, "not implemented") {
		t.Fatalf("open status = %q, want remote download limitation", got.status)
	}
	saved, _ := model.saveFocusedMedia()
	got = saved.(Model)
	if !strings.Contains(got.status, "not downloaded yet") || !strings.Contains(got.status, "not implemented") {
		t.Fatalf("save status = %q, want remote download limitation", got.status)
	}
}

func TestActiveMediaPlacementAccountsForAlignmentAndCompactLayout(t *testing.T) {
	localPath := filepath.Join(t.TempDir(), "photo.jpg")
	if err := os.WriteFile(localPath, []byte("fake"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	incoming := mediaTestModel(localPath, media.BackendUeberzugPP)
	cacheOverlayPreview(t, &incoming, localPath)

	incomingPlacement, ok := incoming.activeMediaPlacement()
	if !ok {
		t.Fatal("incoming activeMediaPlacement() returned false")
	}
	if incomingPlacement.X != 34 || incomingPlacement.Y != 3 || incomingPlacement.MaxWidth != 24 || incomingPlacement.MaxHeight != 6 {
		t.Fatalf("incoming placement = %+v, want x=34 y=3 size=24x6", incomingPlacement)
	}

	outgoing := mediaTestModel(localPath, media.BackendUeberzugPP)
	outgoing.messagesByChat["chat-1"][0].IsOutgoing = true
	cacheOverlayPreview(t, &outgoing, localPath)
	outgoingPlacement, ok := outgoing.activeMediaPlacement()
	if !ok {
		t.Fatal("outgoing activeMediaPlacement() returned false")
	}
	if outgoingPlacement.X <= incomingPlacement.X {
		t.Fatalf("outgoing X = %d, incoming X = %d, want outgoing to the right", outgoingPlacement.X, incomingPlacement.X)
	}

	compact := mediaTestModel(localPath, media.BackendUeberzugPP)
	compact.width = 70
	compact.height = 20
	compact.compactLayout = true
	cacheOverlayPreview(t, &compact, localPath)
	compactPlacement, ok := compact.activeMediaPlacement()
	if !ok {
		t.Fatal("compact activeMediaPlacement() returned false")
	}
	if compactPlacement.X != 4 || compactPlacement.Y != 3 {
		t.Fatalf("compact placement = %+v, want x=4 y=3", compactPlacement)
	}

	resized := compact
	resized.width = 90
	cacheOverlayPreview(t, &resized, localPath)
	resizedPlacement, ok := resized.activeMediaPlacement()
	if !ok {
		t.Fatal("resized activeMediaPlacement() returned false")
	}
	if resizedPlacement.X != compactPlacement.X || resizedPlacement.MaxWidth != compactPlacement.MaxWidth {
		t.Fatalf("resized placement = %+v, compact = %+v, want stable incoming placement", resizedPlacement, compactPlacement)
	}
}

func TestLoadedOverlayPreviewsStayVisibleWhenFocusMoves(t *testing.T) {
	dir := t.TempDir()
	firstPath := filepath.Join(dir, "first.jpg")
	secondPath := filepath.Join(dir, "second.jpg")
	if err := os.WriteFile(firstPath, []byte("first"), 0o644); err != nil {
		t.Fatalf("WriteFile(first) error = %v", err)
	}
	if err := os.WriteFile(secondPath, []byte("second"), 0o644); err != nil {
		t.Fatalf("WriteFile(second) error = %v", err)
	}

	model := mediaTestModel(firstPath, media.BackendUeberzugPP)
	model.height = 36
	model.messagesByChat["chat-1"] = append(model.messagesByChat["chat-1"], store.Message{
		ID:     "m-2",
		ChatID: "chat-1",
		Sender: "Alice",
		Media: []store.MediaMetadata{{
			MessageID:     "m-2",
			FileName:      "second.jpg",
			MIMEType:      "image/jpeg",
			LocalPath:     secondPath,
			DownloadState: "downloaded",
		}},
	})
	model.messageCursor = 1
	for messageIndex := range model.messagesByChat["chat-1"] {
		message := model.messagesByChat["chat-1"][messageIndex]
		request, ok := model.previewRequestForMedia(message, message.Media[0], 0, 0)
		if !ok {
			t.Fatalf("previewRequestForMedia(%s) returned false", message.ID)
		}
		model.previewCache[media.PreviewKey(request)] = media.Preview{
			Key:             media.PreviewKey(request),
			MessageID:       message.ID,
			Kind:            media.KindImage,
			Backend:         media.BackendUeberzugPP,
			RenderedBackend: media.BackendUeberzugPP,
			Display:         media.PreviewDisplayOverlay,
			SourceKind:      media.SourceLocal,
			SourcePath:      message.Media[0].LocalPath,
			Width:           request.Width,
			Height:          request.Height,
		}
	}

	placements := model.visibleMediaPlacements()
	if len(placements) != 2 {
		t.Fatalf("visibleMediaPlacements() = %+v, want both loaded previews visible", placements)
	}
	paths := map[string]bool{}
	for _, placement := range placements {
		paths[placement.Path] = true
	}
	if !paths[firstPath] || !paths[secondPath] {
		t.Fatalf("visible placement paths = %+v, want first and second preview paths", paths)
	}
}

func TestScrollingPausedOverlayReservesBlankAndResyncs(t *testing.T) {
	localPath := filepath.Join(t.TempDir(), "photo.jpg")
	if err := os.WriteFile(localPath, []byte("fake"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	model := mediaTestModel(localPath, media.BackendUeberzugPP)
	model.messagesByChat["chat-1"] = append(model.messagesByChat["chat-1"], store.Message{
		ID:        "m-2",
		ChatID:    "chat-1",
		ChatJID:   "group@g.us",
		Sender:    "Member",
		SenderJID: "member@s.whatsapp.net",
		Body:      "newer text",
	})
	model.messageCursor = 0
	model.messageScrollTop = 0
	cacheOverlayPreview(t, &model, localPath)
	message := model.messagesByChat["chat-1"][0]
	request, ok := model.previewRequestForMedia(message, message.Media[0], 0, 0)
	if !ok {
		t.Fatal("previewRequestForMedia() returned false")
	}
	preview := model.previewCache[media.PreviewKey(request)]
	preview.Lines = []string{"fallback-overlay-line"}
	model.previewCache[media.PreviewKey(request)] = preview

	cmd := model.syncOverlayCmd()
	if cmd == nil {
		t.Fatal("syncOverlayCmd() = nil, want overlay command")
	}
	model = applyOverlayCmd(t, model, cmd)

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("movement command = nil, want clear/resume overlay commands")
	}
	if !model.mediaOverlayPaused {
		t.Fatal("mediaOverlayPaused = false, want paused after scrolling")
	}

	view := stripANSI(model.View())
	if strings.Contains(view, "fallback-overlay-line") {
		t.Fatalf("View() rendered low-resolution fallback while overlay was paused\n%s", view)
	}

	cleared, resumeCmd := model.Update(mediaOverlayMsg{Signature: ""})
	model = cleared.(Model)
	if resumeCmd == nil {
		t.Fatal("resume command = nil after paused overlay clear")
	}
	resumed, addCmd := model.Update(overlayResumeMsg{Generation: model.overlayPauseGeneration})
	model = resumed.(Model)
	if model.mediaOverlayPaused {
		t.Fatal("mediaOverlayPaused = true, want resumed")
	}
	if addCmd != nil {
		model = applyOverlayCmd(t, model, addCmd)
	}
	if model.overlaySignature == "" && !model.overlaySyncPending {
		t.Fatal("overlay is neither active nor pending after resume")
	}
	view = stripANSI(model.View())
	if strings.Contains(view, "fallback-overlay-line") {
		t.Fatalf("View() rendered low-resolution fallback after overlay resume\n%s", view)
	}
}

func TestPendingMediaOverlayKeepsLowResolutionFallback(t *testing.T) {
	localPath := filepath.Join(t.TempDir(), "photo.jpg")
	if err := os.WriteFile(localPath, []byte("fake"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	model := mediaTestModel(localPath, media.BackendUeberzugPP)
	cacheOverlayPreview(t, &model, localPath)
	message := model.messagesByChat["chat-1"][0]
	request, ok := model.previewRequestForMedia(message, message.Media[0], 0, 0)
	if !ok {
		t.Fatal("previewRequestForMedia() returned false")
	}
	preview := model.previewCache[media.PreviewKey(request)]
	preview.Lines = []string{"fallback-overlay-line"}
	model.previewCache[media.PreviewKey(request)] = preview

	cmd := model.syncOverlayCmd()
	if cmd == nil {
		t.Fatal("syncOverlayCmd() = nil, want pending overlay command")
	}
	if !model.overlaySyncPending || model.overlayPendingSignature == "" {
		t.Fatal("overlay sync was not marked pending")
	}
	view := stripANSI(model.View())
	if !strings.Contains(view, "fallback-overlay-line") {
		t.Fatalf("View() blanked low-resolution fallback while overlay was pending\n%s", view)
	}
}

func TestStalePendingOverlayCommandCannotApplyAfterNewSignature(t *testing.T) {
	dir := t.TempDir()
	firstPath := filepath.Join(dir, "first.jpg")
	secondPath := filepath.Join(dir, "second.jpg")
	if err := os.WriteFile(firstPath, []byte("first"), 0o644); err != nil {
		t.Fatalf("WriteFile(first) error = %v", err)
	}
	if err := os.WriteFile(secondPath, []byte("second"), 0o644); err != nil {
		t.Fatalf("WriteFile(second) error = %v", err)
	}
	var overlay bytes.Buffer
	model := mediaTestModel(firstPath, media.BackendUeberzugPP)
	model.overlay = media.NewOverlayManagerForWriter(&overlay)
	cacheOverlayPreview(t, &model, firstPath)

	firstCmd := model.syncOverlayCmd()
	if firstCmd == nil {
		t.Fatal("first syncOverlayCmd() = nil")
	}
	firstSignature := model.overlayPendingSignature
	if firstSignature == "" {
		t.Fatal("first overlay pending signature is empty")
	}

	message := model.messagesByChat["chat-1"][0]
	request, ok := model.previewRequestForMedia(message, message.Media[0], 0, 0)
	if !ok {
		t.Fatal("previewRequestForMedia() returned false")
	}
	key := media.PreviewKey(request)
	preview := model.previewCache[key]
	preview.SourcePath = secondPath
	model.previewCache[key] = preview

	secondCmd := model.syncOverlayCmd()
	if secondCmd == nil {
		t.Fatal("second syncOverlayCmd() = nil")
	}
	if model.overlayPendingSignature == firstSignature {
		t.Fatal("second overlay pending signature did not change")
	}

	staleMsg, ok := firstCmd().(mediaOverlayMsg)
	if !ok {
		t.Fatalf("first command message = %T, want mediaOverlayMsg", staleMsg)
	}
	updated, _ := model.Update(staleMsg)
	model = updated.(Model)
	if model.overlaySignature == firstSignature {
		t.Fatal("stale overlay result became active")
	}

	model = applyOverlayCmd(t, model, secondCmd)
	log := overlay.String()
	if strings.Contains(log, firstPath) {
		t.Fatalf("stale overlay command wrote first placement:\n%s", log)
	}
	if !strings.Contains(log, secondPath) {
		t.Fatalf("fresh overlay command did not write second placement:\n%s", log)
	}
}

func TestKeyMsgRunsPreviewSyncForVisibleOverlay(t *testing.T) {
	localPath := filepath.Join(t.TempDir(), "photo.jpg")
	if err := os.WriteFile(localPath, []byte("fake"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	model := mediaTestModel(localPath, media.BackendUeberzugPP)
	cacheOverlayPreview(t, &model, localPath)
	model.mode = ModeNormal
	model.focus = FocusMessages

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("KeyMsg returned nil command, want overlay sync from withPreviewCmd")
	}
	if !model.overlaySyncPending || model.overlayPendingSignature == "" {
		t.Fatalf("overlay pending = %v signature %q, want key-driven preview sync", model.overlaySyncPending, model.overlayPendingSignature)
	}
}

func TestVisibleMutatingAsyncMessagesRunPreviewSyncForVisibleOverlay(t *testing.T) {
	localPath := filepath.Join(t.TempDir(), "photo.jpg")
	if err := os.WriteFile(localPath, []byte("fake"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	tests := []struct {
		name  string
		setup func(*Model)
		msg   func(Model) tea.Msg
	}{
		{
			name: "retry finished",
			msg: func(Model) tea.Msg {
				return retryMessageFinishedMsg{
					Message: store.Message{ID: "retry-1", ChatID: "chat-1", Sender: "me", Body: "retried"},
				}
			},
		},
		{
			name: "sticker sent",
			msg: func(Model) tea.Msg {
				return stickerSentMsg{
					ChatID:  "chat-1",
					Message: store.Message{ID: "sticker-1", ChatID: "chat-1", Sender: "me", Body: "sticker"},
				}
			},
		},
		{
			name: "message filter applied",
			setup: func(model *Model) {
				model.filterGeneration = 7
				model.messageFilter = "photo"
			},
			msg: func(model Model) tea.Msg {
				return messageFilterAppliedMsg{
					Generation: 7,
					ChatID:     "chat-1",
					Query:      "photo",
					Messages:   model.messagesByChat["chat-1"],
				}
			},
		},
		{
			name: "message filter cleared",
			setup: func(model *Model) {
				model.filterGeneration = 7
				model.messageFilter = ""
			},
			msg: func(model Model) tea.Msg {
				return messageFilterClearedMsg{
					Generation: 7,
					ChatID:     "chat-1",
					Messages:   model.messagesByChat["chat-1"],
				}
			},
		},
		{
			name: "clipboard image copied",
			msg: func(Model) tea.Msg {
				return ClipboardImageCopiedMsg{
					MessageID: "m-1",
					Media: store.MediaMetadata{
						MessageID:     "m-1",
						FileName:      "photo.jpg",
						MIMEType:      "image/jpeg",
						LocalPath:     localPath,
						DownloadState: "downloaded",
					},
				}
			},
		},
		{
			name: "media saved",
			msg: func(Model) tea.Msg {
				return mediaSavedMsg{
					MessageID: "m-1",
					Path:      localPath,
					Media: store.MediaMetadata{
						MessageID:     "m-1",
						FileName:      "photo.jpg",
						MIMEType:      "image/jpeg",
						LocalPath:     localPath,
						DownloadState: "downloaded",
					},
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model := mediaTestModel(localPath, media.BackendUeberzugPP)
			cacheOverlayPreview(t, &model, localPath)
			if tt.setup != nil {
				tt.setup(&model)
			}

			updated, cmd := model.Update(tt.msg(model))
			model = updated.(Model)
			if cmd == nil {
				t.Fatal("Update() returned nil command, want overlay sync from withPreviewCmd")
			}
			if !model.overlaySyncPending || model.overlayPendingSignature == "" {
				t.Fatalf("overlay pending = %v signature %q, want async-result preview sync", model.overlaySyncPending, model.overlayPendingSignature)
			}
		})
	}
}

func TestMediaPreviewReadyQueuesOverlaySyncForNewPreview(t *testing.T) {
	localPath := filepath.Join(t.TempDir(), "photo.jpg")
	if err := os.WriteFile(localPath, []byte("fake"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	model := mediaTestModel(localPath, media.BackendUeberzugPP)
	cacheOverlayPreview(t, &model, localPath)
	message := model.messagesByChat["chat-1"][0]
	request, ok := model.previewRequestForMedia(message, message.Media[0], 0, 0)
	if !ok {
		t.Fatal("previewRequestForMedia() returned false")
	}
	key := media.PreviewKey(request)
	preview := model.previewCache[key]
	delete(model.previewCache, key)
	model.previewInflight = map[string]bool{key: true}

	updated, cmd := model.Update(mediaPreviewReadyMsg{
		Key:        key,
		Generation: model.previewGeneration,
		Request:    request,
		Preview:    preview,
	})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("mediaPreviewReadyMsg returned nil command, want overlay sync")
	}
	if !model.overlaySyncPending || model.overlayPendingSignature == "" {
		t.Fatalf("overlay pending = %v signature %q, want queued sync", model.overlaySyncPending, model.overlayPendingSignature)
	}
}

func TestSnapshotReloadPausesTerminalOverlays(t *testing.T) {
	localPath := filepath.Join(t.TempDir(), "photo.jpg")
	if err := os.WriteFile(localPath, []byte("fake"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	model := mediaTestModel(localPath, media.BackendUeberzugPP)
	cacheOverlayPreview(t, &model, localPath)
	cmd := model.syncOverlayCmd()
	if cmd == nil {
		t.Fatal("syncOverlayCmd() = nil, want overlay command")
	}
	model = applyOverlayCmd(t, model, cmd)

	updated, _ := model.handleSnapshotReloaded(snapshotReloadedMsg{
		ActiveChatID: "chat-1",
		Snapshot: store.Snapshot{
			Chats: []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{"chat-1": {
				{ID: "m-1", ChatID: "chat-1", Sender: "Alice", Body: "reloaded"},
				{ID: "m-2", ChatID: "chat-1", Sender: "Bob", Body: "new"},
			}},
			DraftsByChat: map[string]string{},
			ActiveChatID: "chat-1",
		},
	})
	model = updated
	if !model.mediaOverlayPaused || !model.avatarOverlayPaused {
		t.Fatalf("overlay pause = media:%v avatar:%v, want both paused", model.mediaOverlayPaused, model.avatarOverlayPaused)
	}
	if model.overlayPauseGeneration == 0 {
		t.Fatal("overlayPauseGeneration was not incremented")
	}
}

func TestWindowResizePausesTerminalOverlays(t *testing.T) {
	localPath := filepath.Join(t.TempDir(), "photo.jpg")
	if err := os.WriteFile(localPath, []byte("fake"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	model := mediaTestModel(localPath, media.BackendUeberzugPP)
	model.overlay = media.NewOverlayManagerForWriter(&bytes.Buffer{})
	model.overlaySignature = "active"

	updated, cmd := model.Update(tea.WindowSizeMsg{Width: model.width + 5, Height: model.height + 1})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("WindowSizeMsg command = nil, want clear/resume overlay command")
	}
	if !model.mediaOverlayPaused || !model.avatarOverlayPaused {
		t.Fatalf("overlay pause = media:%v avatar:%v, want both paused", model.mediaOverlayPaused, model.avatarOverlayPaused)
	}
}

func TestStickerOverlayPauseDoesNotRenderLowResolutionFallback(t *testing.T) {
	localPath := filepath.Join(t.TempDir(), "sticker.webp")
	if err := os.WriteFile(localPath, []byte("fake"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	model := mediaTestModel(localPath, media.BackendUeberzugPP)
	model.messagesByChat["chat-1"][0].Media[0].Kind = "sticker"
	model.messagesByChat["chat-1"][0].Media[0].FileName = "sticker.webp"
	model.messagesByChat["chat-1"][0].Media[0].MIMEType = "image/webp"
	model.messagesByChat["chat-1"] = append(model.messagesByChat["chat-1"], store.Message{
		ID:     "m-2",
		ChatID: "chat-1",
		Sender: "Alice",
		Body:   "after sticker",
	})
	model.messageCursor = 0
	model.messageScrollTop = 0
	cacheOverlayPreview(t, &model, localPath)
	message := model.messagesByChat["chat-1"][0]
	request, ok := model.previewRequestForMedia(message, message.Media[0], 0, 0)
	if !ok {
		t.Fatal("previewRequestForMedia() returned false")
	}
	preview := model.previewCache[media.PreviewKey(request)]
	preview.Lines = []string{"low-sticker-line"}
	model.previewCache[media.PreviewKey(request)] = preview

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	model = updated.(Model)
	if !model.mediaOverlayPaused {
		t.Fatal("mediaOverlayPaused = false, want paused after sticker scroll")
	}
	view := stripANSI(model.View())
	if strings.Contains(view, "low-sticker-line") {
		t.Fatalf("View() rendered low-resolution sticker fallback while overlay was paused\n%s", view)
	}
}

func TestMediaPreviewUsesWiderBubbleBoundsThanText(t *testing.T) {
	localPath := filepath.Join(t.TempDir(), "photo.jpg")
	if err := os.WriteFile(localPath, []byte("fake"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	model := mediaTestModel(localPath, media.BackendUeberzugPP)
	model.config.PreviewMaxWidth = 67
	model.config.PreviewMaxHeight = 40

	width, height := model.previewDimensions()
	textWidth := max(10, bubbleWidth(model.messagePaneContentWidth())-4)
	if width <= textWidth {
		t.Fatalf("preview width = %d, want wider than text bubble width %d", width, textWidth)
	}
	if width != 67 {
		t.Fatalf("preview width = %d, want configured/Yazi-like width 67", width)
	}
	if height <= 6 || height > 18 {
		t.Fatalf("preview height = %d, want reasonable media preview height in 7..18", height)
	}
}

func TestInfoPaneShowsOverlayDebugCommand(t *testing.T) {
	localPath := filepath.Join(t.TempDir(), "photo.jpg")
	if err := os.WriteFile(localPath, []byte("fake"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	model := mediaTestModel(localPath, media.BackendUeberzugPP)
	cacheOverlayPreview(t, &model, localPath)

	info := stripANSI(model.renderInfo(120))
	for _, want := range []string{"rendered: ueberzug++", "source: " + localPath, "overlay json:", `"scaler":"fit_contain"`} {
		if !strings.Contains(info, want) {
			t.Fatalf("renderInfo missing %q\n%s", want, info)
		}
	}
}

func TestClippedOverlayPreviewFallsBackToInlineLines(t *testing.T) {
	localPath := filepath.Join(t.TempDir(), "photo.jpg")
	if err := os.WriteFile(localPath, []byte("fake"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	model := mediaTestModel(localPath, media.BackendUeberzugPP)
	model.height = 10
	cacheOverlayPreview(t, &model, localPath)
	message := model.messagesByChat["chat-1"][0]
	request, ok := model.previewRequestForMedia(message, message.Media[0], 0, 0)
	if !ok {
		t.Fatal("previewRequestForMedia() returned false")
	}
	preview := model.previewCache[media.PreviewKey(request)]
	preview.Lines = []string{
		"fallback-1",
		"fallback-2",
		"fallback-3",
		"fallback-4",
		"fallback-5",
		"fallback-6",
	}
	model.previewCache[media.PreviewKey(request)] = preview
	model.messageCursor = 0
	model.messageScrollTop = 0

	if placements := model.visibleMediaPlacements(); len(placements) != 0 {
		t.Fatalf("visibleMediaPlacements() = %+v, want clipped overlay to drop out", placements)
	}

	view := stripANSI(model.View())
	if !strings.Contains(view, "fallback-3") {
		t.Fatalf("View() missing fallback preview lines when overlay is clipped\n%s", view)
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

func TestDeleteMessageForEverybodyWaitsForCompletionBeforeRemoval(t *testing.T) {
	var queued store.Message
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats: []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{
				"chat-1": []store.Message{
					{ID: "m-1", RemoteID: "remote-1", ChatID: "chat-1", Sender: "me", Body: "keep", IsOutgoing: true, Status: "sent"},
					{ID: "m-2", RemoteID: "remote-2", ChatID: "chat-1", Sender: "me", Body: "delete me", IsOutgoing: true, Status: "sent"},
				},
			},
			DraftsByChat: map[string]string{},
			ActiveChatID: "chat-1",
		},
		ConnectionState: ConnectionOnline,
		DeleteMessageForEveryone: func(message store.Message) tea.Cmd {
			queued = message
			return func() tea.Msg {
				return MessageDeletedForEveryoneMsg{MessageID: message.ID}
			}
		},
	})
	model.focus = FocusMessages
	model.messageCursor = 1

	leader, _ := model.updateNormal(tea.KeyMsg{Type: tea.KeySpace})
	got := leader.(Model)
	keyD, _ := got.updateNormal(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	got = keyD.(Model)
	armed, _ := got.updateNormal(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	got = armed.(Model)
	if queued.ID != "" {
		t.Fatal("leader d e queued without confirmation")
	}
	if got.mode != ModeConfirm || got.deleteForEveryoneConfirmID != "m-2" {
		t.Fatalf("confirm state = mode %s id %q, want confirm/m-2", got.mode, got.deleteForEveryoneConfirmID)
	}

	typed, _ := got.updateConfirm(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("Y")})
	got = typed.(Model)
	confirmed, cmd := got.updateConfirm(tea.KeyMsg{Type: tea.KeyEnter})
	got = confirmed.(Model)
	if queued.ID != "m-2" {
		t.Fatalf("queued message = %+v, want m-2", queued)
	}
	if len(got.messagesByChat["chat-1"]) != 2 {
		t.Fatalf("message removed before completion: %+v", got.messagesByChat["chat-1"])
	}
	if cmd == nil {
		t.Fatal("confirm command = nil, want delete command")
	}
	if got.mode != ModeNormal {
		t.Fatalf("mode after confirmation = %s, want normal", got.mode)
	}

	handled, _ := got.Update(cmd())
	got = handled.(Model)
	messages := got.messagesByChat["chat-1"]
	if len(messages) != 1 || messages[0].ID != "m-1" {
		t.Fatalf("messages after delete completion = %+v", messages)
	}
}

func TestDeleteMessageForEverybodyLowercaseYDoesNotConfirm(t *testing.T) {
	var queued store.Message
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats: []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{
				"chat-1": {{ID: "m-1", RemoteID: "remote-1", ChatID: "chat-1", Sender: "me", Body: "delete me", IsOutgoing: true, Status: "sent"}},
			},
			DraftsByChat: map[string]string{},
			ActiveChatID: "chat-1",
		},
		ConnectionState: ConnectionOnline,
		DeleteMessageForEveryone: func(message store.Message) tea.Cmd {
			queued = message
			return func() tea.Msg {
				return MessageDeletedForEveryoneMsg{MessageID: message.ID}
			}
		},
	})
	model.focus = FocusMessages
	model.armDeleteFocusedMessageForEveryone()
	if model.mode != ModeConfirm {
		t.Fatalf("mode = %s, want confirm", model.mode)
	}

	typed, _ := model.updateConfirm(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	got := typed.(Model)
	cancelled, cmd := got.updateConfirm(tea.KeyMsg{Type: tea.KeyEnter})
	got = cancelled.(Model)
	if cmd != nil || queued.ID != "" {
		t.Fatalf("lowercase y confirmed delete: cmd=%T queued=%+v", cmd, queued)
	}
	if got.mode != ModeNormal || got.deleteForEveryoneConfirmID != "" || !strings.Contains(got.status, "cancelled") {
		t.Fatalf("cancel state = mode %s id %q status %q", got.mode, got.deleteForEveryoneConfirmID, got.status)
	}
}

func TestDeleteMessageForEverybodyRejectsIncomingMessage(t *testing.T) {
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats: []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{
				"chat-1": {{ID: "m-1", RemoteID: "remote-1", ChatID: "chat-1", Sender: "Alice", Body: "incoming"}},
			},
			DraftsByChat: map[string]string{},
			ActiveChatID: "chat-1",
		},
		ConnectionState: ConnectionOnline,
		DeleteMessageForEveryone: func(message store.Message) tea.Cmd {
			return func() tea.Msg {
				return MessageDeletedForEveryoneMsg{MessageID: message.ID}
			}
		},
	})
	model.focus = FocusMessages

	updated, cmd := model.executeCommand("delete-message-everybody")
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("delete-message-everybody command = %T, want nil", cmd)
	}
	if got.deleteForEveryoneConfirmID != "" || !strings.Contains(got.status, "only your outgoing messages") {
		t.Fatalf("state = confirm %q status %q", got.deleteForEveryoneConfirmID, got.status)
	}
}

func TestEditMessageUsesLeaderAndUpdatesAfterCompletion(t *testing.T) {
	var queued store.Message
	var queuedBody string
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats: []store.Chat{{ID: "chat-1", Title: "Alice"}},
			MessagesByChat: map[string][]store.Message{
				"chat-1": {{ID: "m-1", RemoteID: "remote-1", ChatID: "chat-1", Sender: "me", Body: "old text", IsOutgoing: true, Status: "sent"}},
			},
			DraftsByChat: map[string]string{},
			ActiveChatID: "chat-1",
		},
		ConnectionState: ConnectionOnline,
		EditMessage: func(message store.Message, body string) tea.Cmd {
			queued = message
			queuedBody = body
			return func() tea.Msg {
				return MessageEditedMsg{MessageID: message.ID, Body: body, EditedAt: time.Unix(1_700_000_000, 0)}
			}
		},
	})
	model.focus = FocusMessages

	leader, _ := model.updateNormal(tea.KeyMsg{Type: tea.KeySpace})
	model = leader.(Model)
	editing, _ := model.updateNormal(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	model = editing.(Model)
	if model.mode != ModeInsert || model.editTarget == nil || model.composer != "old text" {
		t.Fatalf("edit state = mode %s target %+v composer %q", model.mode, model.editTarget, model.composer)
	}

	unchanged, cmd := model.updateInsert(tea.KeyMsg{Type: tea.KeyEnter})
	model = unchanged.(Model)
	if cmd != nil || !strings.Contains(model.status, "unchanged") {
		t.Fatalf("unchanged edit = cmd %T status %q", cmd, model.status)
	}
	model.composer = "new text"
	submitted, cmd := model.updateInsert(tea.KeyMsg{Type: tea.KeyEnter})
	model = submitted.(Model)
	if queued.ID != "m-1" || queuedBody != "new text" || cmd == nil {
		t.Fatalf("queued edit = message %+v body %q cmd %T", queued, queuedBody, cmd)
	}
	if got := model.messagesByChat["chat-1"][0].Body; got != "old text" {
		t.Fatalf("body before completion = %q, want old text", got)
	}

	handled, _ := model.Update(cmd())
	model = handled.(Model)
	message := model.messagesByChat["chat-1"][0]
	if message.Body != "new text" || message.EditedAt.IsZero() {
		t.Fatalf("message after edit = %+v", message)
	}
}

func TestEditMessageCancelDoesNotSaveDraft(t *testing.T) {
	saveDraftCalled := false
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats: []store.Chat{{ID: "chat-1", Title: "Alice", HasDraft: true}},
			MessagesByChat: map[string][]store.Message{
				"chat-1": {{ID: "m-1", RemoteID: "remote-1", ChatID: "chat-1", Sender: "me", Body: "old text", IsOutgoing: true, Status: "sent"}},
			},
			DraftsByChat: map[string]string{"chat-1": "existing draft"},
			ActiveChatID: "chat-1",
		},
		ConnectionState: ConnectionOnline,
		EditMessage: func(message store.Message, body string) tea.Cmd {
			return nil
		},
		SaveDraft: func(chatID, body string) error {
			saveDraftCalled = true
			return nil
		},
	})
	model.focus = FocusMessages

	editing, _ := model.beginEditFocusedMessage()
	model = editing.(Model)
	model.composer = "changed text"
	cancelled, _ := model.updateInsert(tea.KeyMsg{Type: tea.KeyEsc})
	model = cancelled.(Model)
	if saveDraftCalled || model.draftsByChat["chat-1"] != "existing draft" || model.editTarget != nil || model.mode != ModeNormal {
		t.Fatalf("cancel state = saveDraft %v drafts %+v target %+v mode %s", saveDraftCalled, model.draftsByChat, model.editTarget, model.mode)
	}
}

func TestEditMessageRejectsIncomingAndMediaMessages(t *testing.T) {
	tests := []struct {
		name    string
		message store.Message
		want    string
	}{
		{
			name:    "incoming",
			message: store.Message{ID: "m-1", RemoteID: "remote-1", ChatID: "chat-1", Sender: "Alice", Body: "incoming"},
			want:    "only your outgoing text messages",
		},
		{
			name:    "media",
			message: store.Message{ID: "m-1", RemoteID: "remote-1", ChatID: "chat-1", Sender: "me", Body: "caption", IsOutgoing: true, Media: []store.MediaMetadata{{MessageID: "m-1", MIMEType: "image/png"}}},
			want:    "media captions",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model := NewModel(Options{
				Snapshot: store.Snapshot{
					Chats:          []store.Chat{{ID: "chat-1", Title: "Alice"}},
					MessagesByChat: map[string][]store.Message{"chat-1": {tt.message}},
					DraftsByChat:   map[string]string{},
					ActiveChatID:   "chat-1",
				},
				ConnectionState: ConnectionOnline,
				EditMessage: func(message store.Message, body string) tea.Cmd {
					t.Fatal("EditMessage should not be called")
					return nil
				},
			})
			model.focus = FocusMessages

			updated, cmd := model.executeCommand("edit-message")
			got := updated.(Model)
			if cmd != nil || got.editTarget != nil || !strings.Contains(got.status, tt.want) {
				t.Fatalf("edit state = cmd %T target %+v status %q", cmd, got.editTarget, got.status)
			}
		})
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

func TestComposerShowsTailOfLongLastLine(t *testing.T) {
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
	model.composer = "0123456789abcdefghijklmnopqrstuvwxyz"

	view := stripANSI(model.renderComposer(18))
	bodyLines := renderedComposerBodyLines(view)
	if len(bodyLines) < 2 {
		t.Fatalf("renderComposer() did not wrap long input\n%s", view)
	}
	if got := strings.Join(bodyLines, ""); got != model.composer {
		t.Fatalf("renderComposer() body = %q, want %q\n%s", got, model.composer, view)
	}
}

func TestComposerTailPreservesGraphemeClusters(t *testing.T) {
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
	model.composer = "ok 👩🏻‍⚕️👩🏻‍⚕️ pronto"

	view := stripANSI(model.renderComposer(16))
	body := strings.Join(renderedComposerBodyLines(view), "")
	want := model.sanitizeDisplayText(model.composer, true)
	if body != want {
		t.Fatalf("renderComposer() body = %q, want %q\n%s", body, want, view)
	}
	if strings.Contains(view, "�") {
		t.Fatalf("renderComposer() split a grapheme cluster\n%s", view)
	}
}

func TestComposerTailKeepsMultilineHeightAndCursor(t *testing.T) {
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
	model.composer = "line one\nline two is much longer than the pane width"

	view := stripANSI(model.renderComposer(18))
	if !strings.Contains(view, "line one") || !strings.Contains(view, " pane width▌") {
		t.Fatalf("renderComposer() did not preserve multiline composer tail\n%s", view)
	}
	if got := len(strings.Split(view, "\n")); got < 3 {
		t.Fatalf("renderComposer() height = %d, want multiline output\n%s", got, view)
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
		PersistMessage: func(outgoing OutgoingMessage) (store.Message, error) {
			return store.Message{ID: "local-1", ChatID: outgoing.ChatID, Sender: "me", Body: outgoing.Body, IsOutgoing: true}, nil
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

func TestSwitchingChatsShowsLatestMessages(t *testing.T) {
	model := NewModel(Options{
		Snapshot: store.Snapshot{
			Chats: []store.Chat{
				{ID: "chat-1", Title: "Alice"},
				{ID: "chat-2", Title: "Bob"},
			},
			MessagesByChat: map[string][]store.Message{
				"chat-1": {
					{ID: "a-0", ChatID: "chat-1", Sender: "Alice", Body: "alice oldest"},
					{ID: "a-1", ChatID: "chat-1", Sender: "Alice", Body: "alice middle"},
					{ID: "a-2", ChatID: "chat-1", Sender: "Alice", Body: "alice newest"},
				},
				"chat-2": {
					{ID: "b-0", ChatID: "chat-2", Sender: "Bob", Body: "bob oldest"},
					{ID: "b-1", ChatID: "chat-2", Sender: "Bob", Body: "bob middle"},
					{ID: "b-2", ChatID: "chat-2", Sender: "Bob", Body: "bob newest"},
				},
			},
			DraftsByChat: map[string]string{},
			ActiveChatID: "chat-1",
		},
	})
	model.focus = FocusChats
	model.messageCursor = 0
	model.messageScrollTop = 0

	next, _ := model.updateNormal(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	model = next.(Model)
	if model.activeChat != 1 {
		t.Fatalf("activeChat = %d, want 1", model.activeChat)
	}
	if model.messageCursor != 2 || model.messageScrollTop != 2 {
		t.Fatalf("after switching to Bob cursor/scroll = %d/%d, want 2/2", model.messageCursor, model.messageScrollTop)
	}
	bobView := stripANSI(model.renderMessages(70, 8))
	if !strings.Contains(bobView, "bob newest") || strings.Contains(bobView, "bob oldest") {
		t.Fatalf("switching to Bob did not show latest messages\n%s", bobView)
	}

	back, _ := model.updateNormal(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
	model = back.(Model)
	if model.activeChat != 0 {
		t.Fatalf("activeChat = %d, want 0", model.activeChat)
	}
	if model.messageCursor != 2 || model.messageScrollTop != 2 {
		t.Fatalf("after switching back to Alice cursor/scroll = %d/%d, want 2/2", model.messageCursor, model.messageScrollTop)
	}
	aliceView := stripANSI(model.renderMessages(70, 8))
	if !strings.Contains(aliceView, "alice newest") || strings.Contains(aliceView, "alice oldest") {
		t.Fatalf("switching back to Alice did not show latest messages\n%s", aliceView)
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
	if len(lines) < 3 || strings.TrimSpace(lines[2]) == "" {
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

	view := stripANSI(model.renderMessages(70, 10))
	assertLineBeforeFooterContains(t, view, "message 19")
	if !strings.Contains(view, "message 18") {
		t.Fatalf("overscrolled viewport did not keep selected message visible\n%s", view)
	}
}

func TestDefaultRenderingAvoidsBackgroundFills(t *testing.T) {
	t.Setenv("VIMWHAT_TRANSPARENT_BARS", "1")
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
	if runtime.GOOS == "windows" {
		t.Skip("pywal cache layout is Unix-specific")
	}
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

var (
	ansiRE     = regexp.MustCompile(`\x1b\[[0-9;:]*m`)
	ansiCodeRE = regexp.MustCompile(`\x1b\[([0-9;:]*)m`)
)

func stripANSI(value string) string {
	return ansiRE.ReplaceAllString(value, "")
}

func renderedComposerBodyLines(view string) []string {
	var bodyLines []string
	for _, line := range strings.Split(view, "\n") {
		if !strings.HasPrefix(line, "> ") {
			continue
		}
		body := strings.TrimPrefix(line, "> ")
		body = strings.TrimRight(body, " ")
		body = strings.TrimSuffix(body, "▌")
		bodyLines = append(bodyLines, body)
	}
	return bodyLines
}

func assertViewWithinBounds(t *testing.T, model Model) {
	t.Helper()
	view := stripANSI(model.View())
	lines := strings.Split(view, "\n")
	if len(lines) > model.height {
		t.Fatalf("View() produced %d lines, want <= %d", len(lines), model.height)
	}
	for i, line := range lines {
		if width := lipgloss.Width(line); width > model.width {
			t.Fatalf("line %d width = %d, want <= %d", i+1, width, model.width)
		}
	}
	if message, ok := model.focusedMessage(); ok {
		body := strings.TrimSpace(model.sanitizeDisplayLine(firstLine(message.Body)))
		if body != "" {
			prefix := body
			if displayWidth(prefix) > 24 {
				prefix, _ = splitDisplayWidth(prefix, 24)
			}
			if !strings.Contains(view, prefix) {
				t.Fatalf("cursor message %q is not visible in rendered view\n%s", prefix, view)
			}
		}
	}
}

type styledRune struct {
	value string
	codes []string
}

func sgrCodesBeforeNth(value, needle string, occurrence int) []string {
	if occurrence < 0 {
		return nil
	}

	styled := visibleStyledRunes(value)
	visible := make([]rune, 0, len(styled))
	for _, item := range styled {
		r, _ := utf8.DecodeRuneInString(item.value)
		visible = append(visible, r)
	}

	query := []rune(needle)
	if len(query) == 0 || len(visible) < len(query) {
		return nil
	}

	found := -1
	count := 0
	for i := 0; i <= len(visible)-len(query); {
		if equalFoldRuneSlice(visible[i:i+len(query)], query) {
			if count == occurrence {
				found = i
				break
			}
			count++
			i += len(query)
			continue
		}
		i++
	}
	if found == -1 {
		return nil
	}
	return styled[found].codes
}

func visibleStyledRunes(value string) []styledRune {
	var out []styledRune
	var currentCodes []string
	for i := 0; i < len(value); {
		if value[i] == '\x1b' {
			if loc := ansiCodeRE.FindStringSubmatchIndex(value[i:]); loc != nil && loc[0] == 0 {
				currentCodes = strings.FieldsFunc(value[i+loc[2]:i+loc[3]], func(r rune) bool {
					return r == ';' || r == ':'
				})
				i += loc[1]
				continue
			}
		}
		r, size := utf8.DecodeRuneInString(value[i:])
		if r == utf8.RuneError && size == 1 {
			return nil
		}
		codes := append([]string(nil), currentCodes...)
		out = append(out, styledRune{value: value[i : i+size], codes: codes})
		i += size
	}
	return out
}

func hasSGRCode(codes []string, want string) bool {
	for _, code := range codes {
		if code == want {
			return true
		}
	}
	return false
}

func equalFoldRuneSlice(left, right []rune) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if !strings.EqualFold(string(left[i]), string(right[i])) {
			return false
		}
	}
	return true
}

func withANSIStyles(t *testing.T) {
	t.Helper()

	renderer := lipgloss.DefaultRenderer()
	previousProfile := renderer.ColorProfile()
	previousBackground := renderer.HasDarkBackground()
	lipgloss.SetColorProfile(termenv.TrueColor)
	lipgloss.SetHasDarkBackground(true)
	t.Cleanup(func() {
		lipgloss.SetColorProfile(previousProfile)
		lipgloss.SetHasDarkBackground(previousBackground)
	})
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
		LeaderKey:        "space",
	}
}

func testPaths(t *testing.T) config.Paths {
	t.Helper()
	dir := t.TempDir()
	return config.Paths{
		TransientDir:    dir,
		CacheDir:        dir,
		MediaDir:        filepath.Join(dir, "media"),
		PreviewCacheDir: filepath.Join(dir, "preview"),
	}
}

func mediaTestModel(localPath string, backend media.Backend) Model {
	downloadState := "downloaded"
	if localPath == "" {
		downloadState = "remote"
	}
	model := NewModel(Options{
		Config: config.Config{
			PreviewMaxWidth:  24,
			PreviewMaxHeight: 6,
			PreviewDelayMS:   0,
			LeaderKey:        "space",
		},
		Paths: config.Paths{
			PreviewCacheDir: filepath.Join(os.TempDir(), "vimwhat-test-preview"),
		},
		PreviewReport: media.Report{
			Selected: backend,
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
						FileName:      "photo.jpg",
						MIMEType:      "image/jpeg",
						LocalPath:     localPath,
						DownloadState: downloadState,
					}},
				}},
			},
			DraftsByChat: map[string]string{},
			ActiveChatID: "chat-1",
		},
	})
	model.width = 120
	model.height = 30
	model.focus = FocusMessages
	return model
}

func audioTestModel(localPath string) Model {
	downloadState := "downloaded"
	if localPath == "" {
		downloadState = "remote"
	}
	model := NewModel(Options{
		Config: config.Config{
			PreviewMaxWidth:  24,
			PreviewMaxHeight: 6,
			PreviewDelayMS:   0,
			LeaderKey:        "space",
		},
		Paths: config.Paths{
			PreviewCacheDir: filepath.Join(os.TempDir(), "vimwhat-test-preview"),
		},
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
					Media: []store.MediaMetadata{{
						MessageID:     "m-1",
						FileName:      "voice.ogg",
						MIMEType:      "audio/ogg",
						LocalPath:     localPath,
						DownloadState: downloadState,
					}},
				}},
			},
			DraftsByChat: map[string]string{},
			ActiveChatID: "chat-1",
		},
	})
	model.width = 120
	model.height = 30
	model.focus = FocusMessages
	return model
}

type fakeAudioProcess struct {
	stopped bool
	waitErr error
}

func (p *fakeAudioProcess) Wait() error {
	return p.waitErr
}

func (p *fakeAudioProcess) Stop() error {
	p.stopped = true
	return nil
}

func cacheOverlayPreview(t *testing.T, model *Model, sourcePath string) {
	t.Helper()
	ensureTestOverlayManager(model)
	message := model.messagesByChat["chat-1"][0]
	request, ok := model.previewRequestForMedia(message, message.Media[0], 0, 0)
	if !ok {
		t.Fatal("previewRequestForMedia() returned false")
	}
	model.previewCache[media.PreviewKey(request)] = media.Preview{
		Key:             media.PreviewKey(request),
		MessageID:       "m-1",
		Kind:            media.KindImage,
		Backend:         media.BackendUeberzugPP,
		RenderedBackend: media.BackendUeberzugPP,
		Display:         media.PreviewDisplayOverlay,
		SourceKind:      media.SourceLocal,
		SourcePath:      sourcePath,
		Width:           request.Width,
		Height:          request.Height,
	}
}

func applyOverlayCmd(t *testing.T, model Model, cmd tea.Cmd) Model {
	t.Helper()
	msg := cmd()
	overlayMsg, ok := msg.(mediaOverlayMsg)
	if !ok {
		t.Fatalf("overlay command message = %T, want mediaOverlayMsg", msg)
	}
	updated, _ := model.Update(overlayMsg)
	next, ok := updated.(Model)
	if !ok {
		t.Fatalf("updated model = %T, want Model", updated)
	}
	return next
}

func cacheChatAvatarOverlayPreview(t *testing.T, model *Model, chat store.Chat, sourcePath string, lines ...string) {
	t.Helper()
	ensureTestOverlayManager(model)
	request, ok := model.chatAvatarPreviewRequest(chat)
	if !ok {
		t.Fatal("chatAvatarPreviewRequest() returned false")
	}
	model.previewCache[media.PreviewKey(request)] = media.Preview{
		Key:             media.PreviewKey(request),
		MessageID:       request.MessageID,
		Kind:            media.KindImage,
		Backend:         media.BackendUeberzugPP,
		RenderedBackend: media.BackendUeberzugPP,
		Display:         media.PreviewDisplayOverlay,
		SourceKind:      media.SourceLocal,
		SourcePath:      sourcePath,
		Width:           request.Width,
		Height:          request.Height,
		Lines:           lines,
	}
}

func ensureTestOverlayManager(model *Model) {
	if model == nil || model.previewReport.Selected != media.BackendUeberzugPP || model.overlay != nil {
		return
	}
	model.previewReport.UeberzugPPOutput = "test"
	model.overlay = media.NewOverlayManagerForWriter(&bytes.Buffer{})
}
