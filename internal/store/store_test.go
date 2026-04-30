package store

import (
	"context"
	"database/sql"
	"os"
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
	if stats.Chats != 2 || stats.Messages != 2 || stats.Drafts != 1 || stats.Contacts != 1 || stats.Participants != 0 || stats.MediaItems != 1 || stats.Migrations != 11 {
		t.Fatalf("Stats() = %+v, want chats=2 messages=2 drafts=1 contacts=1 participants=0 media=1 migrations=11", stats)
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

func TestReceiptStatusUpdatesAreMonotonic(t *testing.T) {
	ctx := context.Background()
	db, err := Open(filepath.Join(t.TempDir(), "state.sqlite3"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	if err := db.UpsertChat(ctx, Chat{ID: "chat-1", Title: "Alice"}); err != nil {
		t.Fatalf("UpsertChat() error = %v", err)
	}
	if err := db.AddMessage(ctx, Message{
		ID:         "m-1",
		ChatID:     "chat-1",
		ChatJID:    "chat-1@s.whatsapp.net",
		Sender:     "me",
		SenderJID:  "me",
		Body:       "outgoing",
		IsOutgoing: true,
		Status:     "delivered",
	}); err != nil {
		t.Fatalf("AddMessage() error = %v", err)
	}

	updated, err := db.UpdateMessageReceiptStatusIfExists(ctx, "m-1", "read")
	if err != nil {
		t.Fatalf("UpdateMessageReceiptStatusIfExists(read) error = %v", err)
	}
	if !updated {
		t.Fatal("UpdateMessageReceiptStatusIfExists(read) updated = false, want true")
	}
	assertMessageStatus(t, db, ctx, "chat-1", "m-1", "read")

	updated, err = db.UpdateMessageReceiptStatusIfExists(ctx, "m-1", "delivered")
	if err != nil {
		t.Fatalf("UpdateMessageReceiptStatusIfExists(delivered) error = %v", err)
	}
	if !updated {
		t.Fatal("UpdateMessageReceiptStatusIfExists(delivered) updated = false for existing message")
	}
	assertMessageStatus(t, db, ctx, "chat-1", "m-1", "read")

	updated, err = db.UpdateMessageReceiptStatusIfExists(ctx, "m-1", "played")
	if err != nil {
		t.Fatalf("UpdateMessageReceiptStatusIfExists(played) error = %v", err)
	}
	if !updated {
		t.Fatal("UpdateMessageReceiptStatusIfExists(played) updated = false for existing message")
	}
	assertMessageStatus(t, db, ctx, "chat-1", "m-1", "played")

	updated, err = db.UpdateMessageReceiptStatusIfExists(ctx, "missing", "read")
	if err != nil {
		t.Fatalf("UpdateMessageReceiptStatusIfExists(missing) error = %v", err)
	}
	if updated {
		t.Fatal("UpdateMessageReceiptStatusIfExists(missing) updated = true, want false")
	}

	if err := db.UpdateMessageStatus(ctx, "m-1", "failed"); err != nil {
		t.Fatalf("UpdateMessageStatus(failed) error = %v", err)
	}
	assertMessageStatus(t, db, ctx, "chat-1", "m-1", "failed")
}

func assertMessageStatus(t *testing.T, db *Store, ctx context.Context, chatID, messageID, want string) {
	t.Helper()
	messages, err := db.ListMessages(ctx, chatID, 10)
	if err != nil {
		t.Fatalf("ListMessages() error = %v", err)
	}
	for _, message := range messages {
		if message.ID == messageID {
			if message.Status != want {
				t.Fatalf("message %s status = %q, want %q", messageID, message.Status, want)
			}
			return
		}
	}
	t.Fatalf("message %s not found in %+v", messageID, messages)
}

func TestAccentAwareSearch(t *testing.T) {
	ctx := context.Background()
	db, err := Open(filepath.Join(t.TempDir(), "state.sqlite3"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	for _, chat := range []Chat{
		{ID: "accented", Title: "João", LastMessageAt: time.Unix(1, 0)},
		{ID: "plain", Title: "Joao", LastMessageAt: time.Unix(2, 0)},
	} {
		if err := db.UpsertChat(ctx, chat); err != nil {
			t.Fatalf("UpsertChat(%s) error = %v", chat.ID, err)
		}
	}
	if err := db.AddMessage(ctx, Message{ID: "m-1", ChatID: "accented", Sender: "A", Body: "olá mundo", Timestamp: time.Unix(1, 0)}); err != nil {
		t.Fatalf("AddMessage(m-1) error = %v", err)
	}
	if err := db.AddMessage(ctx, Message{ID: "m-2", ChatID: "accented", Sender: "A", Body: "ola mundo", Timestamp: time.Unix(2, 0)}); err != nil {
		t.Fatalf("AddMessage(m-2) error = %v", err)
	}

	chats, err := db.SearchChats(ctx, "Joao", 10)
	if err != nil {
		t.Fatalf("SearchChats(Joao) error = %v", err)
	}
	if len(chats) != 2 {
		t.Fatalf("SearchChats(Joao) = %+v, want accented and plain", chats)
	}
	chats, err = db.SearchChats(ctx, "João", 10)
	if err != nil {
		t.Fatalf("SearchChats(João) error = %v", err)
	}
	if len(chats) != 1 || chats[0].ID != "accented" {
		t.Fatalf("SearchChats(João) = %+v, want accented only", chats)
	}

	messages, err := db.SearchMessages(ctx, "accented", "ola", 10)
	if err != nil {
		t.Fatalf("SearchMessages(ola) error = %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("SearchMessages(ola) = %+v, want both messages", messages)
	}
	messages, err = db.SearchMessages(ctx, "accented", "olá", 10)
	if err != nil {
		t.Fatalf("SearchMessages(olá) error = %v", err)
	}
	if len(messages) != 1 || messages[0].ID != "m-1" {
		t.Fatalf("SearchMessages(olá) = %+v, want accented only", messages)
	}
}

func TestGroupMentionCandidatesAndMessageMentions(t *testing.T) {
	ctx := context.Background()
	db, err := Open(filepath.Join(t.TempDir(), "state.sqlite3"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	if err := db.UpsertChat(ctx, Chat{ID: "group@g.us", JID: "group@g.us", Title: "Group", Kind: "group"}); err != nil {
		t.Fatalf("UpsertChat(group) error = %v", err)
	}
	if err := db.UpsertContact(ctx, Contact{JID: "111@s.whatsapp.net", DisplayName: "José Silva", Phone: "111"}); err != nil {
		t.Fatalf("UpsertContact() error = %v", err)
	}
	if err := db.UpsertContact(ctx, Contact{JID: "333@s.whatsapp.net", DisplayName: "J Otávio", Phone: "333"}); err != nil {
		t.Fatalf("UpsertContact(phone alias) error = %v", err)
	}
	if err := db.ReplaceGroupParticipants(ctx, "group@g.us", []GroupParticipant{
		{ChatID: "group@g.us", JID: "111@s.whatsapp.net", DisplayName: "Jose fallback"},
		{ChatID: "group@g.us", JID: "222@s.whatsapp.net", DisplayName: "Ana"},
		{ChatID: "group@g.us", JID: "333333@lid", PhoneJID: "333@s.whatsapp.net", DisplayName: "José Otávio"},
	}); err != nil {
		t.Fatalf("ReplaceGroupParticipants() error = %v", err)
	}

	candidates, err := db.SearchMentionCandidates(ctx, "group@g.us", "Jose", 10)
	if err != nil {
		t.Fatalf("SearchMentionCandidates(Jose) error = %v", err)
	}
	if len(candidates) == 0 || candidates[0].JID != "111@s.whatsapp.net" || candidates[0].DisplayName != "José Silva" {
		t.Fatalf("SearchMentionCandidates(Jose) = %+v, want contact display name", candidates)
	}
	candidates, err = db.SearchMentionCandidates(ctx, "group@g.us", "José", 10)
	if err != nil {
		t.Fatalf("SearchMentionCandidates(José) error = %v", err)
	}
	if len(candidates) == 0 || candidates[0].JID != "111@s.whatsapp.net" {
		t.Fatalf("SearchMentionCandidates(José) = %+v, want accented contact", candidates)
	}
	candidates, err = db.SearchMentionCandidates(ctx, "group@g.us", "Otavio", 10)
	if err != nil {
		t.Fatalf("SearchMentionCandidates(Otavio) error = %v", err)
	}
	if len(candidates) != 1 || candidates[0].JID != "333333@lid" || candidates[0].DisplayName != "J Otávio" {
		t.Fatalf("SearchMentionCandidates(Otavio) = %+v, want phone contact display name", candidates)
	}
	if err := db.ReplaceGroupParticipants(ctx, "group@g.us", []GroupParticipant{
		{ChatID: "group@g.us", JID: "222@s.whatsapp.net", DisplayName: "Ana"},
	}); err != nil {
		t.Fatalf("ReplaceGroupParticipants(remove) error = %v", err)
	}
	candidates, err = db.SearchMentionCandidates(ctx, "group@g.us", "Jose", 10)
	if err != nil {
		t.Fatalf("SearchMentionCandidates(after replace) error = %v", err)
	}
	if len(candidates) != 0 {
		t.Fatalf("SearchMentionCandidates(after replace) = %+v, want no Jose", candidates)
	}

	if err := db.AddMessage(ctx, Message{
		ID:        "m-mention",
		ChatID:    "group@g.us",
		Sender:    "me",
		Body:      "@Ana hello",
		Timestamp: time.Unix(1, 0),
		Mentions: []MessageMention{{
			JID:         "222@s.whatsapp.net",
			DisplayName: "Ana",
			StartByte:   0,
			EndByte:     4,
		}},
	}); err != nil {
		t.Fatalf("AddMessage(with mention) error = %v", err)
	}
	message, ok, err := db.MessageByID(ctx, "m-mention")
	if err != nil {
		t.Fatalf("MessageByID() error = %v", err)
	}
	if !ok || len(message.Mentions) != 1 || message.Mentions[0].JID != "222@s.whatsapp.net" {
		t.Fatalf("MessageByID().Mentions = %+v ok=%v, want Ana mention", message.Mentions, ok)
	}
}

func TestChatByJIDMessageByIDAndListAllMessages(t *testing.T) {
	ctx := context.Background()
	db, err := Open(filepath.Join(t.TempDir(), "state.sqlite3"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	chat := Chat{ID: "chat-1", JID: "project@g.us", Title: "Project", Kind: "group"}
	if err := db.UpsertChat(ctx, chat); err != nil {
		t.Fatalf("UpsertChat() error = %v", err)
	}

	base := time.Unix(1_700_100_000, 0)
	if err := db.AddMessage(ctx, Message{
		ID:        "m-1",
		ChatID:    chat.ID,
		ChatJID:   chat.JID,
		Sender:    "Alice",
		SenderJID: "alice@s.whatsapp.net",
		Body:      "First line\nSecond line",
		Timestamp: base,
	}); err != nil {
		t.Fatalf("AddMessage(m-1) error = %v", err)
	}
	if err := db.AddMessageWithMedia(ctx, Message{
		ID:         "m-2",
		RemoteID:   "remote-2",
		ChatID:     chat.ID,
		ChatJID:    chat.JID,
		Sender:     "me",
		SenderJID:  "me@s.whatsapp.net",
		Timestamp:  base.Add(time.Minute),
		IsOutgoing: true,
		Status:     "failed",
	}, []MediaMetadata{{
		MessageID:     "m-2",
		MIMEType:      "application/pdf",
		FileName:      "report.pdf",
		LocalPath:     "/tmp/report.pdf",
		DownloadState: "downloaded",
		UpdatedAt:     base.Add(time.Minute),
	}}); err != nil {
		t.Fatalf("AddMessageWithMedia(m-2) error = %v", err)
	}
	if err := db.UpsertReaction(ctx, Reaction{
		MessageID:  "m-2",
		SenderJID:  "alice@s.whatsapp.net",
		Emoji:      "👍",
		Timestamp:  base.Add(2 * time.Minute),
		UpdatedAt:  base.Add(2 * time.Minute),
		IsOutgoing: false,
	}); err != nil {
		t.Fatalf("UpsertReaction() error = %v", err)
	}

	gotChat, ok, err := db.ChatByJID(ctx, chat.JID)
	if err != nil {
		t.Fatalf("ChatByJID() error = %v", err)
	}
	if !ok || gotChat.ID != chat.ID || gotChat.JID != chat.JID {
		t.Fatalf("ChatByJID() = %+v ok=%v", gotChat, ok)
	}
	gotChatByID, ok, err := db.ChatByID(ctx, chat.ID)
	if err != nil {
		t.Fatalf("ChatByID() error = %v", err)
	}
	if !ok || gotChatByID.JID != chat.JID || gotChatByID.Kind != "group" {
		t.Fatalf("ChatByID() = %+v ok=%v", gotChatByID, ok)
	}

	gotMessage, ok, err := db.MessageByID(ctx, "m-2")
	if err != nil {
		t.Fatalf("MessageByID() error = %v", err)
	}
	if !ok {
		t.Fatalf("MessageByID() ok=false, want true")
	}
	if gotMessage.RemoteID != "remote-2" || gotMessage.Status != "failed" {
		t.Fatalf("MessageByID() = %+v, want remote/status preserved", gotMessage)
	}
	if len(gotMessage.Media) != 1 || gotMessage.Media[0].FileName != "report.pdf" {
		t.Fatalf("MessageByID().Media = %+v, want report.pdf", gotMessage.Media)
	}
	if len(gotMessage.Reactions) != 1 || gotMessage.Reactions[0].Emoji != "👍" {
		t.Fatalf("MessageByID().Reactions = %+v, want thumbs up", gotMessage.Reactions)
	}

	all, err := db.ListAllMessages(ctx, chat.ID)
	if err != nil {
		t.Fatalf("ListAllMessages() error = %v", err)
	}
	if len(all) != 2 || all[0].ID != "m-1" || all[1].ID != "m-2" {
		t.Fatalf("ListAllMessages() = %+v, want m-1,m-2", all)
	}

	if _, ok, err := db.ChatByJID(ctx, "missing@g.us"); err != nil || ok {
		t.Fatalf("ChatByJID(missing) ok=%v err=%v, want false,nil", ok, err)
	}
	if _, ok, err := db.ChatByID(ctx, "missing"); err != nil || ok {
		t.Fatalf("ChatByID(missing) ok=%v err=%v, want false,nil", ok, err)
	}
	if _, ok, err := db.MessageByID(ctx, "missing"); err != nil || ok {
		t.Fatalf("MessageByID(missing) ok=%v err=%v, want false,nil", ok, err)
	}
}

func TestAddIncomingMessageIncrementsUnreadOnlyOnce(t *testing.T) {
	ctx := context.Background()
	db, err := Open(filepath.Join(t.TempDir(), "state.sqlite3"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	if err := db.UpsertChat(ctx, Chat{ID: "chat-1", Title: "Alice"}); err != nil {
		t.Fatalf("UpsertChat() error = %v", err)
	}
	message := Message{
		ID:        "chat-1/remote-1",
		RemoteID:  "remote-1",
		ChatID:    "chat-1",
		Sender:    "Alice",
		Body:      "hello",
		Timestamp: time.Unix(1_700_000_000, 0),
	}

	inserted, err := db.AddIncomingMessage(ctx, message)
	if err != nil {
		t.Fatalf("AddIncomingMessage(first) error = %v", err)
	}
	if !inserted {
		t.Fatal("first AddIncomingMessage reported inserted=false")
	}
	inserted, err = db.AddIncomingMessage(ctx, message)
	if err != nil {
		t.Fatalf("AddIncomingMessage(second) error = %v", err)
	}
	if inserted {
		t.Fatal("duplicate AddIncomingMessage reported inserted=true")
	}

	chats, err := db.ListChats(ctx)
	if err != nil {
		t.Fatalf("ListChats() error = %v", err)
	}
	if len(chats) != 1 || chats[0].Unread != 1 {
		t.Fatalf("chats = %+v, want one unread message", chats)
	}

	if err := db.UpsertChatPreserveUnread(ctx, Chat{ID: "chat-1", Title: "Alice Updated"}); err != nil {
		t.Fatalf("UpsertChatPreserveUnread() error = %v", err)
	}
	chats, err = db.ListChats(ctx)
	if err != nil {
		t.Fatalf("ListChats() after preserve error = %v", err)
	}
	if chats[0].Unread != 1 || chats[0].Title != "Alice Updated" {
		t.Fatalf("preserved chat = %+v, want unread preserved and title updated", chats[0])
	}
}

func TestUpsertReactionAttachesToLoadedMessages(t *testing.T) {
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
	if err := store.AddMessage(ctx, Message{
		ID:        "m-1",
		ChatID:    "chat-1",
		Sender:    "Alice",
		SenderJID: "alice@s.whatsapp.net",
		Body:      "hello",
		Timestamp: time.Unix(1_700_000_000, 0),
	}); err != nil {
		t.Fatalf("AddMessage() error = %v", err)
	}
	if err := store.UpsertReaction(ctx, Reaction{
		MessageID:  "m-1",
		SenderJID:  "alice@s.whatsapp.net",
		Emoji:      "👍",
		Timestamp:  time.Unix(1_700_000_100, 0),
		UpdatedAt:  time.Unix(1_700_000_100, 0),
		IsOutgoing: false,
	}); err != nil {
		t.Fatalf("UpsertReaction() error = %v", err)
	}

	messages, err := store.ListMessages(ctx, "chat-1", 10)
	if err != nil {
		t.Fatalf("ListMessages() error = %v", err)
	}
	if len(messages) != 1 || len(messages[0].Reactions) != 1 {
		t.Fatalf("messages = %+v, want one reaction attached", messages)
	}
	if messages[0].Reactions[0].Emoji != "👍" || messages[0].Reactions[0].SenderJID != "alice@s.whatsapp.net" {
		t.Fatalf("reaction = %+v", messages[0].Reactions[0])
	}
}

func TestUpsertReactionClearsExistingReaction(t *testing.T) {
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
	if err := store.AddMessage(ctx, Message{
		ID:        "m-1",
		ChatID:    "chat-1",
		Sender:    "Alice",
		SenderJID: "alice@s.whatsapp.net",
		Body:      "hello",
		Timestamp: time.Unix(1_700_000_000, 0),
	}); err != nil {
		t.Fatalf("AddMessage() error = %v", err)
	}
	if err := store.UpsertReaction(ctx, Reaction{
		MessageID: "m-1",
		SenderJID: "alice@s.whatsapp.net",
		Emoji:     "👍",
		Timestamp: time.Unix(1_700_000_100, 0),
		UpdatedAt: time.Unix(1_700_000_100, 0),
	}); err != nil {
		t.Fatalf("UpsertReaction(add) error = %v", err)
	}
	if err := store.UpsertReaction(ctx, Reaction{
		MessageID: "m-1",
		SenderJID: "alice@s.whatsapp.net",
		Emoji:     "",
		UpdatedAt: time.Unix(1_700_000_200, 0),
	}); err != nil {
		t.Fatalf("UpsertReaction(clear) error = %v", err)
	}

	reactions, err := store.ListMessageReactions(ctx, "m-1")
	if err != nil {
		t.Fatalf("ListMessageReactions() error = %v", err)
	}
	if len(reactions) != 0 {
		t.Fatalf("reactions = %+v, want cleared", reactions)
	}
}

func TestChatTitleSourcePrecedenceRejectsWeakGroupFallback(t *testing.T) {
	ctx := context.Background()
	db, err := Open(filepath.Join(t.TempDir(), "state.sqlite3"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	if err := db.UpsertChat(ctx, Chat{
		ID:          "12345-678@g.us",
		JID:         "12345-678@g.us",
		Title:       "Project Group",
		TitleSource: ChatTitleSourceGroupSubject,
		Kind:        "group",
	}); err != nil {
		t.Fatalf("UpsertChat(real title) error = %v", err)
	}
	if err := db.UpsertChatPreserveUnread(ctx, Chat{
		ID:          "12345-678@g.us",
		JID:         "12345-678@g.us",
		Title:       "12345-678",
		TitleSource: ChatTitleSourceJID,
		Kind:        "group",
	}); err != nil {
		t.Fatalf("UpsertChat(weak title) error = %v", err)
	}

	chats, err := db.ListChats(ctx)
	if err != nil {
		t.Fatalf("ListChats() error = %v", err)
	}
	if len(chats) != 1 || chats[0].Title != "Project Group" || chats[0].TitleSource != ChatTitleSourceGroupSubject {
		t.Fatalf("chat after weak title = %+v, want original group subject", chats)
	}
}

func TestUpdateChatTitleIfExistsRepairsWeakGroupTitle(t *testing.T) {
	ctx := context.Background()
	db, err := Open(filepath.Join(t.TempDir(), "state.sqlite3"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	if err := db.UpsertChat(ctx, Chat{
		ID:          "12345-678@g.us",
		JID:         "12345-678@g.us",
		Title:       "12345-678",
		TitleSource: ChatTitleSourceJID,
		Kind:        "group",
	}); err != nil {
		t.Fatalf("UpsertChat(weak title) error = %v", err)
	}
	updated, err := db.UpdateChatTitleIfExists(ctx, Chat{
		ID:          "12345-678@g.us",
		JID:         "12345-678@g.us",
		Title:       "Project Group",
		TitleSource: ChatTitleSourceGroupSubject,
		Kind:        "group",
	})
	if err != nil {
		t.Fatalf("UpdateChatTitleIfExists() error = %v", err)
	}
	if !updated {
		t.Fatal("UpdateChatTitleIfExists() updated = false, want true")
	}
	chats, err := db.ListChats(ctx)
	if err != nil {
		t.Fatalf("ListChats() error = %v", err)
	}
	if len(chats) != 1 || chats[0].DisplayTitle() != "Project Group" {
		t.Fatalf("chat after repair = %+v, want Project Group", chats)
	}
}

func TestAddHistoricalMessageDoesNotIncrementUnread(t *testing.T) {
	ctx := context.Background()
	db, err := Open(filepath.Join(t.TempDir(), "state.sqlite3"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	if err := db.UpsertChat(ctx, Chat{ID: "chat-1", Title: "Alice"}); err != nil {
		t.Fatalf("UpsertChat() error = %v", err)
	}
	_, err = db.AddHistoricalMessage(ctx, Message{
		ID:        "chat-1/remote-old",
		RemoteID:  "remote-old",
		ChatID:    "chat-1",
		ChatJID:   "chat-1@s.whatsapp.net",
		Sender:    "Alice",
		SenderJID: "alice@s.whatsapp.net",
		Body:      "older imported message",
		Timestamp: time.Unix(1_600_000_000, 0),
		Status:    "received",
	})
	if err != nil {
		t.Fatalf("AddHistoricalMessage() error = %v", err)
	}

	chats, err := db.ListChats(ctx)
	if err != nil {
		t.Fatalf("ListChats() error = %v", err)
	}
	if len(chats) != 1 || chats[0].Unread != 0 {
		t.Fatalf("chats = %+v, want no unread increment", chats)
	}
}

func TestListMessagesBeforeAndOldestMessage(t *testing.T) {
	ctx := context.Background()
	db, err := Open(filepath.Join(t.TempDir(), "state.sqlite3"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	if err := db.UpsertChat(ctx, Chat{ID: "chat-1", Title: "Alice"}); err != nil {
		t.Fatalf("UpsertChat() error = %v", err)
	}
	base := time.Unix(1_700_000_000, 0)
	for i, message := range []Message{
		{ID: "m-1", RemoteID: "r-1", Body: "one", Timestamp: base},
		{ID: "m-2", RemoteID: "r-2", Body: "two", Timestamp: base.Add(time.Minute)},
		{ID: "m-3", RemoteID: "r-3", Body: "three", Timestamp: base.Add(2 * time.Minute)},
	} {
		message.ChatID = "chat-1"
		message.ChatJID = "chat-1@s.whatsapp.net"
		message.Sender = "Alice"
		if err := db.AddMessage(ctx, message); err != nil {
			t.Fatalf("AddMessage(%d) error = %v", i, err)
		}
	}

	oldest, ok, err := db.OldestMessage(ctx, "chat-1")
	if err != nil {
		t.Fatalf("OldestMessage() error = %v", err)
	}
	if !ok || oldest.ID != "m-1" || oldest.RemoteID != "r-1" {
		t.Fatalf("OldestMessage() = %+v ok=%v, want m-1", oldest, ok)
	}

	older, err := db.ListMessagesBefore(ctx, "chat-1", Message{ID: "m-3", Timestamp: base.Add(2 * time.Minute)}, 2)
	if err != nil {
		t.Fatalf("ListMessagesBefore() error = %v", err)
	}
	if len(older) != 2 || older[0].ID != "m-1" || older[1].ID != "m-2" {
		t.Fatalf("older messages = %+v, want m-1,m-2 in ascending order", older)
	}
}

func TestMessageQueriesHideEmptyRowsWithoutMedia(t *testing.T) {
	ctx := context.Background()
	db, err := Open(filepath.Join(t.TempDir(), "state.sqlite3"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	if err := db.UpsertChat(ctx, Chat{ID: "chat-1", Title: "Group", Kind: "group"}); err != nil {
		t.Fatalf("UpsertChat() error = %v", err)
	}
	base := time.Unix(1_700_000_000, 0)
	if err := db.AddMessage(ctx, Message{ID: "empty-old", ChatID: "chat-1", Sender: "Alice", Body: "", Timestamp: base}); err != nil {
		t.Fatalf("AddMessage(empty-old) error = %v", err)
	}
	if err := db.AddMessageWithMedia(ctx, Message{ID: "media", ChatID: "chat-1", Sender: "Alice", Body: "", Timestamp: base.Add(time.Minute)}, []MediaMetadata{{
		FileName:      "photo.jpg",
		MIMEType:      "image/jpeg",
		DownloadState: "remote",
	}}); err != nil {
		t.Fatalf("AddMessageWithMedia(media) error = %v", err)
	}
	if err := db.AddMessage(ctx, Message{ID: "body", ChatID: "chat-1", Sender: "Alice", Body: "visible body", Timestamp: base.Add(2 * time.Minute)}); err != nil {
		t.Fatalf("AddMessage(body) error = %v", err)
	}
	if err := db.AddMessage(ctx, Message{ID: "empty-new", ChatID: "chat-1", Sender: "Alice", Body: "", Timestamp: base.Add(3 * time.Minute)}); err != nil {
		t.Fatalf("AddMessage(empty-new) error = %v", err)
	}

	messages, err := db.ListMessages(ctx, "chat-1", 10)
	if err != nil {
		t.Fatalf("ListMessages() error = %v", err)
	}
	if len(messages) != 2 || messages[0].ID != "media" || messages[1].ID != "body" {
		t.Fatalf("visible messages = %+v, want media,body only", messages)
	}
	if len(messages[0].Media) != 1 || messages[0].Media[0].FileName != "photo.jpg" {
		t.Fatalf("media-only message metadata = %+v, want photo.jpg", messages[0].Media)
	}

	oldest, ok, err := db.OldestMessage(ctx, "chat-1")
	if err != nil {
		t.Fatalf("OldestMessage() error = %v", err)
	}
	if !ok || oldest.ID != "media" {
		t.Fatalf("OldestMessage() = %+v ok=%v, want media", oldest, ok)
	}

	older, err := db.ListMessagesBefore(ctx, "chat-1", Message{ID: "body", Timestamp: base.Add(2 * time.Minute)}, 10)
	if err != nil {
		t.Fatalf("ListMessagesBefore() error = %v", err)
	}
	if len(older) != 1 || older[0].ID != "media" {
		t.Fatalf("older messages = %+v, want only media", older)
	}

	chats, err := db.ListChats(ctx)
	if err != nil {
		t.Fatalf("ListChats() error = %v", err)
	}
	if len(chats) != 1 || chats[0].LastPreview != "visible body" {
		t.Fatalf("chat preview = %+v, want newest renderable body", chats)
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

func TestMessagePayloadRoundTripAndCascadeDelete(t *testing.T) {
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
	if err := store.AddMessage(ctx, Message{
		ID:        "m-1",
		ChatID:    "chat-1",
		Sender:    "Alice",
		Body:      "forwardable",
		Timestamp: time.Unix(1_700_000_000, 0),
	}); err != nil {
		t.Fatalf("AddMessage() error = %v", err)
	}
	payload := MessagePayload{
		MessageID: "m-1",
		Payload:   []byte{1, 2, 3},
		UpdatedAt: time.Unix(1_700_000_010, 0),
	}
	if err := store.UpsertMessagePayload(ctx, payload); err != nil {
		t.Fatalf("UpsertMessagePayload() error = %v", err)
	}
	got, ok, err := store.MessagePayload(ctx, "m-1")
	if err != nil {
		t.Fatalf("MessagePayload() error = %v", err)
	}
	if !ok || got.MessageID != payload.MessageID || string(got.Payload) != string(payload.Payload) || !got.UpdatedAt.Equal(payload.UpdatedAt) {
		t.Fatalf("MessagePayload() = %+v ok=%v, want %+v", got, ok, payload)
	}
	got.Payload[0] = 9
	got, ok, err = store.MessagePayload(ctx, "m-1")
	if err != nil {
		t.Fatalf("MessagePayload() after mutation error = %v", err)
	}
	if !ok || got.Payload[0] != 1 {
		t.Fatalf("MessagePayload() leaked mutable payload = %+v ok=%v", got, ok)
	}
	if err := store.DeleteMessage(ctx, "m-1"); err != nil {
		t.Fatalf("DeleteMessage() error = %v", err)
	}
	if _, ok, err := store.MessagePayload(ctx, "m-1"); err != nil || !ok {
		t.Fatalf("MessagePayload() after local delete ok=%v err=%v, want preserved", ok, err)
	}
	if _, err := store.db.ExecContext(ctx, `DELETE FROM messages WHERE id = ?`, "m-1"); err != nil {
		t.Fatalf("hard delete message error = %v", err)
	}
	if _, ok, err := store.MessagePayload(ctx, "m-1"); err != nil || ok {
		t.Fatalf("MessagePayload() after hard delete ok=%v err=%v, want missing", ok, err)
	}
}

func TestDeleteMessageForEveryoneMarksReasonAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "state.sqlite3")

	store, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	if err := store.UpsertChat(ctx, Chat{ID: "chat-1", JID: "chat-1@s.whatsapp.net", Title: "Alice"}); err != nil {
		t.Fatalf("UpsertChat() error = %v", err)
	}
	if err := store.AddMessage(ctx, Message{
		ID:         "chat-1/remote-1",
		RemoteID:   "remote-1",
		ChatID:     "chat-1",
		ChatJID:    "chat-1@s.whatsapp.net",
		Sender:     "me",
		Body:       "remove from everyone",
		Timestamp:  time.Unix(1_700_000_000, 0),
		IsOutgoing: true,
		Status:     "sent",
	}); err != nil {
		t.Fatalf("AddMessage() error = %v", err)
	}

	deleted, err := store.DeleteMessageForEveryone(ctx, "chat-1/remote-1")
	if err != nil {
		t.Fatalf("DeleteMessageForEveryone() error = %v", err)
	}
	if !deleted {
		t.Fatal("DeleteMessageForEveryone() deleted = false, want true")
	}

	var body, reason string
	var deletedAt int64
	if err := store.db.QueryRowContext(ctx, `
		SELECT body, deleted_at, deleted_reason
		FROM messages
		WHERE id = ?
	`, "chat-1/remote-1").Scan(&body, &deletedAt, &reason); err != nil {
		t.Fatalf("query deleted message: %v", err)
	}
	if body != "" || deletedAt == 0 || reason != "everyone" {
		t.Fatalf("deleted row = body %q deletedAt %d reason %q", body, deletedAt, reason)
	}

	results, err := store.SearchMessages(ctx, "chat-1", "remove", 10)
	if err != nil {
		t.Fatalf("SearchMessages() error = %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("search results after delete = %+v, want none", results)
	}

	deleted, err = store.DeleteMessageForEveryone(ctx, "chat-1/remote-1")
	if err != nil {
		t.Fatalf("DeleteMessageForEveryone(second) error = %v", err)
	}
	if deleted {
		t.Fatal("DeleteMessageForEveryone(second) deleted = true, want false")
	}
}

func TestUpdateMessageBodyUpdatesEditedAtAndFTS(t *testing.T) {
	ctx := context.Background()
	store, err := Open(filepath.Join(t.TempDir(), "state.sqlite3"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	if err := store.UpsertChat(ctx, Chat{ID: "chat-1", Title: "Alice"}); err != nil {
		t.Fatalf("UpsertChat() error = %v", err)
	}
	when := time.Unix(1_700_000_000, 0)
	if err := store.AddMessage(ctx, Message{
		ID:         "chat-1/remote-1",
		RemoteID:   "remote-1",
		ChatID:     "chat-1",
		Sender:     "me",
		Body:       "old report",
		Timestamp:  when,
		IsOutgoing: true,
		Status:     "sent",
	}); err != nil {
		t.Fatalf("AddMessage() error = %v", err)
	}
	editedAt := when.Add(time.Minute)
	updated, err := store.UpdateMessageBody(ctx, "chat-1/remote-1", "new report", editedAt)
	if err != nil {
		t.Fatalf("UpdateMessageBody() error = %v", err)
	}
	if !updated {
		t.Fatal("UpdateMessageBody() updated = false, want true")
	}

	messages, err := store.SearchMessages(ctx, "chat-1", "new", 10)
	if err != nil {
		t.Fatalf("SearchMessages(new) error = %v", err)
	}
	if len(messages) != 1 || messages[0].Body != "new report" || !messages[0].EditedAt.Equal(editedAt) {
		t.Fatalf("edited messages = %+v", messages)
	}
	old, err := store.SearchMessages(ctx, "chat-1", "old", 10)
	if err != nil {
		t.Fatalf("SearchMessages(old) error = %v", err)
	}
	if len(old) != 0 {
		t.Fatalf("old search after edit = %+v, want none", old)
	}
	chats, err := store.ListChats(ctx)
	if err != nil {
		t.Fatalf("ListChats() error = %v", err)
	}
	if len(chats) != 1 || chats[0].LastPreview != "new report" {
		t.Fatalf("chat preview after edit = %+v", chats)
	}
}

func TestListChatsUsesStickerPreviewLabel(t *testing.T) {
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
	if err := store.AddMessage(ctx, Message{
		ID:        "m-1",
		ChatID:    "chat-1",
		Sender:    "Alice",
		Timestamp: time.Unix(1_700_000_000, 0),
	}); err != nil {
		t.Fatalf("AddMessage() error = %v", err)
	}
	if err := store.UpsertMediaMetadata(ctx, MediaMetadata{
		MessageID:          "m-1",
		Kind:               "sticker",
		MIMEType:           "image/webp",
		FileName:           "sticker.webp",
		DownloadState:      "remote",
		IsAnimated:         true,
		AccessibilityLabel: "laughing cat",
	}); err != nil {
		t.Fatalf("UpsertMediaMetadata() error = %v", err)
	}

	chats, err := store.ListChats(ctx)
	if err != nil {
		t.Fatalf("ListChats() error = %v", err)
	}
	if len(chats) != 1 || chats[0].LastPreview != "Sticker: laughing cat" {
		t.Fatalf("ListChats() = %+v, want sticker preview label", chats)
	}
}

func TestSetChatAvatarPersistsFields(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "state.sqlite3")

	store, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	if err := store.UpsertChat(ctx, Chat{ID: "chat-1", JID: "alice@s.whatsapp.net", Title: "Alice"}); err != nil {
		t.Fatalf("UpsertChat() error = %v", err)
	}
	when := time.Unix(1_700_000_123, 0)
	if err := store.SetChatAvatar(ctx, "chat-1", "avatar-1", "/tmp/avatar.png", "/tmp/avatar-thumb.png", when); err != nil {
		t.Fatalf("SetChatAvatar() error = %v", err)
	}

	chat, ok, err := store.ChatByID(ctx, "chat-1")
	if err != nil {
		t.Fatalf("ChatByID() error = %v", err)
	}
	if !ok {
		t.Fatal("ChatByID() ok = false")
	}
	if chat.AvatarID != "avatar-1" || chat.AvatarPath != "/tmp/avatar.png" || chat.AvatarThumbPath != "/tmp/avatar-thumb.png" || !chat.AvatarUpdatedAt.Equal(when) {
		t.Fatalf("chat avatar fields = %+v", chat)
	}
}

func TestUpsertMediaMetadataPreservesExistingLocalFileWhenUpdateOnlyHasThumbnail(t *testing.T) {
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
	if err := store.AddMessageWithMedia(ctx, Message{
		ID:         "m-1",
		ChatID:     "chat-1",
		Sender:     "me",
		Timestamp:  time.Unix(1_700_000_000, 0),
		IsOutgoing: true,
	}, []MediaMetadata{{
		FileName:      "photo.jpg",
		MIMEType:      "image/jpeg",
		SizeBytes:     2048,
		LocalPath:     "/home/me/photo.jpg",
		DownloadState: "downloaded",
	}}); err != nil {
		t.Fatalf("AddMessageWithMedia() error = %v", err)
	}

	if err := store.UpsertMediaMetadata(ctx, MediaMetadata{
		MessageID:     "m-1",
		FileName:      "photo.jpg",
		MIMEType:      "image/jpeg",
		ThumbnailPath: "/home/me/thumb.jpg",
		DownloadState: "remote",
	}); err != nil {
		t.Fatalf("UpsertMediaMetadata() error = %v", err)
	}

	media, err := store.MediaMetadata(ctx, "m-1")
	if err != nil {
		t.Fatalf("MediaMetadata() error = %v", err)
	}
	if media.LocalPath != "/home/me/photo.jpg" || media.ThumbnailPath != "/home/me/thumb.jpg" || media.DownloadState != "downloaded" {
		t.Fatalf("MediaMetadata() = %+v, want local file preserved and thumbnail added", media)
	}
}

func TestMediaDownloadDescriptorRoundTrip(t *testing.T) {
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
	if err := store.AddMessage(ctx, Message{
		ID:        "m-1",
		ChatID:    "chat-1",
		Sender:    "Alice",
		Body:      "photo",
		Timestamp: time.Unix(1_700_000_000, 0),
	}); err != nil {
		t.Fatalf("AddMessage() error = %v", err)
	}

	descriptor := MediaDownloadDescriptor{
		MessageID:     "m-1",
		Kind:          "image",
		URL:           "https://mmg.whatsapp.net/file",
		DirectPath:    "/v/t62.7118-24/example",
		MediaKey:      []byte{1, 2, 3},
		FileSHA256:    []byte{4, 5, 6},
		FileEncSHA256: []byte{7, 8, 9},
		FileLength:    42,
		UpdatedAt:     time.Unix(1_700_000_010, 0),
	}
	if err := store.UpsertMediaDownloadDescriptor(ctx, descriptor); err != nil {
		t.Fatalf("UpsertMediaDownloadDescriptor() error = %v", err)
	}

	got, ok, err := store.MediaDownloadDescriptor(ctx, "m-1")
	if err != nil {
		t.Fatalf("MediaDownloadDescriptor() error = %v", err)
	}
	if !ok {
		t.Fatal("MediaDownloadDescriptor() ok = false")
	}
	if got.MessageID != descriptor.MessageID ||
		got.Kind != descriptor.Kind ||
		got.URL != descriptor.URL ||
		got.DirectPath != descriptor.DirectPath ||
		got.FileLength != descriptor.FileLength ||
		!got.UpdatedAt.Equal(descriptor.UpdatedAt) ||
		string(got.MediaKey) != string(descriptor.MediaKey) ||
		string(got.FileSHA256) != string(descriptor.FileSHA256) ||
		string(got.FileEncSHA256) != string(descriptor.FileEncSHA256) {
		t.Fatalf("MediaDownloadDescriptor() = %+v, want %+v", got, descriptor)
	}

	missing, ok, err := store.MediaDownloadDescriptor(ctx, "missing")
	if err != nil {
		t.Fatalf("MediaDownloadDescriptor(missing) error = %v", err)
	}
	if ok || missing.MessageID != "" {
		t.Fatalf("MediaDownloadDescriptor(missing) = %+v ok=%v, want empty false", missing, ok)
	}
}

func TestUpsertMediaMetadataWithDownloadPersistsBoth(t *testing.T) {
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
	if err := store.AddMessage(ctx, Message{
		ID:        "m-1",
		ChatID:    "chat-1",
		Sender:    "Alice",
		Body:      "photo",
		Timestamp: time.Unix(1_700_000_000, 0),
	}); err != nil {
		t.Fatalf("AddMessage() error = %v", err)
	}

	if err := store.UpsertMediaMetadataWithDownload(ctx, MediaMetadata{
		MessageID:     "m-1",
		MIMEType:      "image/jpeg",
		FileName:      "photo.jpg",
		SizeBytes:     2048,
		DownloadState: "remote",
	}, &MediaDownloadDescriptor{
		Kind:          "image",
		DirectPath:    "/v/t62.7118-24/example",
		MediaKey:      []byte{1},
		FileSHA256:    []byte{2},
		FileEncSHA256: []byte{3},
		FileLength:    2048,
	}); err != nil {
		t.Fatalf("UpsertMediaMetadataWithDownload() error = %v", err)
	}

	media, err := store.MediaMetadata(ctx, "m-1")
	if err != nil {
		t.Fatalf("MediaMetadata() error = %v", err)
	}
	if media.MIMEType != "image/jpeg" || media.FileName != "photo.jpg" || media.DownloadState != "remote" {
		t.Fatalf("MediaMetadata() = %+v", media)
	}
	descriptor, ok, err := store.MediaDownloadDescriptor(ctx, "m-1")
	if err != nil {
		t.Fatalf("MediaDownloadDescriptor() error = %v", err)
	}
	if !ok || descriptor.MessageID != "m-1" || descriptor.Kind != "image" || descriptor.DirectPath == "" {
		t.Fatalf("MediaDownloadDescriptor() = %+v ok=%v", descriptor, ok)
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
	if stats.Chats < 4 || stats.Messages < 10 || stats.Drafts < 1 || stats.MediaItems < 1 {
		t.Fatalf("Stats() after seed = %+v, want at least chats=4 messages=10 drafts=1 media=1", stats)
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
	var demoMedia MediaMetadata
	for _, message := range projectMessages {
		if len(message.Media) > 0 {
			demoMedia = message.Media[0]
			break
		}
	}
	if demoMedia.LocalPath == "" || demoMedia.MIMEType != "image/png" {
		t.Fatalf("demo media = %+v, want local image/png media", demoMedia)
	}
	if _, err := os.Stat(demoMedia.LocalPath); err != nil {
		t.Fatalf("demo media file stat error = %v", err)
	}

	if err := store.ClearDemoData(ctx); err != nil {
		t.Fatalf("ClearDemoData() error = %v", err)
	}

	stats, err = store.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats() after clear error = %v", err)
	}
	if stats.Chats != 0 || stats.Messages != 0 || stats.Drafts != 0 || stats.MediaItems != 0 {
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
	if len(applied) != 11 || len(pending) != 0 {
		t.Fatalf("MigrationStatus() applied=%v pending=%v, want eleven applied and none pending", applied, pending)
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
