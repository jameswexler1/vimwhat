package app

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"vimwhat/internal/store"
	"vimwhat/internal/ui"
)

func outgoingFailureStatus(ctx context.Context, err error) string {
	if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "uncertain"
	}
	return "failed"
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
