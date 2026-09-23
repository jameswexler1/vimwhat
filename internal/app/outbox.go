package app

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"vimwhat/internal/store"
	"vimwhat/internal/ui"
	"vimwhat/internal/whatsapp"
)

func outgoingFailureStatus(ctx context.Context, err error) string {
	var deliveryErr *whatsapp.DeliveryError
	if errors.As(err, &deliveryErr) || ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "uncertain"
	}
	return "failed"
}

func persistSendPayload(ctx context.Context, db *store.Store, updates chan<- ui.LiveUpdate, id string, data []byte) bool {
	writeCtx, cancel := backgroundStoreWriteContext(ctx)
	defer cancel()
	if err := db.SaveOutgoingPayload(writeCtx, id, data); err != nil {
		_ = db.UpdateMessageStatus(writeCtx, id, "uncertain")
		sendLiveUpdate(ctx, updates, ui.LiveUpdate{Refresh: true, Status: fmt.Sprintf("save outgoing payload failed: %s", shortStatusError(err))})
		return false
	}
	return true
}

func handleOutgoingRetry(ctx context.Context, db *store.Store, live WhatsAppLiveSession, updates chan<- ui.LiveUpdate, wg *sync.WaitGroup, request mediaSendRequest) {
	message, found, err := db.MessageByID(ctx, request.RetryID)
	if err == nil && (!found || !message.IsOutgoing || (message.Status != "failed" && message.Status != "uncertain")) {
		err = fmt.Errorf("retry needs a failed or uncertain outgoing message")
	}
	if err == nil && message.RemoteID == "" {
		err = fmt.Errorf("message has no stable delivery ID")
	}
	var send func()
	if err == nil {
		quote, quoteErr := retryQuoteForMessage(ctx, db, message)
		err = quoteErr
		if err == nil {
			switch {
			case len(message.Media) == 0:
				send = func() { completeQueuedTextSend(ctx, db, live, updates, message, message.Body, quote, message.Mentions) }
			case retryMessageHasSticker(message):
				sticker, stickerErr := retryStickerForMessage(message)
				err = stickerErr
				send = func() { completeQueuedStickerSend(ctx, db, live, updates, message, sticker, quote) }
			default:
				attachment, attachmentErr := retryAttachmentForMessage(message)
				err = attachmentErr
				send = func() { completeQueuedMediaSend(ctx, db, live, updates, message, attachment, quote, message.Mentions) }
			}
		}
	}
	if err == nil {
		claimed, claimErr := db.ClaimOutgoingRetry(ctx, message.ID)
		err = claimErr
		if err == nil && !claimed {
			err = fmt.Errorf("message already retried or acknowledged")
		}
	}
	if err != nil {
		sendMediaQueuedResult(ctx, request, store.Message{}, err)
		return
	}
	message.Status = "sending"
	sendMediaQueuedResult(ctx, request, message, nil)
	if wg == nil {
		send()
		return
	}
	wg.Add(1)
	go func() { defer wg.Done(); send() }()
}
