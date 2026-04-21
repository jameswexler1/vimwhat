package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestStoreRoundTrip(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "state.sqlite3")

	store, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	if err := store.UpsertChat(ctx, Chat{ID: "chat-1", Title: "Alice", Pinned: true}); err != nil {
		t.Fatalf("UpsertChat(chat-1) error = %v", err)
	}
	if err := store.UpsertChat(ctx, Chat{ID: "chat-2", Title: "Project"}); err != nil {
		t.Fatalf("UpsertChat(chat-2) error = %v", err)
	}

	older := time.Unix(1_700_000_000, 0)
	newer := older.Add(2 * time.Minute)
	if err := store.AddMessage(ctx, Message{
		ID:        "m-1",
		ChatID:    "chat-1",
		Sender:    "Alice",
		Body:      "older",
		Timestamp: older,
	}); err != nil {
		t.Fatalf("AddMessage(m-1) error = %v", err)
	}
	if err := store.AddMessage(ctx, Message{
		ID:         "m-2",
		ChatID:     "chat-1",
		Sender:     "me",
		Body:       "newer",
		Timestamp:  newer,
		IsOutgoing: true,
	}); err != nil {
		t.Fatalf("AddMessage(m-2) error = %v", err)
	}

	if err := store.SaveDraft(ctx, "chat-2", "ship the sqlite layer"); err != nil {
		t.Fatalf("SaveDraft() error = %v", err)
	}
	if err := store.SetSyncCursor(ctx, "history:chat-1", "cursor-123"); err != nil {
		t.Fatalf("SetSyncCursor() error = %v", err)
	}

	stats, err := store.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats() error = %v", err)
	}
	if stats.Chats != 2 || stats.Messages != 2 || stats.Drafts != 1 {
		t.Fatalf("Stats() = %+v, want chats=2 messages=2 drafts=1", stats)
	}

	snapshot, err := store.LoadSnapshot(ctx, 50)
	if err != nil {
		t.Fatalf("LoadSnapshot() error = %v", err)
	}
	if len(snapshot.Chats) != 2 {
		t.Fatalf("len(snapshot.Chats) = %d, want 2", len(snapshot.Chats))
	}
	if snapshot.ActiveChatID != "chat-1" {
		t.Fatalf("ActiveChatID = %q, want %q", snapshot.ActiveChatID, "chat-1")
	}
	if got := len(snapshot.MessagesByChat["chat-1"]); got != 2 {
		t.Fatalf("len(snapshot.MessagesByChat[chat-1]) = %d, want 2", got)
	}
	if _, ok := snapshot.MessagesByChat["chat-2"]; ok {
		t.Fatal("LoadSnapshot eagerly loaded messages for inactive chat")
	}
	if snapshot.MessagesByChat["chat-1"][0].Body != "older" {
		t.Fatalf("first message body = %q, want %q", snapshot.MessagesByChat["chat-1"][0].Body, "older")
	}
	if !snapshot.Chats[1].HasDraft {
		t.Fatal("expected second chat to report HasDraft")
	}
	if snapshot.DraftsByChat["chat-2"] != "ship the sqlite layer" {
		t.Fatalf("DraftsByChat[chat-2] = %q", snapshot.DraftsByChat["chat-2"])
	}

	draft, err := store.Draft(ctx, "chat-2")
	if err != nil {
		t.Fatalf("Draft() error = %v", err)
	}
	if draft != "ship the sqlite layer" {
		t.Fatalf("Draft() = %q", draft)
	}

	cursor, err := store.SyncCursor(ctx, "history:chat-1")
	if err != nil {
		t.Fatalf("SyncCursor() error = %v", err)
	}
	if cursor != "cursor-123" {
		t.Fatalf("SyncCursor() = %q", cursor)
	}

	var indexed int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM message_fts WHERE chat_id = ?`, "chat-1").Scan(&indexed); err != nil {
		t.Fatalf("query message_fts error = %v", err)
	}
	if indexed != 2 {
		t.Fatalf("indexed message count = %d, want 2", indexed)
	}

	results, err := store.SearchMessages(ctx, "chat-1", "newer", 10)
	if err != nil {
		t.Fatalf("SearchMessages() error = %v", err)
	}
	if len(results) != 1 || results[0].ID != "m-2" {
		t.Fatalf("SearchMessages() = %+v, want m-2", results)
	}

	chats, err := store.SearchChats(ctx, "proj", 10)
	if err != nil {
		t.Fatalf("SearchChats() error = %v", err)
	}
	if len(chats) != 1 || chats[0].ID != "chat-2" {
		t.Fatalf("SearchChats() = %+v, want chat-2", chats)
	}
}

func TestAddOlderMessageDoesNotMoveChatBackward(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "state.sqlite3")

	store, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	if err := store.UpsertChat(ctx, Chat{ID: "chat-1", Title: "Alice"}); err != nil {
		t.Fatalf("UpsertChat() error = %v", err)
	}

	newer := time.Unix(1_700_000_000, 0)
	older := newer.Add(-24 * time.Hour)
	if err := store.AddMessage(ctx, Message{ID: "new", ChatID: "chat-1", Sender: "Alice", Body: "new", Timestamp: newer}); err != nil {
		t.Fatalf("AddMessage(new) error = %v", err)
	}
	if err := store.AddMessage(ctx, Message{ID: "old", ChatID: "chat-1", Sender: "Alice", Body: "old", Timestamp: older}); err != nil {
		t.Fatalf("AddMessage(old) error = %v", err)
	}

	chats, err := store.ListChats(ctx)
	if err != nil {
		t.Fatalf("ListChats() error = %v", err)
	}
	if len(chats) != 1 {
		t.Fatalf("len(chats) = %d, want 1", len(chats))
	}
	if !chats[0].LastMessageAt.Equal(newer) {
		t.Fatalf("LastMessageAt = %s, want %s", chats[0].LastMessageAt, newer)
	}
}

func TestSeedAndClearDemoData(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "state.sqlite3")

	store, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	if err := store.SeedDemoData(ctx); err != nil {
		t.Fatalf("SeedDemoData() error = %v", err)
	}

	stats, err := store.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats() after seed error = %v", err)
	}
	if stats.Chats < 4 || stats.Messages < 10 || stats.Drafts < 1 {
		t.Fatalf("Stats() after seed = %+v, want at least chats=4 messages=10 drafts=1", stats)
	}

	snapshot, err := store.LoadSnapshot(ctx, 200)
	if err != nil {
		t.Fatalf("LoadSnapshot() after seed error = %v", err)
	}
	if len(snapshot.Chats) < 4 {
		t.Fatalf("len(snapshot.Chats) = %d, want at least 4", len(snapshot.Chats))
	}

	projectMessages, err := store.ListMessages(ctx, "demo-chat-project", 200)
	if err != nil {
		t.Fatalf("ListMessages(demo-chat-project) error = %v", err)
	}
	if got := len(projectMessages); got == 0 {
		t.Fatal("expected seeded messages for demo-chat-project")
	}

	if err := store.ClearDemoData(ctx); err != nil {
		t.Fatalf("ClearDemoData() error = %v", err)
	}

	stats, err = store.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats() after clear error = %v", err)
	}
	if stats.Chats != 0 || stats.Messages != 0 || stats.Drafts != 0 {
		t.Fatalf("Stats() after clear = %+v, want all zero", stats)
	}
}
