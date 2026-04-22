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

	if err := ingestor.Apply(ctx, Event{
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

	if err := ingestor.Apply(ctx, Event{
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

	if err := ingestor.Apply(ctx, Event{
		Kind: EventMediaMetadata,
		Media: MediaEvent{
			MessageID:     "msg-1",
			MIMEType:      "image/png",
			FileName:      "image.png",
			SizeBytes:     99,
			DownloadState: "remote",
		},
	}); err != nil {
		t.Fatalf("Apply(media) error = %v", err)
	}

	if err := ingestor.Apply(ctx, Event{
		Kind: EventReceiptUpdate,
		Receipt: ReceiptEvent{
			MessageID: "msg-1",
			ChatID:    "chat-1",
			Status:    "read",
		},
	}); err != nil {
		t.Fatalf("Apply(receipt) error = %v", err)
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
	if err := ingestor.Apply(ctx, Event{
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
	if err := ingestor.Apply(ctx, Event{
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
	if err := ingestor.Apply(ctx, Event{
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
