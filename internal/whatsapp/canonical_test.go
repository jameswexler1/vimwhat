package whatsapp

import (
	"context"
	"path/filepath"
	"slices"
	"testing"
	"time"

	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
)

func TestCanonicalChatJIDUsesLIDMapping(t *testing.T) {
	client := newCanonicalTestClient(t, "48725100804", "92221789466668")

	got, err := client.CanonicalChatJID(context.Background(), "48725100804@s.whatsapp.net")
	if err != nil {
		t.Fatalf("CanonicalChatJID() error = %v", err)
	}
	if got != "92221789466668@lid" {
		t.Fatalf("CanonicalChatJID() = %q, want %q", got, "92221789466668@lid")
	}

	got, err = client.CanonicalChatJID(context.Background(), "92221789466668@lid")
	if err != nil {
		t.Fatalf("CanonicalChatJID(lid) error = %v", err)
	}
	if got != "92221789466668@lid" {
		t.Fatalf("CanonicalChatJID(lid) = %q, want %q", got, "92221789466668@lid")
	}
}

func TestNormalizeMessageEventCanonicalizesMappedDirectChat(t *testing.T) {
	client := newCanonicalTestClient(t, "48725100804", "92221789466668")
	when := time.Unix(1_700_000_000, 0)
	pn := types.NewJID("48725100804", types.DefaultUserServer)
	lid := types.NewJID("92221789466668", types.HiddenUserServer)

	normalized := client.normalizeWhatsmeowEvent(context.Background(), &events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{
				Chat:      pn,
				Sender:    pn,
				SenderAlt: lid,
			},
			ID:        "MSG1",
			PushName:  "Aleksander Wąsowicz",
			Timestamp: when,
		},
		Message: &waE2E.Message{Conversation: proto.String("hello from lid world")},
	})

	if len(normalized) != 2 {
		t.Fatalf("len(normalized) = %d, want 2: %+v", len(normalized), normalized)
	}
	chat := normalized[0].Chat
	if chat.ID != "92221789466668@lid" || chat.JID != "92221789466668@lid" {
		t.Fatalf("chat event = %+v, want canonical lid chat", chat)
	}
	if !slices.Contains(chat.AliasIDs, "48725100804@s.whatsapp.net") {
		t.Fatalf("chat aliases = %v, want phone-number alias", chat.AliasIDs)
	}
	message := normalized[1].Message
	if message.ID != "92221789466668@lid/MSG1" ||
		message.ChatID != "92221789466668@lid" ||
		message.ChatJID != "92221789466668@lid" ||
		message.SenderJID != "92221789466668@lid" {
		t.Fatalf("message event = %+v, want canonical lid identifiers", message)
	}
}

func newCanonicalTestClient(t *testing.T, pnUser, lidUser string) *Client {
	t.Helper()

	ctx := context.Background()
	client, err := OpenSession(ctx, filepath.Join(t.TempDir(), "session.sqlite3"))
	if err != nil {
		t.Fatalf("OpenSession() error = %v", err)
	}
	t.Cleanup(func() {
		_ = client.Close()
	})

	pn := types.NewJID(pnUser, types.DefaultUserServer)
	lid := types.NewJID(lidUser, types.HiddenUserServer)
	if err := client.container.LIDMap.PutLIDMapping(ctx, lid, pn); err != nil {
		t.Fatalf("PutLIDMapping() error = %v", err)
	}
	return client
}
