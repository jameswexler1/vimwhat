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

func (i Ingestor) Apply(ctx context.Context, event Event) error {
	if i.Store == nil {
		return fmt.Errorf("store is required")
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
			return i.Store.UpsertChat(ctx, chat)
		}
		return i.Store.UpsertChatPreserveUnread(ctx, chat)
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
		var err error
		if event.Message.Historical {
			_, err = i.Store.AddHistoricalMessage(ctx, message)
		} else {
			_, err = i.Store.AddIncomingMessage(ctx, message)
		}
		return err
	case EventMediaMetadata:
		metadata := store.MediaMetadata{
			MessageID:     event.Media.MessageID,
			MIMEType:      event.Media.MIMEType,
			FileName:      event.Media.FileName,
			SizeBytes:     event.Media.SizeBytes,
			LocalPath:     event.Media.LocalPath,
			ThumbnailPath: event.Media.ThumbnailPath,
			DownloadState: event.Media.DownloadState,
			UpdatedAt:     event.Media.UpdatedAt,
		}
		descriptor := storeMediaDownloadDescriptor(event.Media.Download, event.Media.MessageID)
		if descriptor.MessageID != "" {
			return i.Store.UpsertMediaMetadataWithDownload(ctx, metadata, &descriptor)
		}
		return i.Store.UpsertMediaMetadata(ctx, metadata)
	case EventReceiptUpdate:
		if event.Receipt.MessageID == "" {
			return fmt.Errorf("receipt message id is required")
		}
		_, err := i.Store.UpdateMessageStatusIfExists(ctx, event.Receipt.MessageID, event.Receipt.Status)
		return err
	case EventReactionUpdate:
		return i.Store.UpsertReaction(ctx, store.Reaction{
			MessageID:  event.Reaction.MessageID,
			SenderJID:  event.Reaction.SenderJID,
			Emoji:      event.Reaction.Emoji,
			Timestamp:  event.Reaction.Timestamp,
			IsOutgoing: event.Reaction.IsOutgoing,
			UpdatedAt:  timeOrNow(event.Reaction.Timestamp),
		})
	case EventPresenceUpdate:
		return nil
	case EventHistoryStatus:
		if event.History.ChatID != "" {
			value := "more"
			if event.History.TerminalReason != "" {
				value = event.History.TerminalReason
			}
			return i.Store.SetSyncCursor(ctx, HistoryExhaustedCursor(event.History.ChatID), value)
		}
		return nil
	case EventContactUpsert:
		contact := store.Contact{
			JID:         event.Contact.JID,
			DisplayName: event.Contact.DisplayName,
			NotifyName:  event.Contact.NotifyName,
			Phone:       event.Contact.Phone,
			UpdatedAt:   event.Contact.UpdatedAt,
		}
		if err := i.Store.UpsertContact(ctx, contact); err != nil {
			return err
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
			return nil
		}
		_, err := i.Store.UpdateChatTitleIfExists(ctx, store.Chat{
			ID:          event.Contact.JID,
			JID:         event.Contact.JID,
			Title:       title,
			TitleSource: source,
			Kind:        "direct",
		})
		return err
	case EventConnectionState:
		return nil
	default:
		return fmt.Errorf("unsupported whatsapp event kind %q", event.Kind)
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
