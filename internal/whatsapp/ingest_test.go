package whatsapp

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"vimwhat/internal/store"
)

func TestIngestorAppliesChatMessageMediaAndReceiptEvents(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "state.sqlite3"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	ingestor := Ingestor{Store: db}
	when := time.Unix(1_700_000_000, 0)

	if _, err := ingestor.Apply(ctx, Event{
		Kind: EventChatUpsert,
		Chat: ChatEvent{
			ID:            "chat-1",
			JID:           "chat-1@s.whatsapp.net",
			Title:         "Alice",
			Kind:          "direct",
			Unread:        1,
			LastMessageAt: when,
		},
	}); err != nil {
		t.Fatalf("Apply(chat) error = %v", err)
	}

	if _, err := ingestor.Apply(ctx, Event{
		Kind: EventMessageUpsert,
		Message: MessageEvent{
			ID:        "msg-1",
			RemoteID:  "remote-1",
			ChatID:    "chat-1",
			ChatJID:   "chat-1@s.whatsapp.net",
			Sender:    "Alice",
			SenderJID: "alice@s.whatsapp.net",
			Body:      "needle text",
			Timestamp: when,
			Status:    "received",
		},
	}); err != nil {
		t.Fatalf("Apply(message) error = %v", err)
	}

	if _, err := ingestor.Apply(ctx, Event{
		Kind: EventMediaMetadata,
		Media: MediaEvent{
			MessageID:     "msg-1",
			MIMEType:      "image/png",
			FileName:      "image.png",
			SizeBytes:     99,
			DownloadState: "remote",
			Download: MediaDownloadDescriptor{
				Kind:          "image",
				DirectPath:    "/v/t62.7118-24/image",
				MediaKey:      []byte{1},
				FileSHA256:    []byte{2},
				FileEncSHA256: []byte{3},
				FileLength:    99,
			},
		},
	}); err != nil {
		t.Fatalf("Apply(media) error = %v", err)
	}

	if _, err := ingestor.Apply(ctx, Event{
		Kind: EventReceiptUpdate,
		Receipt: ReceiptEvent{
			MessageID: "msg-1",
			ChatID:    "chat-1",
			Status:    "read",
		},
	}); err != nil {
		t.Fatalf("Apply(receipt) error = %v", err)
	}
	if _, err := ingestor.Apply(ctx, Event{
		Kind: EventReceiptUpdate,
		Receipt: ReceiptEvent{
			MessageID: "msg-1",
			ChatID:    "chat-1",
			Status:    "delivered",
		},
	}); err != nil {
		t.Fatalf("Apply(late receipt) error = %v", err)
	}

	chats, err := db.ListChats(ctx)
	if err != nil {
		t.Fatalf("ListChats() error = %v", err)
	}
	if len(chats) != 1 || chats[0].JID != "chat-1@s.whatsapp.net" {
		t.Fatalf("chats = %+v", chats)
	}

	messages, err := db.SearchMessages(ctx, "chat-1", "needle", 10)
	if err != nil {
		t.Fatalf("SearchMessages() error = %v", err)
	}
	if len(messages) != 1 || messages[0].RemoteID != "remote-1" || messages[0].Status != "read" {
		t.Fatalf("messages = %+v", messages)
	}

	media, err := db.MediaMetadata(ctx, "msg-1")
	if err != nil {
		t.Fatalf("MediaMetadata() error = %v", err)
	}
	if media.MIMEType != "image/png" || media.DownloadState != "remote" {
		t.Fatalf("media = %+v", media)
	}
	descriptor, ok, err := db.MediaDownloadDescriptor(ctx, "msg-1")
	if err != nil {
		t.Fatalf("MediaDownloadDescriptor() error = %v", err)
	}
	if !ok || descriptor.Kind != "image" || descriptor.DirectPath == "" || descriptor.FileLength != 99 {
		t.Fatalf("descriptor = %+v ok=%v", descriptor, ok)
	}
}

func TestIngestorAppliesHistoricalMessageWithoutUnreadIncrement(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "state.sqlite3"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	ingestor := Ingestor{Store: db}
	when := time.Unix(1_700_000_000, 0)
	if _, err := ingestor.Apply(ctx, Event{
		Kind: EventChatUpsert,
		Chat: ChatEvent{
			ID:            "chat-1",
			JID:           "chat-1@s.whatsapp.net",
			Title:         "Alice",
			Kind:          "direct",
			LastMessageAt: when,
		},
	}); err != nil {
		t.Fatalf("Apply(chat) error = %v", err)
	}
	if _, err := ingestor.Apply(ctx, Event{
		Kind: EventMessageUpsert,
		Message: MessageEvent{
			ID:         "chat-1/old-1",
			RemoteID:   "old-1",
			ChatID:     "chat-1",
			ChatJID:    "chat-1@s.whatsapp.net",
			Sender:     "Alice",
			SenderJID:  "alice@s.whatsapp.net",
			Body:       "old",
			Timestamp:  when,
			Status:     "received",
			Historical: true,
		},
	}); err != nil {
		t.Fatalf("Apply(historical message) error = %v", err)
	}
	if _, err := ingestor.Apply(ctx, Event{
		Kind: EventHistoryStatus,
		History: HistoryEvent{
			ChatID:         "chat-1",
			Messages:       1,
			Exhausted:      true,
			TerminalReason: "no_more",
		},
	}); err != nil {
		t.Fatalf("Apply(history status) error = %v", err)
	}

	chats, err := db.ListChats(ctx)
	if err != nil {
		t.Fatalf("ListChats() error = %v", err)
	}
	if len(chats) != 1 || chats[0].Unread != 0 {
		t.Fatalf("chats = %+v, want no unread increment", chats)
	}
	cursor, err := db.SyncCursor(ctx, HistoryExhaustedCursor("chat-1"))
	if err != nil {
		t.Fatalf("SyncCursor() error = %v", err)
	}
	if cursor != "no_more" {
		t.Fatalf("history cursor = %q, want no_more", cursor)
	}
}

func TestIngestorAppliesMessageDeleteEvent(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "state.sqlite3"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	ingestor := Ingestor{Store: db}
	when := time.Unix(1_700_000_000, 0)
	if _, err := ingestor.Apply(ctx, Event{
		Kind: EventChatUpsert,
		Chat: ChatEvent{
			ID:            "chat-1",
			JID:           "chat-1@s.whatsapp.net",
			Title:         "Alice",
			Kind:          "direct",
			LastMessageAt: when,
		},
	}); err != nil {
		t.Fatalf("Apply(chat) error = %v", err)
	}
	if _, err := ingestor.Apply(ctx, Event{
		Kind: EventMessageUpsert,
		Message: MessageEvent{
			ID:         "chat-1/remote-1",
			RemoteID:   "remote-1",
			ChatID:     "chat-1",
			ChatJID:    "chat-1@s.whatsapp.net",
			Sender:     "me",
			SenderJID:  "me",
			Body:       "remove me",
			Timestamp:  when,
			IsOutgoing: true,
			Status:     "sent",
		},
	}); err != nil {
		t.Fatalf("Apply(message) error = %v", err)
	}
	if _, err := ingestor.Apply(ctx, Event{
		Kind: EventMessageDelete,
		Delete: MessageDeleteEvent{
			MessageID: "chat-1/remote-1",
			RemoteID:  "remote-1",
			ChatID:    "chat-1",
			ChatJID:   "chat-1@s.whatsapp.net",
		},
	}); err != nil {
		t.Fatalf("Apply(delete) error = %v", err)
	}

	messages, err := db.ListMessages(ctx, "chat-1", 10)
	if err != nil {
		t.Fatalf("ListMessages() error = %v", err)
	}
	if len(messages) != 0 {
		t.Fatalf("messages after delete = %+v, want none", messages)
	}
}

func TestIngestorAppliesMessageEditEvent(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "state.sqlite3"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	ingestor := Ingestor{Store: db}
	when := time.Unix(1_700_000_000, 0)
	if _, err := ingestor.Apply(ctx, Event{
		Kind: EventChatUpsert,
		Chat: ChatEvent{
			ID:            "chat-1",
			JID:           "chat-1@s.whatsapp.net",
			Title:         "Alice",
			Kind:          "direct",
			LastMessageAt: when,
		},
	}); err != nil {
		t.Fatalf("Apply(chat) error = %v", err)
	}
	if _, err := ingestor.Apply(ctx, Event{
		Kind: EventMessageUpsert,
		Message: MessageEvent{
			ID:         "chat-1/remote-1",
			RemoteID:   "remote-1",
			ChatID:     "chat-1",
			ChatJID:    "chat-1@s.whatsapp.net",
			Sender:     "me",
			SenderJID:  "me",
			Body:       "old body",
			Timestamp:  when,
			IsOutgoing: true,
			Status:     "sent",
		},
	}); err != nil {
		t.Fatalf("Apply(message) error = %v", err)
	}
	editedAt := when.Add(time.Minute)
	if _, err := ingestor.Apply(ctx, Event{
		Kind: EventMessageEdit,
		Edit: MessageEditEvent{
			MessageID: "chat-1/remote-1",
			RemoteID:  "remote-1",
			ChatID:    "chat-1",
			ChatJID:   "chat-1@s.whatsapp.net",
			Body:      "new body",
			EditedAt:  editedAt,
		},
	}); err != nil {
		t.Fatalf("Apply(edit) error = %v", err)
	}

	messages, err := db.SearchMessages(ctx, "chat-1", "new", 10)
	if err != nil {
		t.Fatalf("SearchMessages() error = %v", err)
	}
	if len(messages) != 1 || messages[0].Body != "new body" || !messages[0].EditedAt.Equal(editedAt) {
		t.Fatalf("messages after edit = %+v", messages)
	}
	old, err := db.SearchMessages(ctx, "chat-1", "old", 10)
	if err != nil {
		t.Fatalf("SearchMessages(old) error = %v", err)
	}
	if len(old) != 0 {
		t.Fatalf("old search after edit = %+v, want none", old)
	}
}

func TestIngestorApplyReportsInsertedOnlyForFirstLiveMessage(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "state.sqlite3"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	ingestor := Ingestor{Store: db}
	when := time.Unix(1_700_000_000, 0)
	if _, err := ingestor.Apply(ctx, Event{
		Kind: EventChatUpsert,
		Chat: ChatEvent{
			ID:    "chat-1",
			JID:   "chat-1@s.whatsapp.net",
			Title: "Alice",
			Kind:  "direct",
		},
	}); err != nil {
		t.Fatalf("Apply(chat) error = %v", err)
	}

	event := Event{
		Kind: EventMessageUpsert,
		Message: MessageEvent{
			ID:                  "chat-1/msg-1",
			RemoteID:            "msg-1",
			ChatID:              "chat-1",
			ChatJID:             "chat-1@s.whatsapp.net",
			Sender:              "Alice",
			SenderJID:           "alice@s.whatsapp.net",
			Body:                "hello",
			NotificationPreview: "hello",
			Timestamp:           when,
			Status:              "received",
		},
	}

	first, err := ingestor.Apply(ctx, event)
	if err != nil {
		t.Fatalf("Apply(first) error = %v", err)
	}
	if !first.MessageInserted {
		t.Fatal("first Apply() reported MessageInserted=false, want true")
	}

	second, err := ingestor.Apply(ctx, event)
	if err != nil {
		t.Fatalf("Apply(second) error = %v", err)
	}
	if second.MessageInserted {
		t.Fatal("second Apply() reported MessageInserted=true, want false")
	}
}

func TestIngestorUpdatesExistingChatFromContactWithoutCreatingChat(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "state.sqlite3"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	ingestor := Ingestor{Store: db}
	if _, err := ingestor.Apply(ctx, Event{
		Kind: EventContactUpsert,
		Contact: ContactEvent{
			JID:         "missing@s.whatsapp.net",
			DisplayName: "Missing",
			UpdatedAt:   time.Unix(1_700_000_000, 0),
			TitleSource: store.ChatTitleSourceContactDisplay,
		},
	}); err != nil {
		t.Fatalf("Apply(contact missing chat) error = %v", err)
	}
	chats, err := db.ListChats(ctx)
	if err != nil {
		t.Fatalf("ListChats() error = %v", err)
	}
	if len(chats) != 0 {
		t.Fatalf("contact upsert created chats = %+v, want none", chats)
	}

	if _, err := ingestor.Apply(ctx, Event{
		Kind: EventChatUpsert,
		Chat: ChatEvent{
			ID:          "12345@s.whatsapp.net",
			JID:         "12345@s.whatsapp.net",
			Title:       "12345",
			TitleSource: store.ChatTitleSourceJID,
			Kind:        "direct",
		},
	}); err != nil {
		t.Fatalf("Apply(chat) error = %v", err)
	}
	if _, err := ingestor.Apply(ctx, Event{
		Kind: EventContactUpsert,
		Contact: ContactEvent{
			JID:         "12345@s.whatsapp.net",
			DisplayName: "Alice",
			UpdatedAt:   time.Unix(1_700_000_001, 0),
			TitleSource: store.ChatTitleSourceContactDisplay,
		},
	}); err != nil {
		t.Fatalf("Apply(contact existing chat) error = %v", err)
	}
	chats, err = db.ListChats(ctx)
	if err != nil {
		t.Fatalf("ListChats() after contact error = %v", err)
	}
	if len(chats) != 1 || chats[0].Title != "Alice" || chats[0].TitleSource != store.ChatTitleSourceContactDisplay {
		t.Fatalf("chat after contact = %+v, want Alice/contact_display", chats)
	}
}

func TestIngestorDoesNotLetOutgoingDirectFallbackOverwritePushName(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "state.sqlite3"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	ingestor := Ingestor{Store: db}
	if _, err := ingestor.Apply(ctx, Event{
		Kind: EventChatUpsert,
		Chat: ChatEvent{
			ID:          "12345@s.whatsapp.net",
			JID:         "12345@s.whatsapp.net",
			Title:       "Alice",
			TitleSource: store.ChatTitleSourcePushName,
			Kind:        "direct",
		},
	}); err != nil {
		t.Fatalf("Apply(incoming title) error = %v", err)
	}
	if _, err := ingestor.Apply(ctx, Event{
		Kind: EventChatUpsert,
		Chat: ChatEvent{
			ID:          "12345@s.whatsapp.net",
			JID:         "12345@s.whatsapp.net",
			Title:       "12345",
			TitleSource: store.ChatTitleSourceJID,
			Kind:        "direct",
		},
	}); err != nil {
		t.Fatalf("Apply(outgoing fallback title) error = %v", err)
	}

	chats, err := db.ListChats(ctx)
	if err != nil {
		t.Fatalf("ListChats() error = %v", err)
	}
	if len(chats) != 1 || chats[0].Title != "Alice" || chats[0].TitleSource != store.ChatTitleSourcePushName {
		t.Fatalf("chat after outgoing fallback = %+v, want Alice/push_name", chats)
	}
}
