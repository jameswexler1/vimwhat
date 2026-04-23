package ui

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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
		PersistMessage: func(string, string, []Attachment) (store.Message, error) {
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

	updated, _ := model.updateInsert(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
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

	updated, _ := model.updateNormal(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	got := updated.(Model)
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

	updated, _ := model.executeCommand("history fetch")
	got := updated.(Model)
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

	updated, _ := model.updateNormal(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
	got := updated.(Model)
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
	model.width = 100
	model.height = 24

	helped, _ := model.updateNormal(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")})
	got := helped.(Model)
	if !got.helpVisible {
		t.Fatal("helpVisible = false, want true")
	}
	view := got.View()
	for _, want := range []string{"vimwhat help", "normal:", "insert:", "command:"} {
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
	if strings.Contains(prompt, "[COMMAND]") || !strings.Contains(prompt, ":help") || !strings.Contains(prompt, "enter run") {
		t.Fatalf("command prompt missing workflow: %q", prompt)
	}

	model.mode = ModeSearch
	model.searchLine = "needle"
	prompt = stripANSI(model.renderInput())
	if strings.Contains(prompt, "[SEARCH]") || !strings.Contains(prompt, "/needle") || !strings.Contains(prompt, "empty clears") {
		t.Fatalf("search prompt missing workflow: %q", prompt)
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
	if model.overlaySignature == "" {
		t.Fatal("overlaySignature is empty after first syncOverlayCmd()")
	}
	second := model.syncOverlayCmd()
	if second != nil {
		t.Fatal("second syncOverlayCmd() returned a command for unchanged placements")
	}

	if msg := first(); msg == nil {
		t.Fatal("first overlay command returned nil message")
	}
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

func TestScrollingClearedOverlayShowsInlineFallback(t *testing.T) {
	localPath := filepath.Join(t.TempDir(), "photo.jpg")
	if err := os.WriteFile(localPath, []byte("fake"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	model := mediaTestModel(localPath, media.BackendUeberzugPP)
	model.chats = []store.Chat{{ID: "chat-1", JID: "group@g.us", Title: "Group", Kind: "group"}}
	model.allChats = []store.Chat{{ID: "chat-1", JID: "group@g.us", Title: "Group", Kind: "group"}}
	model.messagesByChat["chat-1"] = append(model.messagesByChat["chat-1"], store.Message{
		ID:        "m-2",
		ChatID:    "chat-1",
		ChatJID:   "group@g.us",
		Sender:    "Member",
		SenderJID: "member@s.whatsapp.net",
		Body:      "newer text",
	})
	model.messageCursor = 1
	model.messageScrollTop = 1
	cacheOverlayPreview(t, &model, localPath)
	message := model.messagesByChat["chat-1"][0]
	request, ok := model.previewRequestForMedia(message, message.Media[0], 0, 0)
	if !ok {
		t.Fatal("previewRequestForMedia() returned false")
	}
	preview := model.previewCache[media.PreviewKey(request)]
	preview.Lines = []string{"fallback-overlay-line"}
	model.previewCache[media.PreviewKey(request)] = preview

	if cmd := model.syncOverlayCmd(); cmd == nil || model.overlaySignature == "" {
		t.Fatal("syncOverlayCmd() did not mark overlay as active")
	}

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
	model = updated.(Model)
	if model.overlaySignature != "" {
		t.Fatalf("overlaySignature = %q, want cleared after movement", model.overlaySignature)
	}

	view := stripANSI(model.View())
	if !strings.Contains(view, "fallback-overlay-line") {
		t.Fatalf("View() hid inline fallback after clearing overlay\n%s", view)
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
