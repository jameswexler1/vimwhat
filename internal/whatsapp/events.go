package whatsapp

import (
	"fmt"
	"strings"
	"time"

	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	waHistorySync "go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"

	"vimwhat/internal/store"
)

func (c *Client) normalizeWhatsmeowEvent(evt any) []Event {
	switch event := evt.(type) {
	case *events.HistorySync:
		return c.normalizeHistorySyncEvent(event)
	default:
		return normalizeWhatsmeowEvent(evt)
	}
}

func normalizeWhatsmeowEvent(evt any) []Event {
	switch event := evt.(type) {
	case *events.Connected:
		return []Event{connectionEvent(ConnectionOnline, "")}
	case *events.Disconnected:
		return []Event{connectionEvent(ConnectionReconnecting, "")}
	case *events.KeepAliveTimeout:
		return []Event{connectionEvent(ConnectionReconnecting, fmt.Sprintf("keepalive timeout after %d error(s)", event.ErrorCount))}
	case *events.KeepAliveRestored:
		return []Event{connectionEvent(ConnectionOnline, "")}
	case *events.ConnectFailure:
		state := ConnectionOffline
		if event.Reason.IsLoggedOut() {
			state = ConnectionLoggedOut
		}
		return []Event{connectionEvent(state, event.Reason.String())}
	case *events.LoggedOut:
		return []Event{connectionEvent(ConnectionLoggedOut, event.Reason.String())}
	case *events.StreamReplaced:
		return []Event{connectionEvent(ConnectionOffline, "stream replaced by another client")}
	case *events.ClientOutdated:
		return []Event{connectionEvent(ConnectionOffline, "client outdated")}
	case *events.TemporaryBan:
		return []Event{connectionEvent(ConnectionOffline, event.String())}
	case *events.ManualLoginReconnect:
		return []Event{connectionEvent(ConnectionReconnecting, "manual reconnect requested")}
	case *events.Message:
		return normalizeMessageEvent(event)
	case *events.Receipt:
		return normalizeReceiptEvent(event)
	case *events.GroupInfo:
		return normalizeGroupInfoEvent(event.JID, "", event.Name)
	case *events.JoinedGroup:
		return normalizeGroupInfoEvent(event.JID, event.GroupName.Name, &event.GroupName)
	case *events.Contact:
		return normalizeContactEvent(event)
	case *events.PushName:
		return normalizePushNameEvent(event)
	default:
		return nil
	}
}

func (c *Client) normalizeHistorySyncEvent(event *events.HistorySync) []Event {
	if c == nil || c.client == nil || event == nil || event.Data == nil {
		return nil
	}
	history := event.Data
	if history.GetSyncType() != waHistorySync.HistorySync_ON_DEMAND {
		return nil
	}

	var out []Event
	for _, conversation := range history.GetConversations() {
		chatJID, ok := historyConversationJID(conversation)
		if !ok || !supportedChat(chatJID) {
			continue
		}
		chatID := chatJID.String()
		out = append(out, Event{
			Kind: EventChatUpsert,
			Chat: ChatEvent{
				ID:            chatID,
				JID:           chatID,
				Title:         historyConversationTitle(conversation, chatJID),
				TitleSource:   historyConversationTitleSource(conversation, chatJID),
				Kind:          chatKind(chatJID),
				UnreadKnown:   false,
				Pinned:        conversation.GetPinned() > 0,
				Muted:         conversation.GetMuteEndTime() > uint64(time.Now().Unix()),
				LastMessageAt: historyConversationTimestamp(conversation),
				Historical:    true,
			},
		})

		messages := 0
		for _, historyMessage := range conversation.GetMessages() {
			webMessage := historyMessage.GetMessage()
			if webMessage == nil {
				continue
			}
			messageEvent, err := c.client.ParseWebMessage(chatJID, webMessage)
			if err != nil {
				continue
			}
			for _, normalized := range normalizeMessageEvent(messageEvent) {
				if normalized.Kind == EventChatUpsert {
					normalized.Chat.UnreadKnown = false
					normalized.Chat.Historical = true
					if strings.TrimSpace(conversation.GetName()) != "" {
						normalized.Chat.Title = strings.TrimSpace(conversation.GetName())
						normalized.Chat.TitleSource = historyConversationTitleSource(conversation, chatJID)
					}
				}
				if normalized.Kind == EventMessageUpsert {
					normalized.Message.Historical = true
					messages++
				}
				if normalized.Kind == EventMediaMetadata {
					normalized.Media.Historical = true
				}
				out = append(out, normalized)
			}
		}

		terminalReason := historyTerminalReason(conversation)
		out = append(out, Event{
			Kind: EventHistoryStatus,
			History: HistoryEvent{
				ChatID:         chatID,
				SyncType:       history.GetSyncType().String(),
				Messages:       messages,
				Exhausted:      terminalReason != "",
				TerminalReason: terminalReason,
			},
		})
	}
	return out
}

func historyTerminalReason(conversation *waHistorySync.Conversation) string {
	if conversation == nil {
		return ""
	}
	switch conversation.GetEndOfHistoryTransferType() {
	case waHistorySync.Conversation_COMPLETE_AND_NO_MORE_MESSAGE_REMAIN_ON_PRIMARY:
		return "no_more"
	case waHistorySync.Conversation_COMPLETE_ON_DEMAND_SYNC_WITH_MORE_MSG_ON_PRIMARY_BUT_NO_ACCESS:
		return "no_access"
	default:
		return ""
	}
}

func historyConversationJID(conversation *waHistorySync.Conversation) (types.JID, bool) {
	if conversation == nil {
		return types.EmptyJID, false
	}
	for _, candidate := range []string{conversation.GetID(), conversation.GetNewJID()} {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		jid, err := types.ParseJID(candidate)
		if err == nil {
			return jid, true
		}
	}
	return types.EmptyJID, false
}

func historyConversationTitle(conversation *waHistorySync.Conversation, jid types.JID) string {
	if conversation != nil {
		if name := strings.TrimSpace(conversation.GetName()); name != "" {
			return name
		}
	}
	if jid.Server == types.GroupServer {
		return "Unnamed group"
	}
	if jid.User != "" {
		return jid.User
	}
	return jid.String()
}

func historyConversationTitleSource(conversation *waHistorySync.Conversation, jid types.JID) string {
	if conversation != nil && strings.TrimSpace(conversation.GetName()) != "" {
		if jid.Server == types.GroupServer {
			return store.ChatTitleSourceGroupSubject
		}
		return store.ChatTitleSourceHistoryName
	}
	if jid.Server == types.GroupServer {
		return store.ChatTitleSourcePlaceholder
	}
	return store.ChatTitleSourceJID
}

func historyConversationTimestamp(conversation *waHistorySync.Conversation) time.Time {
	if conversation == nil {
		return time.Time{}
	}
	if timestamp := conversation.GetLastMsgTimestamp(); timestamp > 0 {
		return time.Unix(int64(timestamp), 0)
	}
	if timestamp := conversation.GetConversationTimestamp(); timestamp > 0 {
		return time.Unix(int64(timestamp), 0)
	}
	return time.Time{}
}

func connectionEvent(state ConnectionState, detail string) Event {
	return Event{
		Kind: EventConnectionState,
		Connection: ConnectionEvent{
			State:  state,
			Detail: detail,
		},
	}
}

func normalizeMessageEvent(event *events.Message) []Event {
	if event == nil || event.Message == nil || event.Info.ID == "" || !supportedChat(event.Info.Chat) {
		return nil
	}

	chatID := event.Info.Chat.String()
	messageID := localMessageID(chatID, string(event.Info.ID))
	body := messageBody(event.Message)
	media, hasMedia := mediaMetadata(messageID, event.Message, event.Info.Timestamp)
	quotedRemoteID, quotedMessageID := quotedIDs(chatID, event.Message)

	normalized := []Event{
		{
			Kind: EventChatUpsert,
			Chat: ChatEvent{
				ID:            chatID,
				JID:           chatID,
				Title:         chatTitle(event.Info),
				TitleSource:   chatTitleSource(event.Info),
				Kind:          chatKind(event.Info.Chat),
				LastMessageAt: event.Info.Timestamp,
			},
		},
		{
			Kind: EventMessageUpsert,
			Message: MessageEvent{
				ID:              messageID,
				RemoteID:        string(event.Info.ID),
				ChatID:          chatID,
				ChatJID:         chatID,
				Sender:          senderName(event.Info),
				SenderJID:       senderJID(event.Info),
				Body:            body,
				Timestamp:       event.Info.Timestamp,
				IsOutgoing:      event.Info.IsFromMe,
				Status:          initialMessageStatus(event.Info.IsFromMe),
				QuotedMessageID: quotedMessageID,
				QuotedRemoteID:  quotedRemoteID,
			},
		},
	}

	if hasMedia {
		normalized = append(normalized, Event{
			Kind:  EventMediaMetadata,
			Media: media,
		})
	}

	return normalized
}

func normalizeReceiptEvent(event *events.Receipt) []Event {
	if event == nil || len(event.MessageIDs) == 0 || !supportedChat(event.Chat) {
		return nil
	}

	chatID := event.Chat.String()
	status := receiptStatus(event.Type)
	if status == "" {
		return nil
	}

	out := make([]Event, 0, len(event.MessageIDs))
	for _, remoteID := range event.MessageIDs {
		if remoteID == "" {
			continue
		}
		out = append(out, Event{
			Kind: EventReceiptUpdate,
			Receipt: ReceiptEvent{
				MessageID: localMessageID(chatID, string(remoteID)),
				ChatID:    chatID,
				Status:    status,
			},
		})
	}
	return out
}

func supportedChat(jid types.JID) bool {
	switch jid.Server {
	case types.DefaultUserServer, types.HiddenUserServer, types.GroupServer:
		return true
	default:
		return false
	}
}

func chatKind(jid types.JID) string {
	if jid.Server == types.GroupServer {
		return "group"
	}
	return "direct"
}

func chatTitle(info types.MessageInfo) string {
	if info.Chat.Server == types.GroupServer {
		return "Unnamed group"
	}
	if strings.TrimSpace(info.PushName) != "" {
		return strings.TrimSpace(info.PushName)
	}
	if info.Chat.User != "" {
		return info.Chat.User
	}
	return info.Chat.String()
}

func chatTitleSource(info types.MessageInfo) string {
	if info.Chat.Server == types.GroupServer {
		return store.ChatTitleSourcePlaceholder
	}
	if strings.TrimSpace(info.PushName) != "" {
		return store.ChatTitleSourcePushName
	}
	return store.ChatTitleSourceJID
}

func normalizeGroupInfoEvent(jid types.JID, cachedName string, changedName *types.GroupName) []Event {
	if !supportedChat(jid) || jid.Server != types.GroupServer {
		return nil
	}
	name := strings.TrimSpace(cachedName)
	if changedName != nil && strings.TrimSpace(changedName.Name) != "" {
		name = strings.TrimSpace(changedName.Name)
	}
	title := name
	source := store.ChatTitleSourceGroupSubject
	if title == "" {
		title = "Unnamed group"
		source = store.ChatTitleSourcePlaceholder
	}
	chatID := jid.String()
	return []Event{{
		Kind: EventChatUpsert,
		Chat: ChatEvent{
			ID:          chatID,
			JID:         chatID,
			Title:       title,
			TitleSource: source,
			Kind:        "group",
		},
	}}
}

func normalizeContactEvent(event *events.Contact) []Event {
	if event == nil || event.Action == nil || event.JID.IsEmpty() {
		return nil
	}
	displayName := strings.TrimSpace(event.Action.GetFullName())
	if displayName == "" {
		displayName = strings.TrimSpace(event.Action.GetFirstName())
	}
	phone := strings.TrimSpace(event.Action.GetPnJID())
	if phone == "" && event.JID.Server == types.DefaultUserServer {
		phone = event.JID.User
	}
	return []Event{{
		Kind: EventContactUpsert,
		Contact: ContactEvent{
			JID:         event.JID.String(),
			DisplayName: displayName,
			Phone:       phone,
			UpdatedAt:   event.Timestamp,
			TitleSource: store.ChatTitleSourceContactDisplay,
		},
	}}
}

func normalizePushNameEvent(event *events.PushName) []Event {
	if event == nil || event.JID.IsEmpty() || strings.TrimSpace(event.NewPushName) == "" {
		return nil
	}
	return []Event{{
		Kind: EventContactUpsert,
		Contact: ContactEvent{
			JID:         event.JID.String(),
			NotifyName:  strings.TrimSpace(event.NewPushName),
			UpdatedAt:   time.Now(),
			TitleSource: store.ChatTitleSourcePushName,
		},
	}}
}

func senderName(info types.MessageInfo) string {
	if info.IsFromMe {
		return "me"
	}
	if strings.TrimSpace(info.PushName) != "" {
		return strings.TrimSpace(info.PushName)
	}
	if info.Sender.User != "" {
		return info.Sender.User
	}
	if !info.Sender.IsEmpty() {
		return info.Sender.String()
	}
	return "unknown"
}

func senderJID(info types.MessageInfo) string {
	if info.IsFromMe {
		return "me"
	}
	if !info.Sender.IsEmpty() {
		return info.Sender.String()
	}
	return ""
}

func localMessageID(chatID, remoteID string) string {
	if strings.TrimSpace(remoteID) == "" {
		return ""
	}
	return chatID + "/" + remoteID
}

func initialMessageStatus(outgoing bool) string {
	if outgoing {
		return "sent"
	}
	return "received"
}

func messageBody(message *waE2E.Message) string {
	if message == nil {
		return ""
	}
	if body := message.GetConversation(); body != "" {
		return body
	}
	if body := message.GetExtendedTextMessage().GetText(); body != "" {
		return body
	}
	if body := message.GetImageMessage().GetCaption(); body != "" {
		return body
	}
	if body := message.GetVideoMessage().GetCaption(); body != "" {
		return body
	}
	if body := message.GetDocumentMessage().GetCaption(); body != "" {
		return body
	}
	return ""
}

func mediaMetadata(messageID string, message *waE2E.Message, timestamp time.Time) (MediaEvent, bool) {
	if message == nil {
		return MediaEvent{}, false
	}
	updatedAt := timestamp
	if updatedAt.IsZero() {
		updatedAt = time.Now()
	}

	if image := message.GetImageMessage(); image != nil {
		return MediaEvent{
			MessageID:     messageID,
			MIMEType:      image.GetMimetype(),
			SizeBytes:     int64(image.GetFileLength()),
			DownloadState: "remote",
			UpdatedAt:     updatedAt,
			Download: mediaDownloadDescriptor(
				messageID,
				"image",
				image.GetURL(),
				image.GetDirectPath(),
				image.GetMediaKey(),
				image.GetFileSHA256(),
				image.GetFileEncSHA256(),
				image.GetFileLength(),
				updatedAt,
			),
		}, true
	}
	if video := message.GetVideoMessage(); video != nil {
		return MediaEvent{
			MessageID:     messageID,
			MIMEType:      video.GetMimetype(),
			SizeBytes:     int64(video.GetFileLength()),
			DownloadState: "remote",
			UpdatedAt:     updatedAt,
			Download: mediaDownloadDescriptor(
				messageID,
				"video",
				video.GetURL(),
				video.GetDirectPath(),
				video.GetMediaKey(),
				video.GetFileSHA256(),
				video.GetFileEncSHA256(),
				video.GetFileLength(),
				updatedAt,
			),
		}, true
	}
	if audio := message.GetAudioMessage(); audio != nil {
		return MediaEvent{
			MessageID:     messageID,
			MIMEType:      audio.GetMimetype(),
			SizeBytes:     int64(audio.GetFileLength()),
			DownloadState: "remote",
			UpdatedAt:     updatedAt,
			Download: mediaDownloadDescriptor(
				messageID,
				"audio",
				audio.GetURL(),
				audio.GetDirectPath(),
				audio.GetMediaKey(),
				audio.GetFileSHA256(),
				audio.GetFileEncSHA256(),
				audio.GetFileLength(),
				updatedAt,
			),
		}, true
	}
	if document := message.GetDocumentMessage(); document != nil {
		fileName := document.GetFileName()
		if fileName == "" {
			fileName = document.GetTitle()
		}
		return MediaEvent{
			MessageID:     messageID,
			MIMEType:      document.GetMimetype(),
			FileName:      fileName,
			SizeBytes:     int64(document.GetFileLength()),
			DownloadState: "remote",
			UpdatedAt:     updatedAt,
			Download: mediaDownloadDescriptor(
				messageID,
				"document",
				document.GetURL(),
				document.GetDirectPath(),
				document.GetMediaKey(),
				document.GetFileSHA256(),
				document.GetFileEncSHA256(),
				document.GetFileLength(),
				updatedAt,
			),
		}, true
	}
	return MediaEvent{}, false
}

func mediaDownloadDescriptor(
	messageID, kind, url, directPath string,
	mediaKey, fileSHA256, fileEncSHA256 []byte,
	fileLength uint64,
	updatedAt time.Time,
) MediaDownloadDescriptor {
	return MediaDownloadDescriptor{
		MessageID:     messageID,
		Kind:          kind,
		URL:           url,
		DirectPath:    directPath,
		MediaKey:      cloneBytes(mediaKey),
		FileSHA256:    cloneBytes(fileSHA256),
		FileEncSHA256: cloneBytes(fileEncSHA256),
		FileLength:    int64(fileLength),
		UpdatedAt:     updatedAt,
	}
}

func cloneBytes(input []byte) []byte {
	if len(input) == 0 {
		return nil
	}
	out := make([]byte, len(input))
	copy(out, input)
	return out
}

func quotedIDs(chatID string, message *waE2E.Message) (string, string) {
	contextInfo := messageContextInfo(message)
	if contextInfo == nil {
		return "", ""
	}
	remoteID := contextInfo.GetStanzaID()
	if remoteID == "" {
		return "", ""
	}
	return remoteID, localMessageID(chatID, remoteID)
}

func messageContextInfo(message *waE2E.Message) *waE2E.ContextInfo {
	if message == nil {
		return nil
	}
	if contextInfo := message.GetExtendedTextMessage().GetContextInfo(); contextInfo != nil {
		return contextInfo
	}
	if contextInfo := message.GetImageMessage().GetContextInfo(); contextInfo != nil {
		return contextInfo
	}
	if contextInfo := message.GetVideoMessage().GetContextInfo(); contextInfo != nil {
		return contextInfo
	}
	if contextInfo := message.GetAudioMessage().GetContextInfo(); contextInfo != nil {
		return contextInfo
	}
	if contextInfo := message.GetDocumentMessage().GetContextInfo(); contextInfo != nil {
		return contextInfo
	}
	return nil
}

func receiptStatus(receiptType types.ReceiptType) string {
	switch receiptType {
	case types.ReceiptTypeDelivered, types.ReceiptTypeSender:
		return "delivered"
	case types.ReceiptTypeRead, types.ReceiptTypeReadSelf:
		return "read"
	case types.ReceiptTypePlayed, types.ReceiptTypePlayedSelf:
		return "played"
	case types.ReceiptTypeRetry, types.ReceiptTypeServerError:
		return "error"
	default:
		if receiptType == "" {
			return "delivered"
		}
		return string(receiptType)
	}
}
