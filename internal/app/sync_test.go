package app

import (
	"testing"
	"time"

	"vimwhat/internal/whatsapp"
)

func TestClassifyConnectionReplayKeepsOldEventsHistoricalUntilLiveTraffic(t *testing.T) {
	connectedAt := time.Unix(2_000, 0)
	enabled := true

	oldChat, enabled := classifyConnectionReplay(whatsapp.Event{
		Kind: whatsapp.EventChatUpsert,
		Chat: whatsapp.ChatEvent{LastMessageAt: connectedAt.Add(-time.Minute)},
	}, connectedAt, enabled)
	if !oldChat.Replayed || !enabled {
		t.Fatalf("old chat classification = replayed:%v enabled:%v", oldChat.Replayed, enabled)
	}

	oldMessage, enabled := classifyConnectionReplay(whatsapp.Event{
		Kind:    whatsapp.EventMessageUpsert,
		Message: whatsapp.MessageEvent{Timestamp: connectedAt},
	}, connectedAt, enabled)
	if !oldMessage.Replayed || !enabled {
		t.Fatalf("old message classification = replayed:%v enabled:%v", oldMessage.Replayed, enabled)
	}

	liveChat, enabled := classifyConnectionReplay(whatsapp.Event{
		Kind: whatsapp.EventChatUpsert,
		Chat: whatsapp.ChatEvent{LastMessageAt: connectedAt.Add(time.Second)},
	}, connectedAt, enabled)
	if liveChat.Replayed || enabled {
		t.Fatalf("live chat classification = replayed:%v enabled:%v", liveChat.Replayed, enabled)
	}
}
