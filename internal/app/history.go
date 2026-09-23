package app

import (
	"context"
	"fmt"
	"strings"
	"time"
	"vimwhat/internal/store"
	"vimwhat/internal/ui"
	"vimwhat/internal/whatsapp"
)

func isHistoricalImportEvent(event whatsapp.Event) bool {
	switch event.Kind {
	case whatsapp.EventChatUpsert:
		return event.Chat.Historical
	case whatsapp.EventMessageUpsert:
		return event.Message.Historical
	case whatsapp.EventMessageEdit:
		return event.Edit.Historical
	case whatsapp.EventMessageDelete:
		return event.Delete.Historical
	case whatsapp.EventMediaMetadata:
		return event.Media.Historical
	case whatsapp.EventRecentSticker:
		return event.Sticker.Historical
	default:
		return false
	}
}

func classifyConnectionReplay(event whatsapp.Event, connectedAt time.Time, enabled bool) (whatsapp.Event, bool) {
	if !enabled || connectedAt.IsZero() || event.Replayed {
		return event, enabled
	}

	eventTime, ok := replayEventTime(event)
	if !ok || eventTime.IsZero() {
		return event, enabled
	}
	if !eventTime.After(connectedAt) {
		event.Replayed = true
		return event, enabled
	}

	// WhatsApp emits a chat upsert immediately before a new message. Once that
	// pair is newer than this connection, subsequent events are live traffic.
	if event.Kind == whatsapp.EventChatUpsert || event.Kind == whatsapp.EventMessageUpsert {
		return event, false
	}
	return event, enabled
}

func replayEventTime(event whatsapp.Event) (time.Time, bool) {
	switch event.Kind {
	case whatsapp.EventChatUpsert:
		return event.Chat.LastMessageAt, true
	case whatsapp.EventMessageUpsert:
		return event.Message.Timestamp, true
	case whatsapp.EventMessageEdit:
		return event.Edit.EditedAt, true
	case whatsapp.EventMessageDelete:
		return event.Delete.Timestamp, true
	case whatsapp.EventMediaMetadata:
		return event.Media.UpdatedAt, true
	case whatsapp.EventReactionUpdate:
		return event.Reaction.Timestamp, true
	default:
		return time.Time{}, false
	}
}

func isImplicitDatabaseImportEvent(event whatsapp.Event, manualHistoryImport bool) bool {
	if manualHistoryImport {
		return false
	}
	return isHistoricalImportEvent(event) || event.Kind == whatsapp.EventHistoryStatus
}

func isManualHistoryImportEvent(event whatsapp.Event, historyInflight map[string]time.Time) bool {
	if len(historyInflight) == 0 {
		return false
	}
	chatID := historicalImportChatID(event)
	if chatID == "" {
		return false
	}
	_, ok := historyInflight[chatID]
	return ok
}

func historicalImportChatID(event whatsapp.Event) string {
	switch event.Kind {
	case whatsapp.EventChatUpsert:
		if event.Chat.Historical {
			return event.Chat.ID
		}
	case whatsapp.EventMessageUpsert:
		if event.Message.Historical {
			return event.Message.ChatID
		}
	case whatsapp.EventMessageEdit:
		if event.Edit.Historical {
			return event.Edit.ChatID
		}
	case whatsapp.EventMessageDelete:
		if event.Delete.Historical {
			return event.Delete.ChatID
		}
	case whatsapp.EventMediaMetadata:
		if event.Media.Historical {
			return chatIDFromLocalMessageID(event.Media.MessageID)
		}
	case whatsapp.EventHistoryStatus:
		return event.History.ChatID
	}
	return ""
}

func chatIDFromLocalMessageID(messageID string) string {
	chatID, _, ok := strings.Cut(strings.TrimSpace(messageID), "/")
	if !ok {
		return ""
	}
	return chatID
}

func handleHistoryRequest(
	ctx context.Context,
	db *store.Store,
	live WhatsAppLiveSession,
	updates chan<- ui.LiveUpdate,
	inflight map[string]time.Time,
	online bool,
	chatID string,
) {
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		sendLiveUpdate(ctx, updates, ui.LiveUpdate{Status: "history fetch needs an active chat"})
		return
	}
	canonicalChatID, err := canonicalizeHistoryChatID(ctx, live, chatID)
	if err != nil {
		sendLiveUpdate(ctx, updates, ui.LiveUpdate{Status: fmt.Sprintf("history chat failed: %s", shortStatusError(err))})
		return
	}
	chatID = canonicalChatID
	if !online {
		sendLiveUpdate(ctx, updates, ui.LiveUpdate{Status: "history fetch needs WhatsApp online"})
		return
	}
	if requestedAt, ok := inflight[chatID]; ok && time.Since(requestedAt) < historyRequestTimeout {
		sendLiveUpdate(ctx, updates, ui.LiveUpdate{Status: "history already loading"})
		return
	}

	exhausted, err := db.SyncCursor(ctx, whatsapp.HistoryExhaustedCursor(chatID))
	if err != nil {
		sendLiveUpdate(ctx, updates, ui.LiveUpdate{Status: fmt.Sprintf("history cursor failed: %s", shortStatusError(err))})
		return
	}
	if status := blockedHistoryCursorStatus(exhausted); status != "" {
		sendLiveUpdate(ctx, updates, ui.LiveUpdate{Status: status})
		return
	}

	anchorMessage, ok, err := db.OldestMessage(ctx, chatID)
	if err != nil {
		sendLiveUpdate(ctx, updates, ui.LiveUpdate{Status: fmt.Sprintf("history anchor failed: %s", shortStatusError(err))})
		return
	}
	if !ok {
		sendLiveUpdate(ctx, updates, ui.LiveUpdate{Status: "history fetch needs a local message anchor"})
		return
	}
	if strings.TrimSpace(anchorMessage.RemoteID) == "" {
		sendLiveUpdate(ctx, updates, ui.LiveUpdate{Status: "history anchor has no WhatsApp message id"})
		return
	}

	anchor := whatsapp.HistoryAnchor{
		ChatJID:   anchorMessage.ChatJID,
		MessageID: anchorMessage.RemoteID,
		IsFromMe:  anchorMessage.IsOutgoing,
		Timestamp: anchorMessage.Timestamp,
	}
	if strings.TrimSpace(anchor.ChatJID) == "" {
		anchor.ChatJID = anchorMessage.ChatID
	}

	inflight[chatID] = time.Now()
	if err := live.RequestHistoryBefore(ctx, anchor, historyPageSize); err != nil {
		delete(inflight, chatID)
		sendLiveUpdate(ctx, updates, ui.LiveUpdate{Status: fmt.Sprintf("history request failed: %s", shortStatusError(err))})
		return
	}
	sendLiveUpdate(ctx, updates, ui.LiveUpdate{Status: "requested older history"})
}

func historyStatusLine(event whatsapp.HistoryEvent) string {
	switch event.TerminalReason {
	case "no_more":
		return fmt.Sprintf("history exhausted; imported %d older message(s)", event.Messages)
	case "no_access":
		return fmt.Sprintf("older history unavailable; imported %d older message(s)", event.Messages)
	}
	if event.Messages == 0 {
		return "history response contained no older messages"
	}
	return fmt.Sprintf("imported %d older message(s)", event.Messages)
}

func blockedHistoryCursorStatus(value string) string {
	switch value {
	case "no_more":
		return "history exhausted"
	case "no_access":
		return "older history unavailable from primary"
	default:
		return ""
	}
}
