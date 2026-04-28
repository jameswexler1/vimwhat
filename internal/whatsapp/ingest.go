package whatsapp

import (
	"context"
	"fmt"
	"strings"
	"time"

	"vimwhat/internal/store"
)

type Ingestor struct {
	Store *store.Store
}

func (i Ingestor) Apply(ctx context.Context, event Event) (ApplyResult, error) {
	if i.Store == nil {
		return ApplyResult{}, fmt.Errorf("store is required")
	}

	switch event.Kind {
	case EventChatUpsert:
		chat := store.Chat{
			ID:            event.Chat.ID,
			JID:           event.Chat.JID,
			Title:         event.Chat.Title,
			TitleSource:   event.Chat.TitleSource,
			Kind:          event.Chat.Kind,
			Unread:        event.Chat.Unread,
			Pinned:        event.Chat.Pinned,
			Muted:         event.Chat.Muted,
			LastMessageAt: event.Chat.LastMessageAt,
		}
		if event.Chat.UnreadKnown {
			return ApplyResult{}, i.Store.UpsertChat(ctx, chat)
		}
		return ApplyResult{}, i.Store.UpsertChatPreserveUnread(ctx, chat)
	case EventMessageUpsert:
		message := store.Message{
			ID:              event.Message.ID,
			RemoteID:        event.Message.RemoteID,
			ChatID:          event.Message.ChatID,
			ChatJID:         event.Message.ChatJID,
			Sender:          event.Message.Sender,
			SenderJID:       event.Message.SenderJID,
			Body:            event.Message.Body,
			Timestamp:       event.Message.Timestamp,
			IsOutgoing:      event.Message.IsOutgoing,
			Status:          event.Message.Status,
			QuotedMessageID: event.Message.QuotedMessageID,
			QuotedRemoteID:  event.Message.QuotedRemoteID,
		}
		var (
			inserted bool
			err      error
		)
		if event.Message.Historical {
			inserted, err = i.Store.AddHistoricalMessage(ctx, message)
		} else {
			inserted, err = i.Store.AddIncomingMessage(ctx, message)
		}
		return ApplyResult{
			MessageInserted: inserted,
			Message:         event.Message,
		}, err
	case EventMessageEdit:
		messageID := strings.TrimSpace(event.Edit.MessageID)
		if messageID == "" && strings.TrimSpace(event.Edit.ChatID) != "" && strings.TrimSpace(event.Edit.RemoteID) != "" {
			messageID = LocalMessageID(event.Edit.ChatID, event.Edit.RemoteID)
		}
		if messageID == "" {
			return ApplyResult{}, fmt.Errorf("edit message id is required")
		}
		_, err := i.Store.UpdateMessageBody(ctx, messageID, event.Edit.Body, event.Edit.EditedAt)
		return ApplyResult{}, err
	case EventMessageDelete:
		messageID := strings.TrimSpace(event.Delete.MessageID)
		if messageID == "" && strings.TrimSpace(event.Delete.ChatID) != "" && strings.TrimSpace(event.Delete.RemoteID) != "" {
			messageID = LocalMessageID(event.Delete.ChatID, event.Delete.RemoteID)
		}
		if messageID == "" {
			return ApplyResult{}, fmt.Errorf("delete message id is required")
		}
		_, err := i.Store.DeleteMessageForEveryone(ctx, messageID)
		return ApplyResult{}, err
	case EventMediaMetadata:
		metadata := store.MediaMetadata{
			MessageID:          event.Media.MessageID,
			Kind:               event.Media.Kind,
			MIMEType:           event.Media.MIMEType,
			FileName:           event.Media.FileName,
			SizeBytes:          event.Media.SizeBytes,
			LocalPath:          event.Media.LocalPath,
			ThumbnailPath:      event.Media.ThumbnailPath,
			DownloadState:      event.Media.DownloadState,
			IsAnimated:         event.Media.IsAnimated,
			IsLottie:           event.Media.IsLottie,
			AccessibilityLabel: event.Media.AccessibilityLabel,
			UpdatedAt:          event.Media.UpdatedAt,
		}
		descriptor := storeMediaDownloadDescriptor(event.Media.Download, event.Media.MessageID)
		if descriptor.MessageID != "" {
			return ApplyResult{}, i.Store.UpsertMediaMetadataWithDownload(ctx, metadata, &descriptor)
		}
		return ApplyResult{}, i.Store.UpsertMediaMetadata(ctx, metadata)
	case EventReceiptUpdate:
		if event.Receipt.MessageID == "" {
			return ApplyResult{}, fmt.Errorf("receipt message id is required")
		}
		_, err := i.Store.UpdateMessageStatusIfExists(ctx, event.Receipt.MessageID, event.Receipt.Status)
		return ApplyResult{}, err
	case EventReactionUpdate:
		return ApplyResult{}, i.Store.UpsertReaction(ctx, store.Reaction{
			MessageID:  event.Reaction.MessageID,
			SenderJID:  event.Reaction.SenderJID,
			Emoji:      event.Reaction.Emoji,
			Timestamp:  event.Reaction.Timestamp,
			IsOutgoing: event.Reaction.IsOutgoing,
			UpdatedAt:  timeOrNow(event.Reaction.Timestamp),
		})
	case EventPresenceUpdate:
		return ApplyResult{}, nil
	case EventHistoryStatus:
		if event.History.ChatID != "" {
			value := "more"
			if event.History.TerminalReason != "" {
				value = event.History.TerminalReason
			}
			return ApplyResult{}, i.Store.SetSyncCursor(ctx, HistoryExhaustedCursor(event.History.ChatID), value)
		}
		return ApplyResult{}, nil
	case EventContactUpsert:
		contact := store.Contact{
			JID:         event.Contact.JID,
			DisplayName: event.Contact.DisplayName,
			NotifyName:  event.Contact.NotifyName,
			Phone:       event.Contact.Phone,
			UpdatedAt:   event.Contact.UpdatedAt,
		}
		if err := i.Store.UpsertContact(ctx, contact); err != nil {
			return ApplyResult{}, err
		}
		title := strings.TrimSpace(event.Contact.DisplayName)
		source := store.ChatTitleSourceContactDisplay
		if title == "" {
			title = strings.TrimSpace(event.Contact.NotifyName)
			source = event.Contact.TitleSource
			if source == "" {
				source = store.ChatTitleSourcePushName
			}
		}
		if title == "" {
			return ApplyResult{}, nil
		}
		chatID := strings.TrimSpace(event.Contact.ChatID)
		if chatID == "" {
			chatID = event.Contact.JID
		}
		_, err := i.Store.UpdateChatTitleIfExists(ctx, store.Chat{
			ID:          chatID,
			JID:         chatID,
			Title:       title,
			TitleSource: source,
			Kind:        "direct",
		})
		return ApplyResult{}, err
	case EventConnectionState:
		return ApplyResult{}, nil
	case EventOfflineSync:
		return ApplyResult{}, nil
	default:
		return ApplyResult{}, fmt.Errorf("unsupported whatsapp event kind %q", event.Kind)
	}
}

func timeOrNow(value time.Time) time.Time {
	if value.IsZero() {
		return time.Now()
	}
	return value
}

func storeMediaDownloadDescriptor(input MediaDownloadDescriptor, messageID string) store.MediaDownloadDescriptor {
	if strings.TrimSpace(input.MessageID) == "" {
		input.MessageID = messageID
	}
	if strings.TrimSpace(input.MessageID) == "" ||
		strings.TrimSpace(input.Kind) == "" ||
		(strings.TrimSpace(input.URL) == "" && strings.TrimSpace(input.DirectPath) == "") {
		return store.MediaDownloadDescriptor{}
	}
	return store.MediaDownloadDescriptor{
		MessageID:     input.MessageID,
		Kind:          input.Kind,
		URL:           input.URL,
		DirectPath:    input.DirectPath,
		MediaKey:      cloneBytes(input.MediaKey),
		FileSHA256:    cloneBytes(input.FileSHA256),
		FileEncSHA256: cloneBytes(input.FileEncSHA256),
		FileLength:    input.FileLength,
		UpdatedAt:     input.UpdatedAt,
	}
}

func HistoryExhaustedCursor(chatID string) string {
	return "history:" + chatID + ":exhausted"
}
