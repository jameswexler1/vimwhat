package whatsapp

import (
	"testing"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waE2E"
	waHistorySync "go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/proto/waWeb"
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
				URL:           proto.String("https://mmg.whatsapp.net/file"),
				Mimetype:      proto.String("image/jpeg"),
				Caption:       proto.String("photo caption"),
				FileLength:    proto.Uint64(42),
				MediaKey:      []byte{1, 2, 3},
				FileSHA256:    []byte{4, 5, 6},
				FileEncSHA256: []byte{7, 8, 9},
				DirectPath:    proto.String("/v/t62.7118-24/example"),
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
		media.DownloadState != "remote" ||
		media.Download.Kind != "image" ||
		media.Download.URL != "https://mmg.whatsapp.net/file" ||
		media.Download.DirectPath != "/v/t62.7118-24/example" ||
		media.Download.FileLength != 42 ||
		string(media.Download.MediaKey) != string([]byte{1, 2, 3}) ||
		string(media.Download.FileSHA256) != string([]byte{4, 5, 6}) ||
		string(media.Download.FileEncSHA256) != string([]byte{7, 8, 9}) {
		t.Fatalf("media event = %+v", normalized[2])
	}
}

func TestMediaMetadataExtractsDownloadDescriptorsByKind(t *testing.T) {
	when := time.Unix(1_700_000_000, 0)
	tests := []struct {
		name     string
		message  *waE2E.Message
		wantKind string
		wantName string
	}{
		{
			name: "video",
			message: &waE2E.Message{VideoMessage: &waE2E.VideoMessage{
				Mimetype:      proto.String("video/mp4"),
				URL:           proto.String("https://example/video"),
				DirectPath:    proto.String("/video"),
				MediaKey:      []byte{1},
				FileSHA256:    []byte{2},
				FileEncSHA256: []byte{3},
				FileLength:    proto.Uint64(11),
			}},
			wantKind: "video",
		},
		{
			name: "audio",
			message: &waE2E.Message{AudioMessage: &waE2E.AudioMessage{
				Mimetype:      proto.String("audio/ogg"),
				URL:           proto.String("https://example/audio"),
				DirectPath:    proto.String("/audio"),
				MediaKey:      []byte{4},
				FileSHA256:    []byte{5},
				FileEncSHA256: []byte{6},
				FileLength:    proto.Uint64(12),
			}},
			wantKind: "audio",
		},
		{
			name: "document",
			message: &waE2E.Message{DocumentMessage: &waE2E.DocumentMessage{
				Mimetype:      proto.String("application/pdf"),
				FileName:      proto.String("report.pdf"),
				URL:           proto.String("https://example/document"),
				DirectPath:    proto.String("/document"),
				MediaKey:      []byte{7},
				FileSHA256:    []byte{8},
				FileEncSHA256: []byte{9},
				FileLength:    proto.Uint64(13),
			}},
			wantKind: "document",
			wantName: "report.pdf",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			media, ok := mediaMetadata("msg-1", tt.message, when)
			if !ok {
				t.Fatal("mediaMetadata() ok = false")
			}
			if media.Download.Kind != tt.wantKind ||
				media.Download.MessageID != "msg-1" ||
				media.Download.URL == "" ||
				media.Download.DirectPath == "" ||
				media.Download.FileLength == 0 ||
				!media.Download.UpdatedAt.Equal(when) {
				t.Fatalf("download descriptor = %+v, want kind/source/length/time", media.Download)
			}
			if tt.wantName != "" && media.FileName != tt.wantName {
				t.Fatalf("FileName = %q, want %q", media.FileName, tt.wantName)
			}
		})
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

func TestNormalizeHistorySyncEventMarksMessagesHistorical(t *testing.T) {
	when := uint64(1_700_000_000)
	syncType := waHistorySync.HistorySync_ON_DEMAND
	transferComplete := true
	transferType := waHistorySync.Conversation_COMPLETE_ON_DEMAND_SYNC_BUT_MORE_MSG_REMAIN_ON_PRIMARY
	client := &Client{client: &whatsmeow.Client{}}

	normalized := client.normalizeWhatsmeowEvent(&events.HistorySync{
		Data: &waHistorySync.HistorySync{
			SyncType: &syncType,
			Conversations: []*waHistorySync.Conversation{{
				ID:                       proto.String("12345@s.whatsapp.net"),
				Name:                     proto.String("Alice"),
				LastMsgTimestamp:         proto.Uint64(when),
				EndOfHistoryTransfer:     proto.Bool(transferComplete),
				EndOfHistoryTransferType: &transferType,
				Messages: []*waHistorySync.HistorySyncMsg{{
					Message: &waWeb.WebMessageInfo{
						Key: &waCommon.MessageKey{
							RemoteJID: proto.String("12345@s.whatsapp.net"),
							FromMe:    proto.Bool(false),
							ID:        proto.String("OLD1"),
						},
						Message:          &waE2E.Message{Conversation: proto.String("older message")},
						MessageTimestamp: proto.Uint64(when),
						PushName:         proto.String("Alice"),
					},
				}},
			}},
		},
	})

	var sawHistoricalMessage bool
	var sawHistoryStatus bool
	for _, event := range normalized {
		switch event.Kind {
		case EventMessageUpsert:
			if event.Message.ID == "12345@s.whatsapp.net/OLD1" &&
				event.Message.Body == "older message" &&
				event.Message.Historical {
				sawHistoricalMessage = true
			}
		case EventHistoryStatus:
			if event.History.ChatID == "12345@s.whatsapp.net" &&
				event.History.Messages == 1 &&
				!event.History.Exhausted {
				sawHistoryStatus = true
			}
		}
	}
	if !sawHistoricalMessage || !sawHistoryStatus {
		t.Fatalf("normalized history events = %+v, want historical message and status", normalized)
	}
}

func TestHistoryTerminalReasonOnlyExhaustsForTerminalTransferTypes(t *testing.T) {
	tests := []struct {
		name string
		kind waHistorySync.Conversation_EndOfHistoryTransferType
		want string
	}{
		{
			name: "more remains on primary",
			kind: waHistorySync.Conversation_COMPLETE_ON_DEMAND_SYNC_BUT_MORE_MSG_REMAIN_ON_PRIMARY,
			want: "",
		},
		{
			name: "no more remains",
			kind: waHistorySync.Conversation_COMPLETE_AND_NO_MORE_MESSAGE_REMAIN_ON_PRIMARY,
			want: "no_more",
		},
		{
			name: "no access",
			kind: waHistorySync.Conversation_COMPLETE_ON_DEMAND_SYNC_WITH_MORE_MSG_ON_PRIMARY_BUT_NO_ACCESS,
			want: "no_access",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conversation := &waHistorySync.Conversation{EndOfHistoryTransferType: &tt.kind}
			if got := historyTerminalReason(conversation); got != tt.want {
				t.Fatalf("historyTerminalReason() = %q, want %q", got, tt.want)
			}
		})
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
