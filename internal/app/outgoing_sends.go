package app

import (
	"context"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"vimwhat/internal/media"
	"vimwhat/internal/store"
	"vimwhat/internal/ui"
	"vimwhat/internal/whatsapp"
)

func handleTextSendRequest(
	ctx context.Context,
	db *store.Store,
	live WhatsAppLiveSession,
	updates chan<- ui.LiveUpdate,
	wg *sync.WaitGroup,
	online bool,
	request textSendRequest,
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
		sendTextQueuedResult(ctx, request, store.Message{}, fmt.Errorf("store is required"))
		return
	}
	if live == nil {
		sendTextQueuedResult(ctx, request, store.Message{}, fmt.Errorf("whatsapp live session unavailable"))
		return
	}
	if !online {
		sendTextQueuedResult(ctx, request, store.Message{}, fmt.Errorf("text send needs WhatsApp online"))
		return
	}

	body := strings.TrimSpace(request.Body)
	if body == "" {
		sendTextQueuedResult(ctx, request, store.Message{}, fmt.Errorf("text body is required"))
		return
	}
	chatJID, err := canonicalizeLiveChatID(ctx, live, request.ChatID)
	if err != nil {
		sendTextQueuedResult(ctx, request, store.Message{}, err)
		return
	}
	remoteID := strings.TrimSpace(live.GenerateMessageID())
	if remoteID == "" {
		sendTextQueuedResult(ctx, request, store.Message{}, fmt.Errorf("generate message id failed"))
		return
	}

	now := time.Now()
	message := store.Message{
		ID:         whatsapp.LocalMessageID(chatJID, remoteID),
		RemoteID:   remoteID,
		ChatID:     chatJID,
		ChatJID:    chatJID,
		Sender:     "me",
		SenderJID:  "me",
		Body:       body,
		Timestamp:  now,
		IsOutgoing: true,
		Status:     "sending",
		Mentions:   cloneMessageMentionsForMessage(request.Mentions, whatsapp.LocalMessageID(chatJID, remoteID), now),
	}
	if request.Quote != nil {
		message.QuotedMessageID = request.Quote.ID
		message.QuotedRemoteID = request.Quote.RemoteID
	}
	if message.ID == "" {
		sendTextQueuedResult(ctx, request, store.Message{}, fmt.Errorf("message id is required"))
		return
	}
	payload, err := whatsapp.TextPayload(whatsapp.TextSendRequest{Body: mentionWireBody(body, request.Mentions), MentionedJIDs: mentionedJIDs(request.Mentions)})
	if err != nil {
		sendTextQueuedResult(ctx, request, store.Message{}, err)
		return
	}
	if _, err := db.AddHistoricalMessageWithPayload(ctx, message, store.MessagePayload{MessageID: message.ID, Payload: payload}); err != nil {
		sendTextQueuedResult(ctx, request, store.Message{}, err)
		return
	}

	sendTextQueuedResult(ctx, request, message, nil)
	if wg != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			completeQueuedTextSend(ctx, db, live, updates, message, body, request.Quote, request.Mentions)
		}()
		return
	}
	completeQueuedTextSend(ctx, db, live, updates, message, body, request.Quote, request.Mentions)
}

func sendTextQueuedResult(ctx context.Context, request textSendRequest, message store.Message, err error) {
	if request.Result == nil {
		return
	}
	select {
	case request.Result <- textSendQueuedResult{Message: message, Err: err}:
	case <-ctx.Done():
	}
}

func completeQueuedTextSend(ctx context.Context, db *store.Store, live WhatsAppLiveSession, updates chan<- ui.LiveUpdate, message store.Message, body string, quote *store.Message, mentions []store.MessageMention) {
	sendCtx, cancel := context.WithTimeout(ctx, textSendTimeout)
	defer cancel()
	request := whatsapp.TextSendRequest{
		ChatJID:       message.ChatJID,
		Body:          mentionWireBody(body, mentions),
		RemoteID:      message.RemoteID,
		MentionedJIDs: mentionedJIDs(mentions),
	}
	if quote != nil {
		request.QuotedRemoteID = quote.RemoteID
		request.QuotedSenderJID = quote.SenderJID
		request.QuotedMessageBody = quote.Body
	}
	result, err := live.SendText(sendCtx, request)
	if !persistSendPayload(ctx, db, updates, message.ID, result.Payload) {
		return
	}
	if err != nil {
		storeCtx, cancelStore := backgroundStoreWriteContext(ctx)
		_ = db.UpdateMessageStatus(storeCtx, message.ID, outgoingFailureStatus(sendCtx, err))
		cancelStore()
		sendLiveUpdate(ctx, updates, ui.LiveUpdate{
			Refresh: true,
			Status:  fmt.Sprintf("send failed: %s", shortStatusError(err)),
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
			Status:  fmt.Sprintf("send status failed: %s", shortStatusError(err)),
		})
		return
	}
	sendLiveUpdate(ctx, updates, ui.LiveUpdate{
		Refresh: true,
		Status:  "sent message",
	})
}

func handleMediaSendRequest(
	ctx context.Context,
	db *store.Store,
	live WhatsAppLiveSession,
	updates chan<- ui.LiveUpdate,
	wg *sync.WaitGroup,
	online bool,
	request mediaSendRequest,
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
		sendMediaQueuedResult(ctx, request, store.Message{}, fmt.Errorf("store is required"))
		return
	}
	if live == nil {
		sendMediaQueuedResult(ctx, request, store.Message{}, fmt.Errorf("whatsapp live session unavailable"))
		return
	}
	if !online {
		sendMediaQueuedResult(ctx, request, store.Message{}, fmt.Errorf("media send needs WhatsApp online"))
		return
	}
	if request.RetryID != "" {
		handleOutgoingRetry(ctx, db, live, updates, wg, request)
		return
	}
	if request.Sticker != nil {
		handleStickerSendRequest(ctx, db, live, updates, wg, request)
		return
	}
	attachment, err := prepareLiveAttachmentForSend(request.Attachments)
	if err != nil {
		sendMediaQueuedResult(ctx, request, store.Message{}, err)
		return
	}
	body := strings.TrimSpace(request.Body)
	if media.MediaKind(attachment.MIMEType, attachment.FileName) == media.KindAudio && body != "" {
		sendMediaQueuedResult(ctx, request, store.Message{}, fmt.Errorf("audio attachments do not support captions"))
		return
	}
	chatJID, err := canonicalizeLiveChatID(ctx, live, request.ChatID)
	if err != nil {
		sendMediaQueuedResult(ctx, request, store.Message{}, err)
		return
	}
	remoteID := strings.TrimSpace(live.GenerateMessageID())
	if remoteID == "" {
		sendMediaQueuedResult(ctx, request, store.Message{}, fmt.Errorf("generate message id failed"))
		return
	}

	now := time.Now()
	message := store.Message{
		ID:         whatsapp.LocalMessageID(chatJID, remoteID),
		RemoteID:   remoteID,
		ChatID:     chatJID,
		ChatJID:    chatJID,
		Sender:     "me",
		SenderJID:  "me",
		Body:       body,
		Timestamp:  now,
		IsOutgoing: true,
		Status:     "sending",
		Media:      liveMediaForOutgoingMessage(whatsapp.LocalMessageID(chatJID, remoteID), []ui.Attachment{attachment}, now),
		Mentions:   cloneMessageMentionsForMessage(request.Mentions, whatsapp.LocalMessageID(chatJID, remoteID), now),
	}
	if request.Quote != nil {
		message.QuotedMessageID = request.Quote.ID
		message.QuotedRemoteID = request.Quote.RemoteID
	}
	if message.ID == "" {
		sendMediaQueuedResult(ctx, request, store.Message{}, fmt.Errorf("message id is required"))
		return
	}
	if err := db.AddMessage(ctx, message); err != nil {
		sendMediaQueuedResult(ctx, request, store.Message{}, err)
		return
	}

	sendMediaQueuedResult(ctx, request, message, nil)
	if wg != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			completeQueuedMediaSend(ctx, db, live, updates, message, attachment, request.Quote, request.Mentions)
		}()
		return
	}
	completeQueuedMediaSend(ctx, db, live, updates, message, attachment, request.Quote, request.Mentions)
}

func handleStickerSendRequest(
	ctx context.Context,
	db *store.Store,
	live WhatsAppLiveSession,
	updates chan<- ui.LiveUpdate,
	wg *sync.WaitGroup,
	request mediaSendRequest,
) {
	sticker, err := prepareLiveStickerForSend(*request.Sticker)
	if err != nil {
		sendMediaQueuedResult(ctx, request, store.Message{}, err)
		return
	}
	chatJID, err := canonicalizeLiveChatID(ctx, live, request.ChatID)
	if err != nil {
		sendMediaQueuedResult(ctx, request, store.Message{}, err)
		return
	}
	remoteID := strings.TrimSpace(live.GenerateMessageID())
	if remoteID == "" {
		sendMediaQueuedResult(ctx, request, store.Message{}, fmt.Errorf("generate message id failed"))
		return
	}

	messageID := whatsapp.LocalMessageID(chatJID, remoteID)
	now := time.Now()
	message := store.Message{
		ID:         messageID,
		RemoteID:   remoteID,
		ChatID:     chatJID,
		ChatJID:    chatJID,
		Sender:     "me",
		SenderJID:  "me",
		Timestamp:  now,
		IsOutgoing: true,
		Status:     "sending",
		Media:      mediaForOutgoingStickerMessage(messageID, sticker, now),
	}
	if request.Quote != nil {
		message.QuotedMessageID = request.Quote.ID
		message.QuotedRemoteID = request.Quote.RemoteID
	}
	if message.ID == "" {
		sendMediaQueuedResult(ctx, request, store.Message{}, fmt.Errorf("message id is required"))
		return
	}
	if err := db.AddMessage(ctx, message); err != nil {
		sendMediaQueuedResult(ctx, request, store.Message{}, err)
		return
	}

	sendMediaQueuedResult(ctx, request, message, nil)
	if wg != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			completeQueuedStickerSend(ctx, db, live, updates, message, sticker, request.Quote)
		}()
		return
	}
	completeQueuedStickerSend(ctx, db, live, updates, message, sticker, request.Quote)
}

func sendMediaQueuedResult(ctx context.Context, request mediaSendRequest, message store.Message, err error) {
	if request.Result == nil {
		return
	}
	select {
	case request.Result <- mediaSendQueuedResult{Message: message, Err: err}:
	case <-ctx.Done():
	}
}

func completeQueuedMediaSend(ctx context.Context, db *store.Store, live WhatsAppLiveSession, updates chan<- ui.LiveUpdate, message store.Message, attachment ui.Attachment, quote *store.Message, mentions []store.MessageMention) {
	sendCtx, cancel := context.WithTimeout(ctx, mediaSendTimeout)
	defer cancel()
	request := whatsapp.MediaSendRequest{
		ChatJID:       message.ChatJID,
		LocalPath:     attachment.LocalPath,
		FileName:      attachment.FileName,
		MIMEType:      attachment.MIMEType,
		Caption:       mentionWireBody(message.Body, mentions),
		RemoteID:      message.RemoteID,
		MentionedJIDs: mentionedJIDs(mentions),
	}
	if quote != nil {
		request.QuotedRemoteID = quote.RemoteID
		request.QuotedSenderJID = quote.SenderJID
		request.QuotedMessageBody = quote.Body
	}
	result, err := live.SendMedia(sendCtx, request)
	if !persistSendPayload(ctx, db, updates, message.ID, result.Payload) {
		return
	}
	if err != nil {
		storeCtx, cancelStore := backgroundStoreWriteContext(ctx)
		_ = db.UpdateMessageStatus(storeCtx, message.ID, outgoingFailureStatus(sendCtx, err))
		cancelStore()
		sendLiveUpdate(ctx, updates, ui.LiveUpdate{
			Refresh: true,
			Status:  fmt.Sprintf("send failed: %s", shortStatusError(err)),
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
			Status:  fmt.Sprintf("send status failed: %s", shortStatusError(err)),
		})
		return
	}
	sendLiveUpdate(ctx, updates, ui.LiveUpdate{
		Refresh: true,
		Status:  mediaSendStatus(result),
	})
}

func completeQueuedStickerSend(ctx context.Context, db *store.Store, live WhatsAppLiveSession, updates chan<- ui.LiveUpdate, message store.Message, sticker store.RecentSticker, quote *store.Message) {
	sendCtx, cancel := context.WithTimeout(ctx, mediaSendTimeout)
	defer cancel()
	request := whatsapp.StickerSendRequest{
		ChatJID:            message.ChatJID,
		LocalPath:          sticker.LocalPath,
		FileName:           sticker.FileName,
		MIMEType:           sticker.MIMEType,
		Width:              uint32(max(0, sticker.Width)),
		Height:             uint32(max(0, sticker.Height)),
		IsAnimated:         sticker.IsAnimated,
		IsLottie:           sticker.IsLottie,
		AccessibilityLabel: "",
		RemoteID:           message.RemoteID,
	}
	if quote != nil {
		request.QuotedRemoteID = quote.RemoteID
		request.QuotedSenderJID = quote.SenderJID
		request.QuotedMessageBody = quote.Body
	}
	result, err := live.SendSticker(sendCtx, request)
	if !persistSendPayload(ctx, db, updates, message.ID, result.Payload) {
		return
	}
	if err != nil {
		storeCtx, cancelStore := backgroundStoreWriteContext(ctx)
		_ = db.UpdateMessageStatus(storeCtx, message.ID, outgoingFailureStatus(sendCtx, err))
		cancelStore()
		sendLiveUpdate(ctx, updates, ui.LiveUpdate{
			Refresh: true,
			Status:  fmt.Sprintf("sticker send failed: %s", shortStatusError(err)),
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
			Status:  fmt.Sprintf("send status failed: %s", shortStatusError(err)),
		})
		return
	}
	refreshRecentStickerAfterSend(ctx, db, sticker, time.Now())
	sendLiveUpdate(ctx, updates, ui.LiveUpdate{
		Refresh: true,
		Status:  stickerSendStatus(result),
	})
}

func mediaSendStatus(result whatsapp.SendResult) string {
	if notice := strings.TrimSpace(result.Notice); notice != "" {
		return "sent attachment; " + notice
	}
	return "sent attachment"
}

func stickerSendStatus(result whatsapp.SendResult) string {
	if notice := strings.TrimSpace(result.Notice); notice != "" {
		return "sent sticker; " + notice
	}
	return "sent sticker"
}

func retryMediaSendRequest(ctx context.Context, db *store.Store, message store.Message, result chan mediaSendQueuedResult) (mediaSendRequest, error) {
	if retryMessageHasSticker(message) {
		sticker, err := retryStickerForMessage(message)
		if err != nil {
			return mediaSendRequest{}, err
		}
		request := mediaSendRequest{
			RetryID: message.ID,
			Context: ctx,
			ChatID:  retryChatID(message),
			Sticker: &sticker,
			Result:  result,
		}
		quote, err := retryQuoteForMessage(ctx, db, message)
		if err != nil {
			return mediaSendRequest{}, err
		}
		request.Quote = quote
		return request, nil
	}
	attachment, err := retryAttachmentForMessage(message)
	if err != nil {
		return mediaSendRequest{}, err
	}
	request := mediaSendRequest{
		RetryID:     message.ID,
		Context:     ctx,
		ChatID:      retryChatID(message),
		Body:        strings.TrimSpace(message.Body),
		Attachments: []ui.Attachment{attachment},
		Mentions:    slices.Clone(message.Mentions),
		Result:      result,
	}
	quote, err := retryQuoteForMessage(ctx, db, message)
	if err != nil {
		return mediaSendRequest{}, err
	}
	request.Quote = quote
	return request, nil
}

func retryMessageHasSticker(message store.Message) bool {
	return len(message.Media) == 1 && strings.EqualFold(strings.TrimSpace(message.Media[0].Kind), "sticker")
}

func retryAttachmentForMessage(message store.Message) (ui.Attachment, error) {
	item, err := retryMediaItemForMessage(message)
	if err != nil {
		return ui.Attachment{}, err
	}
	if strings.EqualFold(strings.TrimSpace(item.Kind), "sticker") {
		return ui.Attachment{}, fmt.Errorf("retry sticker media through sticker send")
	}
	attachment, err := prepareLiveAttachmentForSend([]ui.Attachment{{
		LocalPath:     item.LocalPath,
		FileName:      item.FileName,
		MIMEType:      item.MIMEType,
		SizeBytes:     item.SizeBytes,
		ThumbnailPath: item.ThumbnailPath,
		DownloadState: item.DownloadState,
	}})
	if err != nil {
		return ui.Attachment{}, err
	}
	return attachment, nil
}

func retryStickerForMessage(message store.Message) (store.RecentSticker, error) {
	item, err := retryMediaItemForMessage(message)
	if err != nil {
		return store.RecentSticker{}, err
	}
	sticker := store.RecentSticker{
		ID:         localStickerID(store.RecentSticker{LocalPath: item.LocalPath, FileName: item.FileName}),
		MIMEType:   item.MIMEType,
		FileName:   item.FileName,
		LocalPath:  item.LocalPath,
		FileLength: item.SizeBytes,
		IsAnimated: item.IsAnimated,
		IsLottie:   item.IsLottie,
		UpdatedAt:  time.Now(),
	}
	return prepareLiveStickerForSend(sticker)
}

func retryMediaItemForMessage(message store.Message) (store.MediaMetadata, error) {
	if !message.IsOutgoing {
		return store.MediaMetadata{}, fmt.Errorf("retry needs an outgoing message")
	}
	if message.Status != "failed" && message.Status != "uncertain" {
		return store.MediaMetadata{}, fmt.Errorf("retry needs a failed or uncertain message")
	}
	if len(message.Media) == 0 {
		return store.MediaMetadata{}, fmt.Errorf("retry needs a media attachment")
	}
	if len(message.Media) > 1 {
		return store.MediaMetadata{}, fmt.Errorf("only one attachment per message is supported")
	}
	return message.Media[0], nil
}

func retryQuoteForMessage(ctx context.Context, db *store.Store, message store.Message) (*store.Message, error) {
	if db == nil || strings.TrimSpace(message.QuotedMessageID) == "" {
		return retryRemoteOnlyQuote(message), nil
	}
	quoted, ok, err := db.MessageByID(ctx, message.QuotedMessageID)
	if err != nil {
		return nil, err
	}
	if ok {
		return &quoted, nil
	}
	return retryRemoteOnlyQuote(message), nil
}

func retryRemoteOnlyQuote(message store.Message) *store.Message {
	quotedRemoteID := strings.TrimSpace(message.QuotedRemoteID)
	if quotedRemoteID == "" {
		return nil
	}
	return &store.Message{RemoteID: quotedRemoteID}
}

func retryChatID(message store.Message) string {
	if chatJID := strings.TrimSpace(message.ChatJID); chatJID != "" {
		return chatJID
	}
	return strings.TrimSpace(message.ChatID)
}

func prepareLiveAttachmentForSend(attachments []ui.Attachment) (ui.Attachment, error) {
	if len(attachments) == 0 {
		return ui.Attachment{}, fmt.Errorf("attachment is required")
	}
	if len(attachments) > 1 {
		return ui.Attachment{}, fmt.Errorf("only one attachment per message is supported")
	}
	attachment := attachments[0]
	localPath := strings.TrimSpace(attachment.LocalPath)
	if localPath == "" {
		return ui.Attachment{}, fmt.Errorf("attachment local path is required")
	}
	info, err := os.Stat(localPath)
	if err != nil {
		return ui.Attachment{}, fmt.Errorf("stat attachment: %w", err)
	}
	if info.IsDir() {
		return ui.Attachment{}, fmt.Errorf("attachment path is a directory")
	}
	if strings.TrimSpace(attachment.FileName) == "" {
		attachment.FileName = info.Name()
	}
	if attachment.SizeBytes <= 0 {
		attachment.SizeBytes = info.Size()
	}
	if strings.TrimSpace(attachment.MIMEType) == "" {
		if guessed := mime.TypeByExtension(strings.ToLower(filepath.Ext(attachment.FileName))); guessed != "" {
			attachment.MIMEType = guessed
		} else {
			attachment.MIMEType = "application/octet-stream"
		}
	}
	attachment.LocalPath = localPath
	attachment.DownloadState = "downloaded"
	return attachment, nil
}

func prepareLiveStickerForSend(sticker store.RecentSticker) (store.RecentSticker, error) {
	localPath := strings.TrimSpace(sticker.LocalPath)
	if localPath == "" {
		return store.RecentSticker{}, fmt.Errorf("sticker local path is required")
	}
	info, err := os.Stat(localPath)
	if err != nil {
		return store.RecentSticker{}, fmt.Errorf("stat sticker file: %w", err)
	}
	if info.IsDir() {
		return store.RecentSticker{}, fmt.Errorf("sticker path is a directory")
	}
	fileName := strings.TrimSpace(sticker.FileName)
	if fileName == "" {
		fileName = info.Name()
	}
	mimeType := strings.TrimSpace(sticker.MIMEType)
	if mimeType == "" {
		mimeType = mime.TypeByExtension(strings.ToLower(filepath.Ext(fileName)))
	}
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	if sticker.IsLottie || strings.EqualFold(filepath.Ext(fileName), ".tgs") {
		return store.RecentSticker{}, fmt.Errorf("lottie sticker send is not supported yet")
	}
	if !isSupportedOutgoingSticker(mimeType, fileName) {
		return store.RecentSticker{}, fmt.Errorf("unsupported sticker MIME type %q", mimeType)
	}
	sticker.ID = localStickerID(sticker)
	sticker.LocalPath = localPath
	sticker.FileName = fileName
	sticker.MIMEType = mimeType
	sticker.FileLength = info.Size()
	sticker.UpdatedAt = time.Now()
	return sticker, nil
}

func isSupportedOutgoingSticker(mimeType, fileName string) bool {
	mimeType = strings.ToLower(strings.TrimSpace(mimeType))
	fileName = strings.ToLower(strings.TrimSpace(fileName))
	return mimeType == "image/webp" || strings.HasSuffix(fileName, ".webp")
}
