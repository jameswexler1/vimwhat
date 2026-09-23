package app

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
	"vimwhat/internal/store"
	"vimwhat/internal/ui"
	"vimwhat/internal/whatsapp"
)

func handleReadReceiptRequest(
	ctx context.Context,
	db *store.Store,
	live WhatsAppLiveSession,
	updates chan<- ui.LiveUpdate,
	wg *sync.WaitGroup,
	online bool,
	request readReceiptRequest,
) {
	if request.Result == nil {
		return
	}
	if db == nil {
		sendReadReceiptResult(ctx, request, fmt.Errorf("store is required"))
		return
	}
	if live == nil {
		sendReadReceiptResult(ctx, request, fmt.Errorf("whatsapp live session unavailable"))
		return
	}
	if !online {
		sendReadReceiptResult(ctx, request, fmt.Errorf("read receipts need WhatsApp online"))
		return
	}
	targets := readReceiptTargets(request.Chat, request.Messages)
	if len(targets) == 0 {
		sendReadReceiptResult(ctx, request, fmt.Errorf("no loaded unread messages to mark read"))
		return
	}
	sendReadReceiptResult(ctx, request, nil)
	if wg != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			completeReadReceipt(ctx, db, live, updates, request.Chat.ID, targets)
		}()
		return
	}
	completeReadReceipt(ctx, db, live, updates, request.Chat.ID, targets)
}

func sendReadReceiptResult(ctx context.Context, request readReceiptRequest, err error) {
	if request.Result == nil {
		return
	}
	select {
	case request.Result <- err:
	case <-ctx.Done():
	}
}

func completeReadReceipt(ctx context.Context, db *store.Store, live WhatsAppLiveSession, updates chan<- ui.LiveUpdate, chatID string, targets []whatsapp.ReadReceiptTarget) {
	readCtx, cancel := context.WithTimeout(ctx, readReceiptTimeout)
	defer cancel()
	if err := live.MarkRead(readCtx, targets); err != nil {
		sendLiveUpdate(ctx, updates, ui.LiveUpdate{
			ReadChatID: chatID,
			Status:     fmt.Sprintf("mark read failed: %s", shortStatusError(err)),
		})
		return
	}
	storeCtx, cancelStore := backgroundStoreWriteContext(ctx)
	ids := make([]string, 0, len(targets))
	for _, target := range targets {
		ids = append(ids, target.RemoteID)
	}
	err := db.AcknowledgeReadTargets(storeCtx, chatID, ids)
	cancelStore()
	if err != nil {
		sendLiveUpdate(ctx, updates, ui.LiveUpdate{
			ReadChatID: chatID,
			Refresh:    true,
			Status:     fmt.Sprintf("clear unread failed: %s", shortStatusError(err)),
		})
		return
	}
	sendLiveUpdate(ctx, updates, ui.LiveUpdate{
		ReadChatID: chatID,
		Refresh:    true,
		Status:     "marked chat read",
	})
}

func readReceiptTargets(chat store.Chat, messages []store.Message) []whatsapp.ReadReceiptTarget {
	chatJID := strings.TrimSpace(chat.JID)
	if chatJID == "" {
		chatJID = strings.TrimSpace(chat.ID)
	}
	targets := make([]whatsapp.ReadReceiptTarget, 0, len(messages))
	for _, message := range messages {
		if message.IsOutgoing || strings.TrimSpace(message.RemoteID) == "" {
			continue
		}
		messageChatJID := strings.TrimSpace(message.ChatJID)
		if messageChatJID == "" {
			messageChatJID = chatJID
		}
		targets = append(targets, whatsapp.ReadReceiptTarget{
			ChatJID:   messageChatJID,
			RemoteID:  message.RemoteID,
			SenderJID: message.SenderJID,
			Timestamp: message.Timestamp,
		})
	}
	return targets
}

func handleReactionRequest(
	ctx context.Context,
	db *store.Store,
	live WhatsAppLiveSession,
	updates chan<- ui.LiveUpdate,
	wg *sync.WaitGroup,
	online bool,
	request reactionRequest,
) {
	if request.Result == nil {
		return
	}
	if db == nil {
		sendReactionResult(ctx, request, fmt.Errorf("store is required"))
		return
	}
	if live == nil {
		sendReactionResult(ctx, request, fmt.Errorf("whatsapp live session unavailable"))
		return
	}
	if !online {
		sendReactionResult(ctx, request, fmt.Errorf("reactions need WhatsApp online"))
		return
	}
	if strings.TrimSpace(request.Message.RemoteID) == "" {
		sendReactionResult(ctx, request, fmt.Errorf("reaction target has no WhatsApp id"))
		return
	}
	chatJID := strings.TrimSpace(request.Message.ChatJID)
	if chatJID == "" {
		chatJID = strings.TrimSpace(request.Message.ChatID)
	}
	chatJID, err := canonicalizeLiveChatID(ctx, live, chatJID)
	if err != nil {
		sendReactionResult(ctx, request, err)
		return
	}
	remoteID := strings.TrimSpace(live.GenerateMessageID())
	if remoteID == "" {
		sendReactionResult(ctx, request, fmt.Errorf("generate message id failed"))
		return
	}
	sendReactionResult(ctx, request, nil)
	if wg != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			completeReaction(ctx, db, live, updates, request.Message, chatJID, request.Emoji, remoteID)
		}()
		return
	}
	completeReaction(ctx, db, live, updates, request.Message, chatJID, request.Emoji, remoteID)
}

func sendReactionResult(ctx context.Context, request reactionRequest, err error) {
	if request.Result == nil {
		return
	}
	select {
	case request.Result <- err:
	case <-ctx.Done():
	}
}

func completeReaction(ctx context.Context, db *store.Store, live WhatsAppLiveSession, updates chan<- ui.LiveUpdate, message store.Message, chatJID, emoji, remoteID string) {
	sendCtx, cancel := context.WithTimeout(ctx, reactionSendTimeout)
	defer cancel()
	_, err := live.SendReaction(sendCtx, whatsapp.ReactionSendRequest{
		ChatJID:         chatJID,
		TargetRemoteID:  message.RemoteID,
		TargetSenderJID: message.SenderJID,
		Emoji:           emoji,
		RemoteID:        remoteID,
	})
	if err != nil {
		sendLiveUpdate(ctx, updates, ui.LiveUpdate{
			Refresh: true,
			Status:  fmt.Sprintf("reaction failed: %s", shortStatusError(err)),
		})
		return
	}
	storeCtx, cancelStore := backgroundStoreWriteContext(ctx)
	err = db.UpsertReaction(storeCtx, store.Reaction{
		MessageID:  message.ID,
		SenderJID:  "me",
		Emoji:      emoji,
		Timestamp:  time.Now(),
		IsOutgoing: true,
		UpdatedAt:  time.Now(),
	})
	cancelStore()
	if err != nil {
		sendLiveUpdate(ctx, updates, ui.LiveUpdate{
			Refresh: true,
			Status:  fmt.Sprintf("reaction store failed: %s", shortStatusError(err)),
		})
		return
	}
	status := "sent reaction"
	if strings.TrimSpace(emoji) == "" {
		status = "cleared reaction"
	}
	sendLiveUpdate(ctx, updates, ui.LiveUpdate{
		Refresh: true,
		Status:  status,
	})
}

func handleDeleteEveryoneRequest(
	ctx context.Context,
	db *store.Store,
	live WhatsAppLiveSession,
	updates chan<- ui.LiveUpdate,
	wg *sync.WaitGroup,
	online bool,
	request deleteEveryoneRequest,
) {
	if request.Result == nil {
		return
	}
	if db == nil {
		sendDeleteEveryoneResult(ctx, request, "", fmt.Errorf("store is required"))
		return
	}
	if live == nil {
		sendDeleteEveryoneResult(ctx, request, request.Message.ID, fmt.Errorf("whatsapp live session unavailable"))
		return
	}
	if !online {
		sendDeleteEveryoneResult(ctx, request, request.Message.ID, fmt.Errorf("delete for everybody needs WhatsApp online"))
		return
	}
	if !request.Message.IsOutgoing {
		sendDeleteEveryoneResult(ctx, request, request.Message.ID, fmt.Errorf("only your outgoing messages can be deleted for everybody"))
		return
	}
	if strings.TrimSpace(request.Message.RemoteID) == "" {
		sendDeleteEveryoneResult(ctx, request, request.Message.ID, fmt.Errorf("delete target has no WhatsApp id"))
		return
	}
	chatJID, err := canonicalizeLiveChatID(ctx, live, retryChatID(request.Message))
	if err != nil {
		sendDeleteEveryoneResult(ctx, request, request.Message.ID, err)
		return
	}
	if wg != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			completeDeleteForEveryone(ctx, db, live, updates, request, chatJID)
		}()
		return
	}
	completeDeleteForEveryone(ctx, db, live, updates, request, chatJID)
}

func sendDeleteEveryoneResult(ctx context.Context, request deleteEveryoneRequest, messageID string, err error) {
	if request.Result == nil {
		return
	}
	if messageID == "" {
		messageID = request.Message.ID
	}
	select {
	case request.Result <- deleteEveryoneResult{MessageID: messageID, Err: err}:
	case <-ctx.Done():
	}
}

func completeDeleteForEveryone(ctx context.Context, db *store.Store, live WhatsAppLiveSession, updates chan<- ui.LiveUpdate, request deleteEveryoneRequest, chatJID string) {
	sendCtx, cancel := context.WithTimeout(ctx, deleteEveryoneTimeout)
	defer cancel()
	result, err := live.DeleteMessageForEveryone(sendCtx, whatsapp.DeleteForEveryoneRequest{
		ChatJID:        chatJID,
		TargetRemoteID: request.Message.RemoteID,
	})
	if err != nil {
		sendDeleteEveryoneResult(ctx, request, request.Message.ID, err)
		sendLiveUpdate(ctx, updates, ui.LiveUpdate{
			Status: fmt.Sprintf("delete for everybody failed: %s", shortStatusError(err)),
		})
		return
	}
	messageID := strings.TrimSpace(request.Message.ID)
	if messageID == "" {
		messageID = strings.TrimSpace(result.MessageID)
	}
	storeCtx, cancelStore := backgroundStoreWriteContext(ctx)
	_, err = db.DeleteMessageForEveryone(storeCtx, messageID)
	cancelStore()
	if err != nil {
		sendDeleteEveryoneResult(ctx, request, messageID, err)
		sendLiveUpdate(ctx, updates, ui.LiveUpdate{
			Refresh: true,
			Status:  fmt.Sprintf("delete local state failed: %s", shortStatusError(err)),
		})
		return
	}
	sendDeleteEveryoneResult(ctx, request, messageID, nil)
	sendLiveUpdate(ctx, updates, ui.LiveUpdate{
		Refresh: true,
		Status:  "deleted message for everybody",
	})
}

func handleEditMessageRequest(
	ctx context.Context,
	db *store.Store,
	live WhatsAppLiveSession,
	updates chan<- ui.LiveUpdate,
	wg *sync.WaitGroup,
	online bool,
	request editMessageRequest,
) {
	if request.Result == nil {
		return
	}
	if db == nil {
		sendEditMessageResult(ctx, request, "", time.Time{}, fmt.Errorf("store is required"))
		return
	}
	if live == nil {
		sendEditMessageResult(ctx, request, request.Message.ID, time.Time{}, fmt.Errorf("whatsapp live session unavailable"))
		return
	}
	if !online {
		sendEditMessageResult(ctx, request, request.Message.ID, time.Time{}, fmt.Errorf("edit needs WhatsApp online"))
		return
	}
	if !request.Message.IsOutgoing {
		sendEditMessageResult(ctx, request, request.Message.ID, time.Time{}, fmt.Errorf("only your outgoing text messages can be edited"))
		return
	}
	if strings.TrimSpace(request.Message.RemoteID) == "" {
		sendEditMessageResult(ctx, request, request.Message.ID, time.Time{}, fmt.Errorf("edit target has no WhatsApp id"))
		return
	}
	if strings.TrimSpace(request.Body) == "" {
		sendEditMessageResult(ctx, request, request.Message.ID, time.Time{}, fmt.Errorf("edit body is required"))
		return
	}
	if len(request.Message.Media) > 0 {
		sendEditMessageResult(ctx, request, request.Message.ID, time.Time{}, fmt.Errorf("media captions are not editable yet"))
		return
	}
	chatJID, err := canonicalizeLiveChatID(ctx, live, retryChatID(request.Message))
	if err != nil {
		sendEditMessageResult(ctx, request, request.Message.ID, time.Time{}, err)
		return
	}
	if wg != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			completeEditMessage(ctx, db, live, updates, request, chatJID)
		}()
		return
	}
	completeEditMessage(ctx, db, live, updates, request, chatJID)
}

func sendEditMessageResult(ctx context.Context, request editMessageRequest, messageID string, editedAt time.Time, err error) {
	if request.Result == nil {
		return
	}
	if messageID == "" {
		messageID = request.Message.ID
	}
	select {
	case request.Result <- editMessageResult{MessageID: messageID, Body: strings.TrimSpace(request.Body), EditedAt: editedAt, Err: err}:
	case <-ctx.Done():
	}
}

func completeEditMessage(ctx context.Context, db *store.Store, live WhatsAppLiveSession, updates chan<- ui.LiveUpdate, request editMessageRequest, chatJID string) {
	sendCtx, cancel := context.WithTimeout(ctx, editMessageTimeout)
	defer cancel()
	body := strings.TrimSpace(request.Body)
	result, err := live.EditMessage(sendCtx, whatsapp.EditMessageRequest{
		ChatJID:        chatJID,
		TargetRemoteID: request.Message.RemoteID,
		Body:           body,
	})
	if err != nil {
		sendEditMessageResult(ctx, request, request.Message.ID, time.Time{}, err)
		sendLiveUpdate(ctx, updates, ui.LiveUpdate{
			Status: fmt.Sprintf("edit failed: %s", shortStatusError(err)),
		})
		return
	}
	messageID := strings.TrimSpace(request.Message.ID)
	if messageID == "" {
		messageID = strings.TrimSpace(result.MessageID)
	}
	editedAt := result.Timestamp
	if editedAt.IsZero() {
		editedAt = time.Now()
	}
	storeCtx, cancelStore := backgroundStoreWriteContext(ctx)
	updated, err := db.UpdateMessageBody(storeCtx, messageID, body, editedAt)
	cancelStore()
	if err != nil {
		sendEditMessageResult(ctx, request, messageID, editedAt, err)
		sendLiveUpdate(ctx, updates, ui.LiveUpdate{
			Refresh: true,
			Status:  fmt.Sprintf("edit local state failed: %s", shortStatusError(err)),
		})
		return
	}
	if !updated {
		err := fmt.Errorf("message %s does not exist", messageID)
		sendEditMessageResult(ctx, request, messageID, editedAt, err)
		sendLiveUpdate(ctx, updates, ui.LiveUpdate{
			Refresh: true,
			Status:  fmt.Sprintf("edit local state failed: %s", shortStatusError(err)),
		})
		return
	}
	sendEditMessageResult(ctx, request, messageID, editedAt, nil)
	sendLiveUpdate(ctx, updates, ui.LiveUpdate{
		Refresh: true,
		Status:  "edited message",
	})
}

func handleForwardMessagesRequest(
	ctx context.Context,
	db *store.Store,
	live WhatsAppLiveSession,
	updates chan<- ui.LiveUpdate,
	wg *sync.WaitGroup,
	online bool,
	request forwardMessagesRequest,
) {
	if request.Result == nil {
		return
	}
	if request.Context != nil {
		select {
		case <-request.Context.Done():
			return
		default:
		}
	}
	if db == nil {
		sendForwardMessagesResult(ctx, request, forwardMessagesResult{Err: fmt.Errorf("store is required")})
		return
	}
	if live == nil {
		sendForwardMessagesResult(ctx, request, forwardMessagesResult{Err: fmt.Errorf("whatsapp live session unavailable")})
		return
	}
	if !online {
		sendForwardMessagesResult(ctx, request, forwardMessagesResult{Err: fmt.Errorf("forwarding needs WhatsApp online")})
		return
	}
	if len(request.Messages) == 0 {
		sendForwardMessagesResult(ctx, request, forwardMessagesResult{Err: fmt.Errorf("no messages selected")})
		return
	}
	recipients := uniqueForwardRecipients(request.Recipients)
	if len(recipients) == 0 {
		sendForwardMessagesResult(ctx, request, forwardMessagesResult{Err: fmt.Errorf("no forward recipients selected")})
		return
	}

	var result forwardMessagesResult
	for _, source := range request.Messages {
		current, exists, loadErr := db.MessageByID(ctx, source.ID)
		if loadErr != nil {
			result.Failed++
			continue
		}
		if !exists || !current.DeletedAt.IsZero() {
			result.Skipped++
			continue
		}
		source = current
		if err := forwardRequestContextErr(request); err != nil {
			result.Err = err
			sendForwardMessagesResult(ctx, request, result)
			return
		}
		payload, ok, err := db.MessagePayload(ctx, source.ID)
		if err != nil {
			result.Failed++
			continue
		}
		if !ok && source.IsOutgoing && len(source.Media) == 0 && source.Body != "" {
			payload.Payload, err = whatsapp.TextPayload(whatsapp.TextSendRequest{Body: mentionWireBody(source.Body, source.Mentions), MentionedJIDs: mentionedJIDs(source.Mentions)})
			ok = err == nil
		}
		if !ok || len(payload.Payload) == 0 {
			result.Skipped++
			continue
		}
		if _, err := whatsapp.ForwardedMessageFromPayload(payload.Payload); err != nil {
			result.Skipped++
			continue
		}
		markForwarded := !source.IsOutgoing
		for _, recipient := range recipients {
			if err := forwardRequestContextErr(request); err != nil {
				result.Err = err
				sendForwardMessagesResult(ctx, request, result)
				return
			}
			message, err := queueForwardedMessage(ctx, db, live, source, payload.Payload, recipient)
			if err != nil {
				result.Failed++
				continue
			}
			result.Sent++
			if wg != nil {
				wg.Add(1)
				go func(message store.Message, payload []byte, markForwarded bool) {
					defer wg.Done()
					completeQueuedForwardSend(ctx, db, live, updates, message, payload, markForwarded)
				}(message, cloneBytes(payload.Payload), markForwarded)
				continue
			}
			completeQueuedForwardSend(ctx, db, live, updates, message, payload.Payload, markForwarded)
		}
	}
	if result.Sent == 0 && result.Failed > 0 {
		result.Err = fmt.Errorf("forward failed for all available targets")
	}
	sendForwardMessagesResult(ctx, request, result)
}

func forwardRequestContextErr(request forwardMessagesRequest) error {
	if request.Context == nil {
		return nil
	}
	select {
	case <-request.Context.Done():
		return request.Context.Err()
	default:
		return nil
	}
}

func queueForwardedMessage(ctx context.Context, db *store.Store, live WhatsAppLiveSession, source store.Message, payload []byte, recipient store.Chat) (store.Message, error) {
	if len(payload) == 0 {
		return store.Message{}, fmt.Errorf("forward payload is required")
	}
	chatID := strings.TrimSpace(recipient.JID)
	if chatID == "" {
		chatID = strings.TrimSpace(recipient.ID)
	}
	chatJID, err := canonicalizeLiveChatID(ctx, live, chatID)
	if err != nil {
		return store.Message{}, err
	}
	remoteID := strings.TrimSpace(live.GenerateMessageID())
	if remoteID == "" {
		return store.Message{}, fmt.Errorf("generate message id failed")
	}
	message := forwardedLocalMessage(source, chatJID, remoteID, time.Now())
	if message.ID == "" {
		return store.Message{}, fmt.Errorf("message id is required")
	}
	if _, err := db.AddHistoricalMessageWithPayload(ctx, message, store.MessagePayload{
		MessageID: message.ID,
		Payload:   payload,
		UpdatedAt: time.Now(),
	}); err != nil {
		return store.Message{}, err
	}
	return message, nil
}

func forwardedLocalMessage(source store.Message, chatJID, remoteID string, now time.Time) store.Message {
	messageID := whatsapp.LocalMessageID(chatJID, remoteID)
	return store.Message{
		ID:         messageID,
		RemoteID:   remoteID,
		ChatID:     chatJID,
		ChatJID:    chatJID,
		Sender:     "me",
		SenderJID:  "me",
		Body:       source.Body,
		Timestamp:  now,
		IsOutgoing: true,
		Status:     "sending",
		Media:      cloneForwardedMedia(messageID, source.Media, now),
	}
}

func cloneForwardedMedia(messageID string, mediaItems []store.MediaMetadata, updatedAt time.Time) []store.MediaMetadata {
	if len(mediaItems) == 0 {
		return nil
	}
	out := make([]store.MediaMetadata, 0, len(mediaItems))
	for _, item := range mediaItems {
		item.MessageID = messageID
		item.UpdatedAt = updatedAt
		out = append(out, item)
	}
	return out
}

func sendForwardMessagesResult(ctx context.Context, request forwardMessagesRequest, result forwardMessagesResult) {
	if request.Result == nil {
		return
	}
	select {
	case request.Result <- result:
	case <-ctx.Done():
	}
}

func completeQueuedForwardSend(ctx context.Context, db *store.Store, live WhatsAppLiveSession, updates chan<- ui.LiveUpdate, message store.Message, payload []byte, markForwarded bool) {
	sendCtx, cancel := context.WithTimeout(ctx, forwardMessageTimeout)
	defer cancel()
	result, err := live.ForwardMessage(sendCtx, whatsapp.ForwardMessageRequest{
		ChatJID:       message.ChatJID,
		Payload:       payload,
		RemoteID:      message.RemoteID,
		MarkForwarded: markForwarded,
	})
	if err != nil {
		storeCtx, cancelStore := backgroundStoreWriteContext(ctx)
		_ = db.UpdateMessageStatus(storeCtx, message.ID, outgoingFailureStatus(sendCtx, err))
		cancelStore()
		sendLiveUpdate(ctx, updates, ui.LiveUpdate{
			Refresh: true,
			Status:  fmt.Sprintf("forward failed: %s", shortStatusError(err)),
		})
		return
	}
	status := strings.TrimSpace(result.Status)
	if status == "" {
		status = "sent"
	}
	storeCtx, cancelStore := backgroundStoreWriteContext(ctx)
	err = db.UpdateMessageStatus(storeCtx, message.ID, status)
	cancelStore()
	if err != nil {
		sendLiveUpdate(ctx, updates, ui.LiveUpdate{
			Refresh: true,
			Status:  fmt.Sprintf("forward status failed: %s", shortStatusError(err)),
		})
		return
	}
	sendLiveUpdate(ctx, updates, ui.LiveUpdate{
		Refresh: true,
		Status:  "forwarded message",
	})
}
