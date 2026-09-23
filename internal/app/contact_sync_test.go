package app

import (
	"context"
	"path/filepath"
	"testing"

	"vimwhat/internal/config"
	"vimwhat/internal/store"
	"vimwhat/internal/whatsapp"
)

func TestStartupSyncImportsContactNames(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "state.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	session := &fakeLiveWhatsAppSession{stickerSync: []whatsapp.Event{{
		Kind: whatsapp.EventContactUpsert, Contact: whatsapp.ContactEvent{
			JID: "123@s.whatsapp.net", DisplayName: "Alice",
		},
	}}}
	got := runStickerSync(ctx, db, session, config.Paths{}, stickerSyncRequest{}, false)
	if got.Result.Err != nil || !got.Update.Refresh {
		t.Fatalf("result = %+v", got)
	}
	contact, err := db.Contact(ctx, "123@s.whatsapp.net")
	if err != nil || contact.DisplayName != "Alice" {
		t.Fatalf("contact = %+v, err = %v", contact, err)
	}
}
