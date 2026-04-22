package store

import (
	"context"
	"database/sql"
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
	if err := store.UpsertChat(ctx, Chat{ID: "chat-2", JID: "project@g.us", Title: "Project", Kind: "group"}); err != nil {
		t.Fatalf("UpsertChat(chat-2) error = %v", err)
	}

	older := time.Unix(1_700_000_000, 0)
	newer := older.Add(2 * time.Minute)
	if err := store.AddMessage(ctx, Message{
		ID:        "m-1",
		RemoteID:  "remote-1",
		ChatID:    "chat-1",
		ChatJID:   "chat-1@s.whatsapp.net",
		Sender:    "Alice",
		SenderJID: "alice@s.whatsapp.net",
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
	if err := store.UpsertContact(ctx, Contact{
		JID:         "alice@s.whatsapp.net",
		DisplayName: "Alice",
		NotifyName:  "Alice A.",
		Phone:       "+15550100",
	}); err != nil {
		t.Fatalf("UpsertContact() error = %v", err)
	}
	if err := store.UpsertMediaMetadata(ctx, MediaMetadata{
		MessageID:     "m-2",
		MIMEType:      "image/jpeg",
		FileName:      "photo.jpg",
		SizeBytes:     42,
		DownloadState: "remote",
	}); err != nil {
		t.Fatalf("UpsertMediaMetadata() error = %v", err)
	}
	if err := store.SaveUISnapshot(ctx, UISnapshot{
		Kind:   "register",
		Name:   "a",
		ChatID: "chat-1",
		Value:  "copied text",
	}); err != nil {
		t.Fatalf("SaveUISnapshot() error = %v", err)
	}

	stats, err := store.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats() error = %v", err)
	}
	if stats.Chats != 2 || stats.Messages != 2 || stats.Drafts != 1 || stats.Contacts != 1 || stats.MediaItems != 1 || stats.Migrations != 3 {
		t.Fatalf("Stats() = %+v, want chats=2 messages=2 drafts=1 contacts=1 media=1 migrations=3", stats)
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
	if snapshot.Chats[1].Kind != "group" || snapshot.Chats[1].JID != "project@g.us" {
		t.Fatalf("snapshot chat protocol fields = %+v", snapshot.Chats[1])
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
	if snapshot.MessagesByChat["chat-1"][0].RemoteID != "remote-1" {
		t.Fatalf("first message RemoteID = %q, want remote-1", snapshot.MessagesByChat["chat-1"][0].RemoteID)
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
	contact, err := store.Contact(ctx, "alice@s.whatsapp.net")
	if err != nil {
		t.Fatalf("Contact() error = %v", err)
	}
	if contact.DisplayName != "Alice" || contact.NotifyName != "Alice A." {
		t.Fatalf("Contact() = %+v", contact)
	}
	media, err := store.MediaMetadata(ctx, "m-2")
	if err != nil {
		t.Fatalf("MediaMetadata() error = %v", err)
	}
	if media.MIMEType != "image/jpeg" || media.SizeBytes != 42 || media.DownloadState != "remote" {
		t.Fatalf("MediaMetadata() = %+v", media)
	}
	savedSnapshot, err := store.UISnapshot(ctx, "register", "a", "chat-1")
	if err != nil {
		t.Fatalf("UISnapshot() error = %v", err)
	}
	if savedSnapshot.Value != "copied text" {
		t.Fatalf("UISnapshot().Value = %q", savedSnapshot.Value)
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

	if err := store.UpdateMessageStatus(ctx, "m-2", "server_ack"); err != nil {
		t.Fatalf("UpdateMessageStatus() error = %v", err)
	}
	messages, err := store.ListMessages(ctx, "chat-1", 10)
	if err != nil {
		t.Fatalf("ListMessages() after status error = %v", err)
	}
	if messages[1].Status != "server_ack" {
		t.Fatalf("updated message status = %q, want server_ack", messages[1].Status)
	}
}

func TestMessageMediaAndLocalDelete(t *testing.T) {
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
	now := time.Unix(1_700_000_000, 0)
	if err := store.AddMessageWithMedia(ctx, Message{
		ID:        "m-1",
		ChatID:    "chat-1",
		Sender:    "Alice",
		Body:      "",
		Timestamp: now,
	}, []MediaMetadata{{
		FileName:      "report.pdf",
		MIMEType:      "application/pdf",
		SizeBytes:     2048,
		DownloadState: "downloaded",
	}}); err != nil {
		t.Fatalf("AddMessageWithMedia() error = %v", err)
	}

	messages, err := store.ListMessages(ctx, "chat-1", 10)
	if err != nil {
		t.Fatalf("ListMessages() error = %v", err)
	}
	if len(messages) != 1 || len(messages[0].Media) != 1 {
		t.Fatalf("messages with media = %+v", messages)
	}
	if messages[0].Media[0].MessageID != "m-1" {
		t.Fatalf("media MessageID = %q, want m-1", messages[0].Media[0].MessageID)
	}
	chats, err := store.ListChats(ctx)
	if err != nil {
		t.Fatalf("ListChats() error = %v", err)
	}
	if len(chats) != 1 || chats[0].LastPreview != "report.pdf" {
		t.Fatalf("chat preview = %+v, want report.pdf", chats)
	}

	if err := store.DeleteMessage(ctx, "m-1"); err != nil {
		t.Fatalf("DeleteMessage() error = %v", err)
	}
	messages, err = store.ListMessages(ctx, "chat-1", 10)
	if err != nil {
		t.Fatalf("ListMessages() after delete error = %v", err)
	}
	if len(messages) != 0 {
		t.Fatalf("messages after delete = %+v, want none", messages)
	}
	results, err := store.SearchMessages(ctx, "chat-1", "report", 10)
	if err != nil {
		t.Fatalf("SearchMessages() after delete error = %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("search results after delete = %+v, want none", results)
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

func TestOpenMigratesVersionOneDatabase(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "state.sqlite3")

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	statements := []string{
		`CREATE TABLE schema_migrations (id INTEGER PRIMARY KEY, name TEXT NOT NULL UNIQUE, applied_at INTEGER NOT NULL)`,
		`INSERT INTO schema_migrations (name, applied_at) VALUES ('0001_initial_schema', 1)`,
		`CREATE TABLE chats (
			id TEXT PRIMARY KEY,
			title TEXT NOT NULL,
			unread_count INTEGER NOT NULL DEFAULT 0,
			pinned INTEGER NOT NULL DEFAULT 0,
			muted INTEGER NOT NULL DEFAULT 0,
			last_message_at INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
		`CREATE INDEX chats_sort_idx ON chats (pinned DESC, last_message_at DESC, title ASC)`,
		`CREATE TABLE messages (
			id TEXT PRIMARY KEY,
			chat_id TEXT NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
			sender TEXT NOT NULL,
			body TEXT NOT NULL DEFAULT '',
			timestamp_unix INTEGER NOT NULL,
			is_outgoing INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE INDEX messages_chat_time_idx ON messages (chat_id, timestamp_unix ASC, id ASC)`,
		`CREATE TABLE drafts (
			chat_id TEXT PRIMARY KEY REFERENCES chats(id) ON DELETE CASCADE,
			body TEXT NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
		`CREATE TABLE sync_cursors (
			name TEXT PRIMARY KEY,
			value TEXT NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
		`CREATE VIRTUAL TABLE message_fts USING fts5(
			message_id UNINDEXED,
			chat_id UNINDEXED,
			body
		)`,
	}
	for _, stmt := range statements {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			_ = db.Close()
			t.Fatalf("prepare old schema statement %q error = %v", stmt, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close old db error = %v", err)
	}

	store, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open() migrated old db error = %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	applied, pending, err := store.MigrationStatus(ctx)
	if err != nil {
		t.Fatalf("MigrationStatus() error = %v", err)
	}
	if len(applied) != 3 || len(pending) != 0 {
		t.Fatalf("MigrationStatus() applied=%v pending=%v, want three applied and none pending", applied, pending)
	}

	if err := store.UpsertChat(ctx, Chat{ID: "chat-1", Title: "Alice"}); err != nil {
		t.Fatalf("UpsertChat() after migration error = %v", err)
	}
	if err := store.AddMessage(ctx, Message{
		ID:        "m-1",
		RemoteID:  "remote-1",
		ChatID:    "chat-1",
		Sender:    "Alice",
		Body:      "migrated",
		Timestamp: time.Unix(1_700_000_000, 0),
	}); err != nil {
		t.Fatalf("AddMessage() after migration error = %v", err)
	}
	messages, err := store.ListMessages(ctx, "chat-1", 10)
	if err != nil {
		t.Fatalf("ListMessages() after migration error = %v", err)
	}
	if len(messages) != 1 || messages[0].RemoteID != "remote-1" {
		t.Fatalf("messages after migration = %+v", messages)
	}
}
