package whatsapp

import (
	"testing"
	"time"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
)

func TestNormalizeMessageEventExtractsTextQuoteAndMedia(t *testing.T) {
	when := time.Unix(1_700_000_000, 0)
	chat := types.NewJID("12345", types.DefaultUserServer)
	evt := &events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{
				Chat:   chat,
				Sender: chat,
			},
			ID:        "ABC123",
			PushName:  "Alice",
			Timestamp: when,
		},
		Message: &waE2E.Message{
			ImageMessage: &waE2E.ImageMessage{
				Mimetype:   proto.String("image/jpeg"),
				Caption:    proto.String("photo caption"),
				FileLength: proto.Uint64(42),
				ContextInfo: &waE2E.ContextInfo{
					StanzaID: proto.String("QUOTE1"),
				},
			},
		},
	}

	normalized := normalizeWhatsmeowEvent(evt)
	if len(normalized) != 3 {
		t.Fatalf("len(normalized) = %d, want 3: %+v", len(normalized), normalized)
	}
	if normalized[0].Kind != EventChatUpsert || normalized[0].Chat.Title != "Alice" {
		t.Fatalf("chat event = %+v", normalized[0])
	}
	message := normalized[1].Message
	if normalized[1].Kind != EventMessageUpsert ||
		message.ID != "12345@s.whatsapp.net/ABC123" ||
		message.Body != "photo caption" ||
		message.QuotedRemoteID != "QUOTE1" ||
		message.QuotedMessageID != "12345@s.whatsapp.net/QUOTE1" ||
		message.Status != "received" {
		t.Fatalf("message event = %+v", normalized[1])
	}
	media := normalized[2].Media
	if normalized[2].Kind != EventMediaMetadata ||
		media.MessageID != message.ID ||
		media.MIMEType != "image/jpeg" ||
		media.SizeBytes != 42 ||
		media.DownloadState != "remote" {
		t.Fatalf("media event = %+v", normalized[2])
	}
}

func TestNormalizeReceiptEventMapsRemoteIDsToLocalStatus(t *testing.T) {
	chat := types.NewJID("12345", types.DefaultUserServer)
	normalized := normalizeWhatsmeowEvent(&events.Receipt{
		MessageSource: types.MessageSource{Chat: chat},
		MessageIDs:    []types.MessageID{"ABC123"},
		Type:          types.ReceiptTypeRead,
	})
	if len(normalized) != 1 {
		t.Fatalf("len(normalized) = %d, want 1", len(normalized))
	}
	receipt := normalized[0].Receipt
	if normalized[0].Kind != EventReceiptUpdate ||
		receipt.MessageID != "12345@s.whatsapp.net/ABC123" ||
		receipt.Status != "read" {
		t.Fatalf("receipt event = %+v", normalized[0])
	}
}

func TestNormalizeConnectionEvents(t *testing.T) {
	normalized := normalizeWhatsmeowEvent(&events.Connected{})
	if len(normalized) != 1 || normalized[0].Connection.State != ConnectionOnline {
		t.Fatalf("connected normalized to %+v", normalized)
	}

	normalized = normalizeWhatsmeowEvent(&events.Disconnected{})
	if len(normalized) != 1 || normalized[0].Connection.State != ConnectionReconnecting {
		t.Fatalf("disconnected normalized to %+v", normalized)
	}
}
