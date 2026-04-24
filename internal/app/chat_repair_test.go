package app

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"vimwhat/internal/store"
	"vimwhat/internal/ui"
)

func TestRepairCanonicalDirectChatsMergesAliasRows(t *testing.T) {
	ctx := context.Background()
	db := openAppTestStore(t)

	canonicalID := "221938140102739@lid"
	aliasID := "558981428437@s.whatsapp.net"
	when := time.Unix(1_700_000_000, 0)

	for _, chat := range []store.Chat{
		{ID: canonicalID, JID: canonicalID, Title: "José Otávio", TitleSource: store.ChatTitleSourcePushName, Kind: "direct"},
		{ID: aliasID, JID: aliasID, Title: "J Otávio", TitleSource: store.ChatTitleSourceContactDisplay, Kind: "direct"},
	} {
		if err := db.UpsertChat(ctx, chat); err != nil {
			t.Fatalf("UpsertChat(%s) error = %v", chat.ID, err)
		}
	}
	if err := db.AddMessage(ctx, store.Message{
		ID:        aliasID + "/REM1",
		RemoteID:  "REM1",
		ChatID:    aliasID,
		ChatJID:   aliasID,
		Sender:    "José Otávio",
		SenderJID: aliasID,
		Body:      "bom dia",
		Timestamp: when,
		Status:    "received",
	}); err != nil {
		t.Fatalf("AddMessage(alias) error = %v", err)
	}

	session := &fakeLiveWhatsAppSession{
		canonicalChatID: map[string]string{
			aliasID:     canonicalID,
			canonicalID: canonicalID,
		},
	}
	if err := repairCanonicalDirectChats(ctx, db, session); err != nil {
		t.Fatalf("repairCanonicalDirectChats() error = %v", err)
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
		t.Fatalf("canonical chat missing after repair")
	}
	if chat.Title != "J Otávio" || chat.TitleSource != store.ChatTitleSourceContactDisplay {
		t.Fatalf("merged chat title = %q/%q", chat.Title, chat.TitleSource)
	}
	message, ok, err := db.MessageByID(ctx, canonicalID+"/REM1")
	if err != nil {
		t.Fatalf("MessageByID() error = %v", err)
	}
	if !ok || message.ChatID != canonicalID || message.SenderJID != canonicalID {
		t.Fatalf("merged message = %+v", message)
	}
}

func TestHandleTextSendRequestCanonicalizesQueuedDirectMessage(t *testing.T) {
	ctx := context.Background()
	db := openAppTestStore(t)

	canonicalID := "92221789466668@lid"
	aliasID := "48725100804@s.whatsapp.net"
	if err := db.UpsertChat(ctx, store.Chat{
		ID:          canonicalID,
		JID:         canonicalID,
		Title:       "Aleksander Wąsowicz",
		TitleSource: store.ChatTitleSourceContactDisplay,
		Kind:        "direct",
	}); err != nil {
		t.Fatalf("UpsertChat() error = %v", err)
	}

	session := &fakeLiveWhatsAppSession{
		sends:       make(chan fakeSendRequest, 1),
		generatedID: "REMOTE1",
		canonicalChatID: map[string]string{
			aliasID: aliasID, // overwritten below after normalization
		},
	}
	session.canonicalChatID[aliasID] = canonicalID
	updates := make(chan ui.LiveUpdate, 8)
	result := make(chan textSendQueuedResult, 1)

	handleTextSendRequest(ctx, db, session, updates, nil, true, textSendRequest{
		ChatID: aliasID,
		Body:   "hello canonical world",
		Result: result,
	})

	queued := <-result
	if queued.Err != nil {
		t.Fatalf("queued send error = %v", queued.Err)
	}
	if queued.Message.ChatID != canonicalID || queued.Message.ChatJID != canonicalID || queued.Message.ID != canonicalID+"/REMOTE1" {
		t.Fatalf("queued message = %+v, want canonical direct ids", queued.Message)
	}
	request := <-session.sends
	if request.request.ChatJID != canonicalID {
		t.Fatalf("SendText() chat = %q, want canonical chat id %q", request.request.ChatJID, canonicalID)
	}

	messages, err := db.ListMessages(ctx, canonicalID, 10)
	if err != nil {
		t.Fatalf("ListMessages() error = %v", err)
	}
	if len(messages) != 1 || messages[0].ID != canonicalID+"/REMOTE1" || messages[0].Status != "sent" {
		t.Fatalf("stored messages = %+v", messages)
	}
}

func openAppTestStore(t *testing.T) *store.Store {
	t.Helper()

	db, err := store.Open(filepath.Join(t.TempDir(), "state.sqlite3"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})
	return db
}
