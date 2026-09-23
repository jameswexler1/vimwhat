package whatsapp

import (
	"context"
	"path/filepath"
	"sync"
	"testing"

	"vimwhat/internal/store"
)

func TestContactNamesSurviveEventOrderingAndAliases(t *testing.T) {
	for _, order := range []string{"contact-first", "chat-first", "concurrent"} {
		for _, alias := range []bool{false, true} {
			t.Run(order+map[bool]string{true: "/alias", false: "/direct"}[alias], func(t *testing.T) {
				ctx := context.Background()
				db, err := store.Open(filepath.Join(t.TempDir(), "state.sqlite3"))
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
				id := "12345@s.whatsapp.net"
				canonical := id
				if alias {
					canonical = "54321@lid"
				}
				contact := Event{Kind: EventContactUpsert, Contact: ContactEvent{
					JID: id, ChatID: canonical, DisplayName: "Alice", AliasIDs: []string{id},
				}}
				chat := Event{Kind: EventChatUpsert, Chat: ChatEvent{
					ID: canonical, JID: canonical, AliasIDs: []string{id},
					Title: "12345", TitleSource: store.ChatTitleSourceJID, Kind: "direct",
				}}
				ingestor := Ingestor{Store: db}
				apply := func(event Event) {
					if _, err := ingestor.Apply(ctx, event); err != nil {
						t.Error(err)
					}
				}
				switch order {
				case "contact-first":
					apply(contact)
					apply(chat)
				case "chat-first":
					apply(chat)
					apply(contact)
				default:
					var wg sync.WaitGroup
					for _, event := range []Event{contact, chat} {
						wg.Add(1)
						go func() { defer wg.Done(); apply(event) }()
					}
					wg.Wait()
				}
				chats, err := db.ListChats(ctx)
				if err != nil || len(chats) != 1 || chats[0].Title != "Alice" {
					t.Fatalf("chats = %+v, err = %v", chats, err)
				}
			})
		}
	}
}

func TestStartupAppStateIncludesContactNames(t *testing.T) {
	if !syncedAppStateEvent(Event{Kind: EventContactUpsert}) {
		t.Fatal("contact updates excluded from app-state import")
	}
}
