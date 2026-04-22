package whatsapp

import (
	"context"
	"fmt"

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
		return i.Store.UpsertMediaMetadata(ctx, store.MediaMetadata{
			MessageID:     event.Media.MessageID,
			MIMEType:      event.Media.MIMEType,
			FileName:      event.Media.FileName,
			SizeBytes:     event.Media.SizeBytes,
			LocalPath:     event.Media.LocalPath,
			ThumbnailPath: event.Media.ThumbnailPath,
			DownloadState: event.Media.DownloadState,
			UpdatedAt:     event.Media.UpdatedAt,
		})
	case EventReceiptUpdate:
		if event.Receipt.MessageID == "" {
			return fmt.Errorf("receipt message id is required")
		}
		_, err := i.Store.UpdateMessageStatusIfExists(ctx, event.Receipt.MessageID, event.Receipt.Status)
		return err
	case EventHistoryStatus:
		if event.History.ChatID != "" {
			value := "more"
			if event.History.TerminalReason != "" {
				value = event.History.TerminalReason
			}
			return i.Store.SetSyncCursor(ctx, HistoryExhaustedCursor(event.History.ChatID), value)
		}
		return nil
	case EventConnectionState:
		return nil
	default:
		return fmt.Errorf("unsupported whatsapp event kind %q", event.Kind)
	}
}

func HistoryExhaustedCursor(chatID string) string {
	return "history:" + chatID + ":exhausted"
}
