package app

import (
	"context"
	"path/filepath"
	"testing"

	"vimwhat/internal/store"
	"vimwhat/internal/ui"
)

func TestRetryTextReusesDurableID(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "state.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.UpsertChat(ctx, store.Chat{ID: "123@s.whatsapp.net", Title: "Alice"}); err != nil {
		t.Fatal(err)
	}
	m := store.Message{ID: "m", RemoteID: "stable", ChatID: "123@s.whatsapp.net", ChatJID: "123@s.whatsapp.net", Sender: "me", Body: "retry", Status: "uncertain", IsOutgoing: true}
	if err := db.AddMessage(ctx, m); err != nil {
		t.Fatal(err)
	}
	live := &fakeLiveWhatsAppSession{sends: make(chan fakeSendRequest, 1)}
	result := make(chan mediaSendQueuedResult, 1)
	updates := make(chan ui.LiveUpdate, 2)
	handleOutgoingRetry(ctx, db, live, updates, nil, mediaSendRequest{RetryID: "m", Result: result})
	queued := <-result
	if queued.Err != nil || queued.Message.ID != "m" {
		t.Fatalf("queued %+v", queued)
	}
	request := <-live.sends
	if request.request.RemoteID != "stable" {
		t.Fatalf("new delivery ID: %+v", request)
	}
	stored, _, err := db.MessageByID(ctx, "m")
	if err != nil || stored.Status != "sent" {
		t.Fatalf("stored %+v %v", stored, err)
	}
}

func TestCancelledSendSettlesWithIndependentContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	writeCtx, done := backgroundStoreWriteContext(ctx)
	defer done()
	if writeCtx.Err() != nil {
		t.Fatalf("settlement context cancelled: %v", writeCtx.Err())
	}
	if outgoingFailureStatus(ctx, context.Canceled) != "uncertain" {
		t.Fatal("cancelled send must be uncertain")
	}
}
