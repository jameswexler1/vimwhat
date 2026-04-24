package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestMergeChatAliasMovesMessagesDraftsAndHistoryCursor(t *testing.T) {
	ctx := context.Background()
	db := openMergeTestStore(t)

	canonicalID := "221938140102739@lid"
	aliasID := "558981428437@s.whatsapp.net"
	when := time.Unix(1_700_000_000, 0)

	if err := db.UpsertChat(ctx, Chat{
		ID:            canonicalID,
		JID:           canonicalID,
		Title:         "José Otávio",
		TitleSource:   ChatTitleSourcePushName,
		Kind:          "direct",
		Unread:        1,
		LastMessageAt: when,
	}); err != nil {
		t.Fatalf("UpsertChat(canonical) error = %v", err)
	}
	if err := db.UpsertChat(ctx, Chat{
		ID:            aliasID,
		JID:           aliasID,
		Title:         "J Otávio",
		TitleSource:   ChatTitleSourceContactDisplay,
		Kind:          "direct",
		Unread:        2,
		Pinned:        true,
		LastMessageAt: when.Add(2 * time.Minute),
	}); err != nil {
		t.Fatalf("UpsertChat(alias) error = %v", err)
	}

	canonicalMessage := Message{
		ID:        canonicalID + "/L1",
		RemoteID:  "L1",
		ChatID:    canonicalID,
		ChatJID:   canonicalID,
		Sender:    "José Otávio",
		SenderJID: canonicalID,
		Body:      "Manda aí",
		Timestamp: when,
		Status:    "received",
	}
	if err := db.AddMessage(ctx, canonicalMessage); err != nil {
		t.Fatalf("AddMessage(canonical) error = %v", err)
	}

	aliasBase := Message{
		ID:         aliasID + "/P0",
		RemoteID:   "P0",
		ChatID:     aliasID,
		ChatJID:    aliasID,
		Sender:     "me",
		SenderJID:  "me",
		Body:       "Vou te mandar o binary",
		Timestamp:  when.Add(time.Minute),
		IsOutgoing: true,
		Status:     "sent",
	}
	if err := db.AddMessage(ctx, aliasBase); err != nil {
		t.Fatalf("AddMessage(alias base) error = %v", err)
	}

	aliasQuoted := Message{
		ID:              aliasID + "/P1",
		RemoteID:        "P1",
		ChatID:          aliasID,
		ChatJID:         aliasID,
		Sender:          "me",
		SenderJID:       "me",
		Body:            "Aqui",
		Timestamp:       when.Add(2 * time.Minute),
		IsOutgoing:      true,
		Status:          "sent",
		QuotedMessageID: aliasBase.ID,
		QuotedRemoteID:  aliasBase.RemoteID,
	}
	if err := db.AddMessageWithMedia(ctx, aliasQuoted, []MediaMetadata{{
		MessageID:     aliasQuoted.ID,
		Kind:          "image",
		MIMEType:      "image/jpeg",
		FileName:      "photo.jpg",
		LocalPath:     "/tmp/photo.jpg",
		DownloadState: "downloaded",
		UpdatedAt:     when.Add(2 * time.Minute),
	}}); err != nil {
		t.Fatalf("AddMessageWithMedia(alias quoted) error = %v", err)
	}
	if err := db.UpsertReaction(ctx, Reaction{
		MessageID: aliasQuoted.ID,
		SenderJID: aliasID,
		Emoji:     "🔥",
		Timestamp: when.Add(2 * time.Minute),
		UpdatedAt: when.Add(2 * time.Minute),
	}); err != nil {
		t.Fatalf("UpsertReaction(alias) error = %v", err)
	}
	if err := db.SaveDraft(ctx, aliasID, "draft on alias"); err != nil {
		t.Fatalf("SaveDraft(alias) error = %v", err)
	}
	if err := db.SetSyncCursor(ctx, historyExhaustedCursorName(aliasID), "no_more"); err != nil {
		t.Fatalf("SetSyncCursor(alias) error = %v", err)
	}
	if err := db.SaveUISnapshot(ctx, UISnapshot{
		Kind:      "pane",
		Name:      "state",
		ChatID:    aliasID,
		Value:     "alias-view",
		UpdatedAt: when.Add(3 * time.Minute),
	}); err != nil {
		t.Fatalf("SaveUISnapshot(alias) error = %v", err)
	}

	if err := db.MergeChatAlias(ctx, canonicalID, aliasID); err != nil {
		t.Fatalf("MergeChatAlias() error = %v", err)
	}

	if _, ok, err := db.ChatByID(ctx, aliasID); err != nil {
		t.Fatalf("ChatByID(alias) error = %v", err)
	} else if ok {
		t.Fatalf("alias chat %s still exists", aliasID)
	}
	chat, ok, err := db.ChatByID(ctx, canonicalID)
	if err != nil {
		t.Fatalf("ChatByID(canonical) error = %v", err)
	}
	if !ok {
		t.Fatalf("canonical chat %s missing after merge", canonicalID)
	}
	if chat.Title != "J Otávio" || chat.TitleSource != ChatTitleSourceContactDisplay {
		t.Fatalf("merged chat title = %q/%q, want alias contact-display title", chat.Title, chat.TitleSource)
	}
	if chat.Unread != 3 || !chat.Pinned {
		t.Fatalf("merged chat flags = %+v, want unread sum and pinned alias", chat)
	}

	messages, err := db.ListMessages(ctx, canonicalID, 10)
	if err != nil {
		t.Fatalf("ListMessages() error = %v", err)
	}
	if len(messages) != 3 {
		t.Fatalf("len(messages) = %d, want 3", len(messages))
	}

	quoted, ok, err := db.MessageByID(ctx, canonicalID+"/P1")
	if err != nil {
		t.Fatalf("MessageByID(quoted) error = %v", err)
	}
	if !ok {
		t.Fatalf("merged alias message missing")
	}
	if quoted.ChatID != canonicalID || quoted.ChatJID != canonicalID || quoted.QuotedMessageID != canonicalID+"/P0" {
		t.Fatalf("merged quoted message = %+v", quoted)
	}

	draft, err := db.Draft(ctx, canonicalID)
	if err != nil {
		t.Fatalf("Draft(canonical) error = %v", err)
	}
	if draft != "draft on alias" {
		t.Fatalf("Draft(canonical) = %q, want alias draft", draft)
	}
	cursor, err := db.SyncCursor(ctx, historyExhaustedCursorName(canonicalID))
	if err != nil {
		t.Fatalf("SyncCursor(canonical) error = %v", err)
	}
	if cursor != "no_more" {
		t.Fatalf("SyncCursor(canonical) = %q, want no_more", cursor)
	}
	snapshot, err := db.UISnapshot(ctx, "pane", "state", canonicalID)
	if err != nil {
		t.Fatalf("UISnapshot(canonical) error = %v", err)
	}
	if snapshot.Value != "alias-view" {
		t.Fatalf("UISnapshot(canonical).Value = %q, want alias snapshot", snapshot.Value)
	}

	reactions, err := db.ListMessageReactions(ctx, canonicalID+"/P1")
	if err != nil {
		t.Fatalf("ListMessageReactions() error = %v", err)
	}
	if len(reactions) != 1 || reactions[0].SenderJID != canonicalID || reactions[0].Emoji != "🔥" {
		t.Fatalf("merged reactions = %+v, want canonical direct sender jid", reactions)
	}
	media, err := db.MediaMetadata(ctx, canonicalID+"/P1")
	if err != nil {
		t.Fatalf("MediaMetadata() error = %v", err)
	}
	if media.LocalPath != "/tmp/photo.jpg" || media.FileName != "photo.jpg" {
		t.Fatalf("merged media = %+v", media)
	}
}

func TestMergeChatAliasDedupesDuplicateRemoteMessage(t *testing.T) {
	ctx := context.Background()
	db := openMergeTestStore(t)

	canonicalID := "92221789466668@lid"
	aliasID := "48725100804@s.whatsapp.net"
	when := time.Unix(1_700_000_100, 0)

	for _, chat := range []Chat{
		{ID: canonicalID, JID: canonicalID, Title: "Aleksander Wąsowicz", TitleSource: ChatTitleSourcePushName, Kind: "direct"},
		{ID: aliasID, JID: aliasID, Title: "Aleksander Wąsowicz", TitleSource: ChatTitleSourceContactDisplay, Kind: "direct"},
	} {
		if err := db.UpsertChat(ctx, chat); err != nil {
			t.Fatalf("UpsertChat(%s) error = %v", chat.ID, err)
		}
	}

	if err := db.AddMessage(ctx, Message{
		ID:        canonicalID + "/MSG1",
		RemoteID:  "MSG1",
		ChatID:    canonicalID,
		ChatJID:   canonicalID,
		Sender:    "Aleksander Wąsowicz",
		SenderJID: canonicalID,
		Body:      "",
		Timestamp: when,
		Status:    "received",
	}); err != nil {
		t.Fatalf("AddMessage(canonical) error = %v", err)
	}
	if err := db.AddMessageWithMedia(ctx, Message{
		ID:        aliasID + "/MSG1",
		RemoteID:  "MSG1",
		ChatID:    aliasID,
		ChatJID:   aliasID,
		Sender:    "Aleksander Wąsowicz",
		SenderJID: aliasID,
		Body:      "filled from alias",
		Timestamp: when,
		Status:    "received",
	}, []MediaMetadata{{
		MessageID:     aliasID + "/MSG1",
		Kind:          "image",
		MIMEType:      "image/jpeg",
		FileName:      "sticker.jpg",
		LocalPath:     "/tmp/sticker.jpg",
		DownloadState: "downloaded",
		UpdatedAt:     when,
	}}); err != nil {
		t.Fatalf("AddMessageWithMedia(alias) error = %v", err)
	}

	if err := db.MergeChatAlias(ctx, canonicalID, aliasID); err != nil {
		t.Fatalf("MergeChatAlias() error = %v", err)
	}

	messages, err := db.ListMessages(ctx, canonicalID, 10)
	if err != nil {
		t.Fatalf("ListMessages() error = %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("len(messages) = %d, want 1 after dedupe", len(messages))
	}
	if messages[0].ID != canonicalID+"/MSG1" || messages[0].Body != "filled from alias" {
		t.Fatalf("deduped message = %+v", messages[0])
	}
	media, err := db.MediaMetadata(ctx, canonicalID+"/MSG1")
	if err != nil {
		t.Fatalf("MediaMetadata() error = %v", err)
	}
	if media.LocalPath != "/tmp/sticker.jpg" {
		t.Fatalf("deduped media = %+v", media)
	}
}

func openMergeTestStore(t *testing.T) *Store {
	t.Helper()

	db, err := Open(filepath.Join(t.TempDir(), "state.sqlite3"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})
	return db
}
