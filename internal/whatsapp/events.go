package whatsapp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	waHistorySync "go.mau.fi/whatsmeow/proto/waHistorySync"
	waSyncAction "go.mau.fi/whatsmeow/proto/waSyncAction"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

	"vimwhat/internal/media"
	"vimwhat/internal/store"
)

func (c *Client) normalizeWhatsmeowEvent(ctx context.Context, evt any) []Event {
	switch event := evt.(type) {
	case *events.HistorySync:
		return c.normalizeHistorySyncEvent(ctx, event)
	case *events.Message:
		return c.normalizeMessageEvent(ctx, event)
	case *events.Receipt:
		return c.normalizeReceiptEvent(ctx, event)
	case *events.Picture:
		return c.normalizePictureEvent(ctx, event)
	case *events.ChatPresence:
		return c.normalizeChatPresenceEvent(ctx, event)
	case *events.Contact:
		return c.normalizeContactEvent(ctx, event)
	case *events.PushName:
		return c.normalizePushNameEvent(ctx, event)
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
	case *events.OfflineSyncPreview:
		return []Event{{
			Kind: EventOfflineSync,
			Offline: OfflineSyncEvent{
				Active:         true,
				Total:          event.Total,
				AppDataChanges: event.AppDataChanges,
				Messages:       event.Messages,
				Notifications:  event.Notifications,
				Receipts:       event.Receipts,
			},
		}}
	case *events.OfflineSyncCompleted:
		return []Event{{
			Kind: EventOfflineSync,
			Offline: OfflineSyncEvent{
				Completed: true,
				Total:     event.Count,
				Processed: event.Count,
			},
		}}
	case *events.AppState:
		return normalizeAppStateEvent(event)
	case *events.Message:
		return normalizeMessageEvent(event)
	case *events.Receipt:
		return normalizeReceiptEvent(event)
	case *events.Picture:
		return normalizePictureEvent(event)
	case *events.ChatPresence:
		return normalizeChatPresenceEvent(event)
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

func (c *Client) normalizeMessageEvent(ctx context.Context, event *events.Message) []Event {
	if c != nil && c.client != nil && event != nil && event.Message != nil && event.Message.GetEncReactionMessage() != nil {
		if reaction, err := c.client.DecryptReaction(ctx, event); err == nil && reaction != nil {
			clone := *event
			clone.Message = &waE2E.Message{ReactionMessage: reaction}
			return c.normalizeParsedMessageEvent(ctx, &clone)
		}
	}
	return c.normalizeParsedMessageEvent(ctx, event)
}

func (c *Client) normalizeHistorySyncEvent(ctx context.Context, event *events.HistorySync) []Event {
	if c == nil || c.client == nil || event == nil || event.Data == nil {
		return nil
	}
	history := event.Data
	out := normalizeHistoryRecentStickers(history)
	if history.GetSyncType() != waHistorySync.HistorySync_ON_DEMAND {
		return out
	}

	for _, conversation := range history.GetConversations() {
		chatJID, alternateChatJID, ok := historyConversationJIDs(conversation)
		if !ok || !supportedChat(chatJID) {
			continue
		}
		canonicalChatJID, aliases := c.canonicalChatIdentity(ctx, chatJID, alternateChatJID)
		if canonicalChatJID.IsEmpty() || !supportedChat(canonicalChatJID) {
			continue
		}
		chatID := canonicalChatJID.String()
		out = append(out, Event{
			Kind: EventChatUpsert,
			Chat: ChatEvent{
				ID:            chatID,
				JID:           chatID,
				AliasIDs:      aliases,
				Title:         historyConversationTitle(conversation, canonicalChatJID),
				TitleSource:   historyConversationTitleSource(conversation, canonicalChatJID),
				Kind:          chatKind(canonicalChatJID),
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
			for _, normalized := range c.normalizeParsedMessageEvent(ctx, messageEvent) {
				if normalized.Kind == EventChatUpsert {
					normalized.Chat.UnreadKnown = false
					normalized.Chat.Historical = true
					if strings.TrimSpace(conversation.GetName()) != "" {
						normalized.Chat.Title = strings.TrimSpace(conversation.GetName())
						normalized.Chat.TitleSource = historyConversationTitleSource(conversation, canonicalChatJID)
					}
				}
				if normalized.Kind == EventMessageUpsert {
					normalized.Message.Historical = true
					messages++
				}
				if normalized.Kind == EventMessageEdit {
					normalized.Edit.Historical = true
				}
				if normalized.Kind == EventMediaMetadata {
					normalized.Media.Historical = true
				}
				if normalized.Kind == EventMessageDelete {
					normalized.Delete.Historical = true
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

func normalizeHistoryRecentStickers(history *waHistorySync.HistorySync) []Event {
	if history == nil {
		return nil
	}
	stickers := history.GetRecentStickers()
	out := make([]Event, 0, len(stickers))
	for _, sticker := range stickers {
		event, ok := recentStickerFromHistory(sticker)
		if !ok {
			continue
		}
		event.Historical = true
		out = append(out, Event{
			Kind:    EventRecentSticker,
			Sticker: event,
		})
	}
	return out
}

func normalizeAppStateEvent(event *events.AppState) []Event {
	if event == nil || event.SyncActionValue == nil {
		return nil
	}
	if sticker := event.GetStickerAction(); sticker != nil {
		stickerEvent, ok := recentStickerFromAction(sticker, event.GetTimestamp())
		if !ok {
			return nil
		}
		return []Event{{
			Kind:    EventRecentSticker,
			Sticker: stickerEvent,
		}}
	}
	if remove := event.GetRemoveRecentStickerAction(); remove != nil {
		lastUsedAt := recentStickerTime(remove.GetLastStickerSentTS())
		if lastUsedAt.IsZero() {
			return nil
		}
		return []Event{{
			Kind: EventRecentStickerRemove,
			StickerRemove: RecentStickerRemoveEvent{
				LastUsedAt: lastUsedAt,
				UpdatedAt:  time.Now(),
			},
		}}
	}
	return nil
}

func recentStickerFromHistory(sticker *waHistorySync.StickerMetadata) (RecentStickerEvent, bool) {
	if sticker == nil {
		return RecentStickerEvent{}, false
	}
	lastUsedAt := recentStickerTime(sticker.GetLastStickerSentTS())
	updatedAt := lastUsedAt
	if updatedAt.IsZero() {
		updatedAt = time.Now()
	}
	event := RecentStickerEvent{
		URL:           strings.TrimSpace(sticker.GetURL()),
		DirectPath:    strings.TrimSpace(sticker.GetDirectPath()),
		MediaKey:      cloneBytes(sticker.GetMediaKey()),
		FileSHA256:    cloneBytes(sticker.GetFileSHA256()),
		FileEncSHA256: cloneBytes(sticker.GetFileEncSHA256()),
		FileLength:    int64(sticker.GetFileLength()),
		MIMEType:      strings.TrimSpace(sticker.GetMimetype()),
		Width:         int(sticker.GetWidth()),
		Height:        int(sticker.GetHeight()),
		Weight:        float64(sticker.GetWeight()),
		LastUsedAt:    lastUsedAt,
		IsLottie:      sticker.GetIsLottie(),
		IsAvatar:      sticker.GetIsAvatarSticker(),
		ImageHash:     strings.TrimSpace(sticker.GetImageHash()),
		UpdatedAt:     updatedAt,
	}
	event.FileName = stickerFileNameForRecent(event.MIMEType, event.IsLottie)
	event.ID = recentStickerID(event)
	if event.ID == "" || (event.DirectPath == "" && event.URL == "") || len(event.MediaKey) == 0 || len(event.FileEncSHA256) == 0 {
		return RecentStickerEvent{}, false
	}
	return event, true
}

func recentStickerFromAction(sticker *waSyncAction.StickerAction, timestampMS int64) (RecentStickerEvent, bool) {
	if sticker == nil {
		return RecentStickerEvent{}, false
	}
	lastUsedAt := recentStickerTime(timestampMS)
	updatedAt := lastUsedAt
	if updatedAt.IsZero() {
		updatedAt = time.Now()
	}
	event := RecentStickerEvent{
		URL:           strings.TrimSpace(sticker.GetURL()),
		DirectPath:    strings.TrimSpace(sticker.GetDirectPath()),
		MediaKey:      cloneBytes(sticker.GetMediaKey()),
		FileEncSHA256: cloneBytes(sticker.GetFileEncSHA256()),
		FileLength:    int64(sticker.GetFileLength()),
		MIMEType:      strings.TrimSpace(sticker.GetMimetype()),
		Width:         int(sticker.GetWidth()),
		Height:        int(sticker.GetHeight()),
		LastUsedAt:    lastUsedAt,
		IsFavorite:    sticker.GetIsFavorite(),
		IsLottie:      sticker.GetIsLottie(),
		IsAvatar:      sticker.GetIsAvatarSticker(),
		ImageHash:     strings.TrimSpace(sticker.GetImageHash()),
		UpdatedAt:     updatedAt,
	}
	event.FileName = stickerFileNameForRecent(event.MIMEType, event.IsLottie)
	event.ID = recentStickerID(event)
	if event.ID == "" || (event.DirectPath == "" && event.URL == "") || len(event.MediaKey) == 0 || len(event.FileEncSHA256) == 0 {
		return RecentStickerEvent{}, false
	}
	return event, true
}

func recentStickerID(sticker RecentStickerEvent) string {
	var seed strings.Builder
	for _, value := range []string{
		sticker.ImageHash,
		sticker.DirectPath,
		sticker.URL,
		hex.EncodeToString(sticker.FileSHA256),
		hex.EncodeToString(sticker.FileEncSHA256),
		hex.EncodeToString(sticker.MediaKey),
	} {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		seed.WriteString(value)
		seed.WriteByte('\n')
	}
	if seed.Len() == 0 {
		return ""
	}
	sum := sha256.Sum256([]byte(seed.String()))
	return "sticker-" + hex.EncodeToString(sum[:])[:24]
}

func recentStickerTime(value int64) time.Time {
	if value <= 0 {
		return time.Time{}
	}
	if value > 100_000_000_000 {
		return time.UnixMilli(value)
	}
	return time.Unix(value, 0)
}

func stickerFileNameForRecent(mimeType string, isLottie bool) string {
	if isLottie {
		return "sticker.tgs"
	}
	return stickerFileName(mimeType)
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

func historyConversationJIDs(conversation *waHistorySync.Conversation) (types.JID, types.JID, bool) {
	if conversation == nil {
		return types.EmptyJID, types.EmptyJID, false
	}
	var parsed []types.JID
	for _, candidate := range []string{conversation.GetID(), conversation.GetNewJID()} {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		jid, err := types.ParseJID(candidate)
		if err == nil {
			parsed = append(parsed, jid)
		}
	}
	if len(parsed) == 0 {
		return types.EmptyJID, types.EmptyJID, false
	}
	if len(parsed) == 1 {
		return parsed[0], types.EmptyJID, true
	}
	return parsed[0], parsed[1], true
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

func (c *Client) normalizeParsedMessageEvent(ctx context.Context, event *events.Message) []Event {
	if event == nil || event.Message == nil || event.Info.ID == "" || !supportedChat(event.Info.Chat) {
		return nil
	}

	canonicalChatJID, aliases := c.canonicalChatIdentity(ctx, event.Info.Chat, directChatAlternateJID(event.Info))
	if canonicalChatJID.IsEmpty() {
		canonicalChatJID = canonicalizableChatJID(event.Info.Chat)
	}
	chatID := canonicalChatJID.String()
	messageID := localMessageID(chatID, string(event.Info.ID))
	senderJID := canonicalDirectSenderJID(chatID, event.Info)
	reaction, hasReaction := reactionEventForSender(chatID, senderJID, event)
	body := messageBody(event.Message)
	media, hasMedia := mediaMetadata(messageID, event, event.Info.Timestamp)
	quotedRemoteID, quotedMessageID := quotedIDs(chatID, event.Message)
	notificationPreview := messageNotificationPreview(body, media, hasMedia)

	normalized := []Event{{
		Kind: EventChatUpsert,
		Chat: ChatEvent{
			ID:            chatID,
			JID:           chatID,
			AliasIDs:      aliases,
			Title:         chatTitleForJID(event.Info, canonicalChatJID),
			TitleSource:   chatTitleSourceForJID(event.Info, canonicalChatJID),
			Kind:          chatKind(canonicalChatJID),
			LastMessageAt: event.Info.Timestamp,
		},
	}}

	if deleted, ok := messageDeleteEvent(chatID, event); ok {
		normalized = append(normalized, Event{
			Kind:   EventMessageDelete,
			Delete: deleted,
		})
		return normalized
	}

	if edit, ok := messageEditEvent(chatID, event); ok {
		normalized = append(normalized, Event{
			Kind: EventMessageEdit,
			Edit: edit,
		})
		return normalized
	}

	if hasReaction {
		normalized = append(normalized, Event{
			Kind:     EventReactionUpdate,
			Reaction: reaction,
		})
		return normalized
	}

	if strings.TrimSpace(body) != "" || hasMedia {
		normalized = append(normalized, Event{
			Kind: EventMessageUpsert,
			Message: MessageEvent{
				ID:                  messageID,
				RemoteID:            string(event.Info.ID),
				ChatID:              chatID,
				ChatJID:             chatID,
				Sender:              senderName(event.Info),
				SenderJID:           senderJID,
				Body:                body,
				NotificationPreview: notificationPreview,
				Timestamp:           event.Info.Timestamp,
				IsOutgoing:          event.Info.IsFromMe,
				Status:              initialMessageStatus(event.Info.IsFromMe),
				QuotedMessageID:     quotedMessageID,
				QuotedRemoteID:      quotedRemoteID,
				ForwardPayload:      messageForwardPayload(event.Message),
			},
		})
	}

	if hasMedia {
		normalized = append(normalized, Event{
			Kind:  EventMediaMetadata,
			Media: media,
		})
	}

	return normalized
}

func (c *Client) normalizeReceiptEvent(ctx context.Context, event *events.Receipt) []Event {
	if event == nil || len(event.MessageIDs) == 0 || !supportedChat(event.Chat) {
		return nil
	}

	canonicalChatJID, _ := c.canonicalChatIdentity(ctx, event.Chat, types.EmptyJID)
	if canonicalChatJID.IsEmpty() {
		canonicalChatJID = canonicalizableChatJID(event.Chat)
	}
	chatID := canonicalChatJID.String()
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

func (c *Client) normalizeChatPresenceEvent(ctx context.Context, event *events.ChatPresence) []Event {
	if event == nil || !supportedChat(event.Chat) {
		return nil
	}
	canonicalChatJID, _ := c.canonicalChatIdentity(ctx, event.Chat, types.EmptyJID)
	if canonicalChatJID.IsEmpty() {
		canonicalChatJID = canonicalizableChatJID(event.Chat)
	}
	chatID := canonicalChatJID.String()
	sender := event.Sender.ToNonAD()
	senderJID := ""
	if event.Chat.Server != types.GroupServer && chatID != "" {
		senderJID = chatID
	} else if !sender.IsEmpty() {
		senderJID = sender.String()
	}
	display := senderJID
	switch {
	case sender.User != "":
		display = sender.User
	case event.Chat.Server != types.GroupServer && canonicalChatJID.User != "":
		display = canonicalChatJID.User
	}
	return []Event{{
		Kind: EventPresenceUpdate,
		Presence: PresenceEvent{
			ChatID:    chatID,
			SenderJID: senderJID,
			Sender:    display,
			Typing:    event.State == types.ChatPresenceComposing,
			UpdatedAt: time.Now(),
		},
	}}
}

func (c *Client) normalizePictureEvent(ctx context.Context, event *events.Picture) []Event {
	if event == nil || event.JID.IsEmpty() || !supportedChat(event.JID) {
		return nil
	}
	updatedAt := event.Timestamp
	if updatedAt.IsZero() {
		updatedAt = time.Now()
	}
	canonicalChatJID, _ := c.canonicalChatIdentity(ctx, event.JID, types.EmptyJID)
	if canonicalChatJID.IsEmpty() {
		canonicalChatJID = canonicalizableChatJID(event.JID)
	}
	chatID := canonicalChatJID.String()
	return []Event{{
		Kind: EventChatAvatarUpdate,
		Avatar: AvatarEvent{
			ChatID:    chatID,
			ChatJID:   chatID,
			AvatarID:  strings.TrimSpace(event.PictureID),
			Remove:    event.Remove,
			UpdatedAt: updatedAt,
		},
	}}
}

func (c *Client) normalizeContactEvent(ctx context.Context, event *events.Contact) []Event {
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
	canonicalChatJID, _ := c.canonicalChatIdentity(ctx, event.JID, contactPhoneJID(phone))
	if canonicalChatJID.IsEmpty() {
		canonicalChatJID = canonicalizableChatJID(event.JID)
	}
	return []Event{{
		Kind: EventContactUpsert,
		Contact: ContactEvent{
			JID:         event.JID.String(),
			ChatID:      canonicalChatJID.String(),
			DisplayName: displayName,
			Phone:       phone,
			UpdatedAt:   event.Timestamp,
			TitleSource: store.ChatTitleSourceContactDisplay,
		},
	}}
}

func (c *Client) normalizePushNameEvent(ctx context.Context, event *events.PushName) []Event {
	if event == nil || event.JID.IsEmpty() || strings.TrimSpace(event.NewPushName) == "" {
		return nil
	}
	canonicalChatJID, _ := c.canonicalChatIdentity(ctx, event.JID, types.EmptyJID)
	if canonicalChatJID.IsEmpty() {
		canonicalChatJID = canonicalizableChatJID(event.JID)
	}
	return []Event{{
		Kind: EventContactUpsert,
		Contact: ContactEvent{
			JID:         event.JID.String(),
			ChatID:      canonicalChatJID.String(),
			NotifyName:  strings.TrimSpace(event.NewPushName),
			UpdatedAt:   time.Now(),
			TitleSource: store.ChatTitleSourcePushName,
		},
	}}
}

func normalizeMessageEvent(event *events.Message) []Event {
	if event == nil || event.Message == nil || event.Info.ID == "" || !supportedChat(event.Info.Chat) {
		return nil
	}

	chatID := event.Info.Chat.String()
	messageID := localMessageID(chatID, string(event.Info.ID))
	reaction, hasReaction := reactionEvent(chatID, event)
	body := messageBody(event.Message)
	media, hasMedia := mediaMetadata(messageID, event, event.Info.Timestamp)
	quotedRemoteID, quotedMessageID := quotedIDs(chatID, event.Message)
	notificationPreview := messageNotificationPreview(body, media, hasMedia)

	normalized := []Event{{
		Kind: EventChatUpsert,
		Chat: ChatEvent{
			ID:            chatID,
			JID:           chatID,
			Title:         chatTitle(event.Info),
			TitleSource:   chatTitleSource(event.Info),
			Kind:          chatKind(event.Info.Chat),
			LastMessageAt: event.Info.Timestamp,
		},
	}}

	if deleted, ok := messageDeleteEvent(chatID, event); ok {
		normalized = append(normalized, Event{
			Kind:   EventMessageDelete,
			Delete: deleted,
		})
		return normalized
	}

	if edit, ok := messageEditEvent(chatID, event); ok {
		normalized = append(normalized, Event{
			Kind: EventMessageEdit,
			Edit: edit,
		})
		return normalized
	}

	if hasReaction {
		normalized = append(normalized, Event{
			Kind:     EventReactionUpdate,
			Reaction: reaction,
		})
		return normalized
	}

	if strings.TrimSpace(body) != "" || hasMedia {
		normalized = append(normalized, Event{
			Kind: EventMessageUpsert,
			Message: MessageEvent{
				ID:                  messageID,
				RemoteID:            string(event.Info.ID),
				ChatID:              chatID,
				ChatJID:             chatID,
				Sender:              senderName(event.Info),
				SenderJID:           senderJID(event.Info),
				Body:                body,
				NotificationPreview: notificationPreview,
				Timestamp:           event.Info.Timestamp,
				IsOutgoing:          event.Info.IsFromMe,
				Status:              initialMessageStatus(event.Info.IsFromMe),
				QuotedMessageID:     quotedMessageID,
				QuotedRemoteID:      quotedRemoteID,
				ForwardPayload:      messageForwardPayload(event.Message),
			},
		})
	}

	if hasMedia {
		normalized = append(normalized, Event{
			Kind:  EventMediaMetadata,
			Media: media,
		})
	}

	return normalized
}

func messageNotificationPreview(body string, item MediaEvent, hasMedia bool) string {
	if text := strings.TrimSpace(body); text != "" {
		return text
	}
	if !hasMedia {
		return ""
	}

	if strings.EqualFold(strings.TrimSpace(item.Kind), "sticker") {
		if label := strings.TrimSpace(item.AccessibilityLabel); label != "" {
			return "Sticker: " + label
		}
		return "Sticker"
	}

	switch media.MediaKind(item.MIMEType, item.FileName) {
	case media.KindImage:
		if name := strings.TrimSpace(item.FileName); name != "" {
			return "Image: " + name
		}
		return "Image"
	case media.KindVideo:
		if name := strings.TrimSpace(item.FileName); name != "" {
			return "Video: " + name
		}
		return "Video"
	case media.KindAudio:
		if name := strings.TrimSpace(item.FileName); name != "" {
			return "Audio: " + name
		}
		return "Audio"
	default:
		if name := strings.TrimSpace(item.FileName); name != "" {
			return "Attachment: " + name
		}
		if mimeType := strings.TrimSpace(item.MIMEType); mimeType != "" {
			return "Attachment: " + mimeType
		}
		return "Attachment"
	}
}

func reactionEvent(chatID string, event *events.Message) (ReactionEvent, bool) {
	return reactionEventForSender(chatID, senderJID(event.Info), event)
}

func messageDeleteEvent(chatID string, event *events.Message) (MessageDeleteEvent, bool) {
	if event == nil || event.Message == nil {
		return MessageDeleteEvent{}, false
	}
	protocol := event.Message.GetProtocolMessage()
	if protocol == nil || protocol.GetType() != waE2E.ProtocolMessage_REVOKE || protocol.GetKey() == nil {
		return MessageDeleteEvent{}, false
	}
	remoteID := strings.TrimSpace(protocol.GetKey().GetID())
	if remoteID == "" || strings.TrimSpace(chatID) == "" {
		return MessageDeleteEvent{}, false
	}
	return MessageDeleteEvent{
		MessageID:     localMessageID(chatID, remoteID),
		RemoteID:      remoteID,
		ChatID:        chatID,
		ChatJID:       chatID,
		DeletedReason: "everyone",
		Timestamp:     event.Info.Timestamp,
	}, true
}

func messageEditEvent(chatID string, event *events.Message) (MessageEditEvent, bool) {
	if event == nil || event.Message == nil || !event.IsEdit {
		return MessageEditEvent{}, false
	}
	protocol := event.Message.GetProtocolMessage()
	if protocol == nil || protocol.GetType() != waE2E.ProtocolMessage_MESSAGE_EDIT || protocol.GetKey() == nil {
		return MessageEditEvent{}, false
	}
	remoteID := strings.TrimSpace(protocol.GetKey().GetID())
	body := strings.TrimSpace(messageBody(protocol.GetEditedMessage()))
	if remoteID == "" || strings.TrimSpace(chatID) == "" || body == "" {
		return MessageEditEvent{}, false
	}
	editedAt := event.Info.Timestamp
	if millis := protocol.GetTimestampMS(); millis > 0 {
		editedAt = time.UnixMilli(millis)
	}
	if editedAt.IsZero() {
		editedAt = time.Now()
	}
	return MessageEditEvent{
		MessageID: localMessageID(chatID, remoteID),
		RemoteID:  remoteID,
		ChatID:    chatID,
		ChatJID:   chatID,
		Body:      body,
		EditedAt:  editedAt,
	}, true
}

func reactionEventForSender(chatID, senderJID string, event *events.Message) (ReactionEvent, bool) {
	if event == nil || event.Message == nil {
		return ReactionEvent{}, false
	}
	reaction := event.Message.GetReactionMessage()
	if reaction == nil || reaction.GetKey() == nil {
		return ReactionEvent{}, false
	}
	remoteID := strings.TrimSpace(reaction.GetKey().GetID())
	if remoteID == "" {
		return ReactionEvent{}, false
	}
	timestamp := event.Info.Timestamp
	if millis := reaction.GetSenderTimestampMS(); millis > 0 {
		timestamp = time.UnixMilli(millis)
	}
	return ReactionEvent{
		MessageID:  localMessageID(chatID, remoteID),
		ChatID:     chatID,
		SenderJID:  senderJID,
		Emoji:      reaction.GetText(),
		Timestamp:  timestamp,
		IsOutgoing: event.Info.IsFromMe,
	}, true
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

func normalizeChatPresenceEvent(event *events.ChatPresence) []Event {
	if event == nil || !supportedChat(event.Chat) {
		return nil
	}
	sender := event.Sender
	senderJID := ""
	if !sender.IsEmpty() {
		senderJID = sender.String()
	}
	display := senderJID
	if sender.User != "" {
		display = sender.User
	}
	return []Event{{
		Kind: EventPresenceUpdate,
		Presence: PresenceEvent{
			ChatID:    event.Chat.String(),
			SenderJID: senderJID,
			Sender:    display,
			Typing:    event.State == types.ChatPresenceComposing,
			UpdatedAt: time.Now(),
		},
	}}
}

func normalizePictureEvent(event *events.Picture) []Event {
	if event == nil || event.JID.IsEmpty() || !supportedChat(event.JID) {
		return nil
	}
	updatedAt := event.Timestamp
	if updatedAt.IsZero() {
		updatedAt = time.Now()
	}
	chatID := event.JID.String()
	return []Event{{
		Kind: EventChatAvatarUpdate,
		Avatar: AvatarEvent{
			ChatID:    chatID,
			ChatJID:   chatID,
			AvatarID:  strings.TrimSpace(event.PictureID),
			Remove:    event.Remove,
			UpdatedAt: updatedAt,
		},
	}}
}

func supportedChat(jid types.JID) bool {
	switch jid.Server {
	case types.DefaultUserServer, types.HiddenUserServer, types.GroupServer:
		return true
	default:
		return false
	}
}

func NormalizeSendChatJID(chatJID string) (string, error) {
	chatJID = strings.TrimSpace(chatJID)
	if chatJID == "" {
		return "", fmt.Errorf("chat jid is required")
	}
	jid, err := types.ParseJID(chatJID)
	if err != nil {
		return "", fmt.Errorf("parse send chat jid: %w", err)
	}
	if jid.Device > 0 {
		return "", fmt.Errorf("send chat jid must not include a device: %s", chatJID)
	}
	if !supportedChat(jid) {
		return "", fmt.Errorf("unsupported send chat jid %s", chatJID)
	}
	return jid.String(), nil
}

func chatKind(jid types.JID) string {
	if jid.Server == types.GroupServer {
		return "group"
	}
	return "direct"
}

func chatTitle(info types.MessageInfo) string {
	return chatTitleForJID(info, info.Chat)
}

func chatTitleSource(info types.MessageInfo) string {
	return chatTitleSourceForJID(info, info.Chat)
}

func chatTitleForJID(info types.MessageInfo, chatJID types.JID) string {
	if chatJID.Server == types.GroupServer {
		return "Unnamed group"
	}
	if !info.IsFromMe && strings.TrimSpace(info.PushName) != "" {
		return strings.TrimSpace(info.PushName)
	}
	for _, candidate := range []types.JID{info.Chat, directChatAlternateJID(info), chatJID} {
		if candidate.Server == types.DefaultUserServer && candidate.User != "" {
			return candidate.User
		}
	}
	if chatJID.User != "" {
		return chatJID.User
	}
	if !chatJID.IsEmpty() {
		return chatJID.String()
	}
	if info.Chat.User != "" {
		return info.Chat.User
	}
	return info.Chat.String()
}

func chatTitleSourceForJID(info types.MessageInfo, chatJID types.JID) string {
	if chatJID.Server == types.GroupServer {
		return store.ChatTitleSourcePlaceholder
	}
	if !info.IsFromMe && strings.TrimSpace(info.PushName) != "" {
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
			ChatID:      event.JID.String(),
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
			ChatID:      event.JID.String(),
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

func contactPhoneJID(raw string) types.JID {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return types.EmptyJID
	}
	if strings.Contains(raw, "@") {
		jid, err := types.ParseJID(raw)
		if err == nil {
			return jid
		}
	}
	return types.NewJID(raw, types.DefaultUserServer)
}

func localMessageID(chatID, remoteID string) string {
	if strings.TrimSpace(remoteID) == "" {
		return ""
	}
	return chatID + "/" + remoteID
}

func LocalMessageID(chatID, remoteID string) string {
	return localMessageID(chatID, remoteID)
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

func mediaMetadata(messageID string, event *events.Message, timestamp time.Time) (MediaEvent, bool) {
	if event == nil || event.Message == nil {
		return MediaEvent{}, false
	}
	updatedAt := timestamp
	if updatedAt.IsZero() {
		updatedAt = time.Now()
	}
	message := event.Message

	if image := message.GetImageMessage(); image != nil {
		return MediaEvent{
			MessageID:     messageID,
			Kind:          "image",
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
			Kind:          "video",
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
			Kind:          "audio",
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
			Kind:          "document",
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
	if sticker := message.GetStickerMessage(); sticker != nil {
		fileName := "sticker.webp"
		switch {
		case sticker.GetIsLottie() || event.IsLottieSticker:
			fileName = "sticker.tgs"
		case sticker.GetMimetype() != "":
			fileName = stickerFileName(sticker.GetMimetype())
		}
		return MediaEvent{
			MessageID:          messageID,
			Kind:               "sticker",
			MIMEType:           sticker.GetMimetype(),
			FileName:           fileName,
			SizeBytes:          int64(sticker.GetFileLength()),
			ThumbnailData:      cloneBytes(sticker.GetPngThumbnail()),
			DownloadState:      "remote",
			IsAnimated:         sticker.GetIsAnimated(),
			IsLottie:           sticker.GetIsLottie() || event.IsLottieSticker,
			AccessibilityLabel: strings.TrimSpace(sticker.GetAccessibilityLabel()),
			UpdatedAt:          updatedAt,
			Download: mediaDownloadDescriptor(
				messageID,
				"sticker",
				sticker.GetURL(),
				sticker.GetDirectPath(),
				sticker.GetMediaKey(),
				sticker.GetFileSHA256(),
				sticker.GetFileEncSHA256(),
				sticker.GetFileLength(),
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

func messageForwardPayload(message *waE2E.Message) []byte {
	if message == nil {
		return nil
	}
	payload, err := proto.Marshal(message)
	if err != nil {
		return nil
	}
	return payload
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
	if contextInfo := message.GetStickerMessage().GetContextInfo(); contextInfo != nil {
		return contextInfo
	}
	return nil
}

func stickerFileName(mimeType string) string {
	mimeType = strings.ToLower(strings.TrimSpace(mimeType))
	switch mimeType {
	case "image/webp":
		return "sticker.webp"
	case "image/png":
		return "sticker.png"
	case "application/x-tgsticker", "application/x-tgs", "application/gzip":
		return "sticker.tgs"
	default:
		return "sticker"
	}
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
