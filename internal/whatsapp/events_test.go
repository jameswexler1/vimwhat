package whatsapp

import (
	"context"
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

	"vimwhat/internal/store"
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
	if normalized[0].Chat.TitleSource != store.ChatTitleSourcePushName {
		t.Fatalf("chat title source = %q, want push name", normalized[0].Chat.TitleSource)
	}
	message := normalized[1].Message
	if normalized[1].Kind != EventMessageUpsert ||
		message.ID != "12345@s.whatsapp.net/ABC123" ||
		message.Body != "photo caption" ||
		message.NotificationPreview != "photo caption" ||
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

func TestNormalizeOfflineSyncEvents(t *testing.T) {
	preview := normalizeWhatsmeowEvent(&events.OfflineSyncPreview{
		Total:          12,
		AppDataChanges: 2,
		Messages:       5,
		Notifications:  1,
		Receipts:       4,
	})
	if len(preview) != 1 || preview[0].Kind != EventOfflineSync {
		t.Fatalf("preview normalized = %+v, want one offline sync event", preview)
	}
	if got := preview[0].Offline; !got.Active ||
		got.Total != 12 ||
		got.AppDataChanges != 2 ||
		got.Messages != 5 ||
		got.Notifications != 1 ||
		got.Receipts != 4 {
		t.Fatalf("preview offline sync = %+v", got)
	}

	completed := normalizeWhatsmeowEvent(&events.OfflineSyncCompleted{Count: 12})
	if len(completed) != 1 || completed[0].Kind != EventOfflineSync {
		t.Fatalf("completed normalized = %+v, want one offline sync event", completed)
	}
	if got := completed[0].Offline; !got.Completed || got.Total != 12 || got.Processed != 12 {
		t.Fatalf("completed offline sync = %+v", got)
	}
}

func TestNormalizeMessageEventUsesJIDTitleForOutgoingDirectMessages(t *testing.T) {
	when := time.Unix(1_700_000_000, 0)
	chat := types.NewJID("12345", types.DefaultUserServer)
	sender := types.NewJID("99999", types.DefaultUserServer)
	normalized := normalizeWhatsmeowEvent(&events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{
				Chat:     chat,
				Sender:   sender,
				IsFromMe: true,
			},
			ID:        "OUT1",
			PushName:  "Gustavo",
			Timestamp: when,
		},
		Message: &waE2E.Message{Conversation: proto.String("sent from desktop")},
	})

	if len(normalized) != 2 {
		t.Fatalf("len(normalized) = %d, want 2: %+v", len(normalized), normalized)
	}
	if normalized[0].Kind != EventChatUpsert ||
		normalized[0].Chat.Title != "12345" ||
		normalized[0].Chat.TitleSource != store.ChatTitleSourceJID {
		t.Fatalf("chat event = %+v, want direct chat JID title fallback", normalized[0])
	}
	if normalized[1].Kind != EventMessageUpsert ||
		normalized[1].Message.Sender != "me" ||
		!normalized[1].Message.IsOutgoing {
		t.Fatalf("message event = %+v, want outgoing message from me", normalized[1])
	}
}

func TestNormalizeMessageEventSkipsEmptyUnsupportedMessages(t *testing.T) {
	when := time.Unix(1_700_000_000, 0)
	chat := types.NewJID("12345", types.DefaultUserServer)
	normalized := normalizeWhatsmeowEvent(&events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{
				Chat:   chat,
				Sender: chat,
			},
			ID:        "EMPTY1",
			PushName:  "Alice",
			Timestamp: when,
		},
		Message: &waE2E.Message{},
	})

	if len(normalized) != 1 {
		t.Fatalf("len(normalized) = %d, want chat event only: %+v", len(normalized), normalized)
	}
	if normalized[0].Kind != EventChatUpsert || normalized[0].Chat.ID != "12345@s.whatsapp.net" {
		t.Fatalf("chat event = %+v", normalized[0])
	}
}

func TestNormalizeMessageEventKeepsBodylessMediaMessages(t *testing.T) {
	when := time.Unix(1_700_000_000, 0)
	chat := types.NewJID("12345", types.DefaultUserServer)
	normalized := normalizeWhatsmeowEvent(&events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{
				Chat:   chat,
				Sender: chat,
			},
			ID:        "MEDIA1",
			PushName:  "Alice",
			Timestamp: when,
		},
		Message: &waE2E.Message{
			ImageMessage: &waE2E.ImageMessage{
				URL:           proto.String("https://mmg.whatsapp.net/file"),
				Mimetype:      proto.String("image/jpeg"),
				FileLength:    proto.Uint64(42),
				MediaKey:      []byte{1},
				FileSHA256:    []byte{2},
				FileEncSHA256: []byte{3},
				DirectPath:    proto.String("/v/t62.7118-24/example"),
			},
		},
	})

	if len(normalized) != 3 {
		t.Fatalf("len(normalized) = %d, want chat,message,media: %+v", len(normalized), normalized)
	}
	if normalized[1].Kind != EventMessageUpsert || normalized[1].Message.Body != "" || normalized[1].Message.NotificationPreview != "Image" {
		t.Fatalf("message event = %+v", normalized[1])
	}
	if normalized[2].Kind != EventMediaMetadata || normalized[2].Media.MessageID != normalized[1].Message.ID {
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
			media, ok := mediaMetadata("msg-1", &events.Message{Message: tt.message}, when)
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

func TestNormalizeMessageEventExtractsStickerMetadata(t *testing.T) {
	when := time.Unix(1_700_000_000, 0)
	chat := types.NewJID("12345", types.DefaultUserServer)
	normalized := normalizeWhatsmeowEvent(&events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{
				Chat:   chat,
				Sender: chat,
			},
			ID:        "STICKER1",
			PushName:  "Alice",
			Timestamp: when,
		},
		Message: &waE2E.Message{
			StickerMessage: &waE2E.StickerMessage{
				Mimetype:           proto.String("image/webp"),
				URL:                proto.String("https://example/sticker"),
				DirectPath:         proto.String("/sticker"),
				MediaKey:           []byte{1},
				FileSHA256:         []byte{2},
				FileEncSHA256:      []byte{3},
				FileLength:         proto.Uint64(77),
				IsAnimated:         proto.Bool(true),
				AccessibilityLabel: proto.String("thumbs up"),
				PngThumbnail:       []byte{0x89, 'P', 'N', 'G'},
			},
		},
	})

	if len(normalized) != 3 {
		t.Fatalf("len(normalized) = %d, want 3: %+v", len(normalized), normalized)
	}
	if normalized[1].Kind != EventMessageUpsert || normalized[1].Message.NotificationPreview != "Sticker: thumbs up" {
		t.Fatalf("message event = %+v", normalized[1])
	}
	media := normalized[2].Media
	if normalized[2].Kind != EventMediaMetadata ||
		media.Kind != "sticker" ||
		media.FileName != "sticker.webp" ||
		media.Download.Kind != "sticker" ||
		!media.IsAnimated ||
		media.IsLottie ||
		media.AccessibilityLabel != "thumbs up" ||
		len(media.ThumbnailData) == 0 {
		t.Fatalf("media event = %+v", normalized[2])
	}
}

func TestNormalizePictureEvent(t *testing.T) {
	chat := types.NewJID("12345", types.DefaultUserServer)
	when := time.Unix(1_700_000_000, 0)
	normalized := normalizeWhatsmeowEvent(&events.Picture{
		JID:       chat,
		Timestamp: when,
		PictureID: "avatar-1",
	})
	if len(normalized) != 1 || normalized[0].Kind != EventChatAvatarUpdate {
		t.Fatalf("normalized = %+v", normalized)
	}
	if normalized[0].Avatar.ChatID != chat.String() || normalized[0].Avatar.AvatarID != "avatar-1" || normalized[0].Avatar.Remove {
		t.Fatalf("avatar event = %+v", normalized[0].Avatar)
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

func TestNormalizeReactionMessageEvent(t *testing.T) {
	when := time.Unix(1_700_000_000, 0)
	chat := types.NewJID("12345", types.DefaultUserServer)
	normalized := normalizeWhatsmeowEvent(&events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{
				Chat:   chat,
				Sender: chat,
			},
			ID:        "REACTION1",
			PushName:  "Alice",
			Timestamp: when,
		},
		Message: &waE2E.Message{
			ReactionMessage: &waE2E.ReactionMessage{
				Key: &waCommon.MessageKey{
					RemoteJID: proto.String(chat.String()),
					ID:        proto.String("TARGET1"),
				},
				Text: proto.String("🔥"),
			},
		},
	})
	if len(normalized) != 2 {
		t.Fatalf("len(normalized) = %d, want chat+reaction", len(normalized))
	}
	if normalized[1].Kind != EventReactionUpdate {
		t.Fatalf("event kind = %s, want %s", normalized[1].Kind, EventReactionUpdate)
	}
	if normalized[1].Reaction.MessageID != "12345@s.whatsapp.net/TARGET1" || normalized[1].Reaction.Emoji != "🔥" {
		t.Fatalf("reaction event = %+v", normalized[1].Reaction)
	}
}

func TestNormalizeRevokeMessageEvent(t *testing.T) {
	when := time.Unix(1_700_000_000, 0)
	chat := types.NewJID("12345", types.DefaultUserServer)
	normalized := normalizeWhatsmeowEvent(&events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{
				Chat:     chat,
				Sender:   types.NewJID("99999", types.DefaultUserServer),
				IsFromMe: true,
			},
			ID:        "REVOKE1",
			Timestamp: when,
		},
		Message: &waE2E.Message{
			ProtocolMessage: &waE2E.ProtocolMessage{
				Key: &waCommon.MessageKey{
					RemoteJID: proto.String(chat.String()),
					FromMe:    proto.Bool(true),
					ID:        proto.String("TARGET1"),
				},
				Type: waE2E.ProtocolMessage_REVOKE.Enum(),
			},
		},
	})
	if len(normalized) != 2 {
		t.Fatalf("len(normalized) = %d, want chat+delete", len(normalized))
	}
	if normalized[1].Kind != EventMessageDelete {
		t.Fatalf("event kind = %s, want %s", normalized[1].Kind, EventMessageDelete)
	}
	if normalized[1].Delete.MessageID != "12345@s.whatsapp.net/TARGET1" ||
		normalized[1].Delete.RemoteID != "TARGET1" ||
		normalized[1].Delete.DeletedReason != "everyone" {
		t.Fatalf("delete event = %+v", normalized[1].Delete)
	}
}

func TestNormalizeEditedMessageEvent(t *testing.T) {
	when := time.Unix(1_700_000_000, 0)
	editMS := when.Add(time.Minute).UnixMilli()
	chat := types.NewJID("12345", types.DefaultUserServer)
	evt := (&events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{
				Chat:     chat,
				Sender:   types.NewJID("99999", types.DefaultUserServer),
				IsFromMe: true,
			},
			ID:        "EDIT1",
			Timestamp: when,
		},
		RawMessage: &waE2E.Message{
			EditedMessage: &waE2E.FutureProofMessage{
				Message: &waE2E.Message{
					ProtocolMessage: &waE2E.ProtocolMessage{
						Key: &waCommon.MessageKey{
							RemoteJID: proto.String(chat.String()),
							FromMe:    proto.Bool(true),
							ID:        proto.String("TARGET1"),
						},
						Type: waE2E.ProtocolMessage_MESSAGE_EDIT.Enum(),
						EditedMessage: &waE2E.Message{
							Conversation: proto.String("edited text"),
						},
						TimestampMS: proto.Int64(editMS),
					},
				},
			},
		},
	}).UnwrapRaw()

	normalized := normalizeWhatsmeowEvent(evt)
	if len(normalized) != 2 {
		t.Fatalf("len(normalized) = %d, want chat+edit", len(normalized))
	}
	if normalized[1].Kind != EventMessageEdit {
		t.Fatalf("event kind = %s, want %s", normalized[1].Kind, EventMessageEdit)
	}
	if normalized[1].Edit.MessageID != "12345@s.whatsapp.net/TARGET1" ||
		normalized[1].Edit.RemoteID != "TARGET1" ||
		normalized[1].Edit.Body != "edited text" ||
		normalized[1].Edit.EditedAt.UnixMilli() != editMS {
		t.Fatalf("edit event = %+v", normalized[1].Edit)
	}
}

func TestNormalizeChatPresenceEvent(t *testing.T) {
	chat := types.NewJID("12345", types.DefaultUserServer)
	sender := types.NewJID("67890", types.DefaultUserServer)
	normalized := normalizeWhatsmeowEvent(&events.ChatPresence{
		MessageSource: types.MessageSource{
			Chat:   chat,
			Sender: sender,
		},
		State: types.ChatPresenceComposing,
	})
	if len(normalized) != 1 || normalized[0].Kind != EventPresenceUpdate {
		t.Fatalf("normalized = %+v, want presence event", normalized)
	}
	if normalized[0].Presence.ChatID != chat.String() || normalized[0].Presence.SenderJID != sender.String() || !normalized[0].Presence.Typing {
		t.Fatalf("presence = %+v", normalized[0].Presence)
	}
}

func TestNormalizeGroupMessageUsesPlaceholderTitle(t *testing.T) {
	when := time.Unix(1_700_000_000, 0)
	chat := types.NewJID("12345-678", types.GroupServer)
	normalized := normalizeWhatsmeowEvent(&events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{
				Chat:   chat,
				Sender: types.NewJID("11111", types.DefaultUserServer),
			},
			ID:        "GROUP1",
			PushName:  "Alice",
			Timestamp: when,
		},
		Message: &waE2E.Message{Conversation: proto.String("hello group")},
	})
	if len(normalized) < 2 {
		t.Fatalf("normalized = %+v, want chat and message events", normalized)
	}
	chatEvent := normalized[0].Chat
	if normalized[0].Kind != EventChatUpsert ||
		chatEvent.Title != "Unnamed group" ||
		chatEvent.TitleSource != store.ChatTitleSourcePlaceholder {
		t.Fatalf("group chat event = %+v, want placeholder title", normalized[0])
	}
}

func TestNormalizeGroupInfoUsesSubjectTitle(t *testing.T) {
	chat := types.NewJID("12345-678", types.GroupServer)
	normalized := normalizeWhatsmeowEvent(&events.GroupInfo{
		JID: chat,
		Name: &types.GroupName{
			Name: "Project Group",
		},
	})
	if len(normalized) != 1 ||
		normalized[0].Kind != EventChatUpsert ||
		normalized[0].Chat.Title != "Project Group" ||
		normalized[0].Chat.TitleSource != store.ChatTitleSourceGroupSubject {
		t.Fatalf("group info normalized to %+v", normalized)
	}
}

func TestNormalizeJoinedGroupIncludesParticipants(t *testing.T) {
	chat := types.NewJID("12345-678", types.GroupServer)
	participant := types.NewJID("111", types.DefaultUserServer)
	normalized := normalizeWhatsmeowEvent(&events.JoinedGroup{
		GroupInfo: types.GroupInfo{
			JID: chat,
			GroupName: types.GroupName{
				Name: "Project Group",
			},
			Participants: []types.GroupParticipant{{
				JID:         participant,
				DisplayName: "Anon",
				IsAdmin:     true,
			}},
		},
	})
	if len(normalized) != 2 || normalized[1].Kind != EventGroupParticipants {
		t.Fatalf("joined group normalized to %+v, want chat and participants", normalized)
	}
	participants := normalized[1].Participants
	if !participants.Replace ||
		participants.ChatID != chat.String() ||
		len(participants.Participants) != 1 ||
		participants.Participants[0].JID != participant.String() ||
		!participants.Participants[0].IsAdmin {
		t.Fatalf("participant event = %+v", participants)
	}
}

func TestNormalizeHistorySyncEventMarksMessagesHistorical(t *testing.T) {
	when := uint64(1_700_000_000)
	syncType := waHistorySync.HistorySync_ON_DEMAND
	transferComplete := true
	transferType := waHistorySync.Conversation_COMPLETE_ON_DEMAND_SYNC_BUT_MORE_MSG_REMAIN_ON_PRIMARY
	client := &Client{client: &whatsmeow.Client{}}

	normalized := client.normalizeWhatsmeowEvent(context.Background(), &events.HistorySync{
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
