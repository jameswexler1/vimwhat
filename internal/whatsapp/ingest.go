package whatsapp

import (
	"context"
	"fmt"

	"maybewhats/internal/store"
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
		return i.Store.UpsertChat(ctx, store.Chat{
			ID:            event.Chat.ID,
			JID:           event.Chat.JID,
			Title:         event.Chat.Title,
			Kind:          event.Chat.Kind,
			Unread:        event.Chat.Unread,
			Pinned:        event.Chat.Pinned,
			Muted:         event.Chat.Muted,
			LastMessageAt: event.Chat.LastMessageAt,
		})
	case EventMessageUpsert:
		return i.Store.AddMessage(ctx, store.Message{
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
		})
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
		return i.Store.UpdateMessageStatus(ctx, event.Receipt.MessageID, event.Receipt.Status)
	default:
		return fmt.Errorf("unsupported whatsapp event kind %q", event.Kind)
	}
}
