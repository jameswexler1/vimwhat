package whatsapp

import (
	"context"
	"fmt"
	"testing"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waE2E"
	waHistorySync "go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/proto/waWeb"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
)

func TestAutomaticHistoryImportsNamesAndRecentWindow(t *testing.T) {
	for _, tc := range []struct {
		kind waHistorySync.HistorySync_HistorySyncType
		want int
	}{
		{waHistorySync.HistorySync_INITIAL_BOOTSTRAP, 50},
		{waHistorySync.HistorySync_RECENT, 50},
		{waHistorySync.HistorySync_FULL, 0},
		{waHistorySync.HistorySync_ON_DEMAND, 75},
	} {
		t.Run(tc.kind.String(), func(t *testing.T) {
			conversation := &waHistorySync.Conversation{
				ID: proto.String("123@s.whatsapp.net"), Name: proto.String("Alice"),
				EndOfHistoryTransfer:     proto.Bool(true),
				EndOfHistoryTransferType: waHistorySync.Conversation_COMPLETE_AND_NO_MORE_MESSAGE_REMAIN_ON_PRIMARY.Enum(),
			}
			for i := 0; i < 75; i++ {
				conversation.Messages = append(conversation.Messages, &waHistorySync.HistorySyncMsg{
					Message: &waWeb.WebMessageInfo{
						Key:              &waCommon.MessageKey{RemoteJID: conversation.ID, ID: proto.String(fmt.Sprint(i)), FromMe: proto.Bool(false)},
						Message:          &waE2E.Message{Conversation: proto.String("hello")},
						MessageTimestamp: proto.Uint64(uint64(1700000000 + i)),
					},
				})
			}
			client := &Client{client: &whatsmeow.Client{}}
			got := client.normalizeHistorySyncEvent(context.Background(), &events.HistorySync{
				Data: &waHistorySync.HistorySync{SyncType: &tc.kind, Conversations: []*waHistorySync.Conversation{conversation}},
			})
			count, names := 0, 0
			for _, event := range got {
				switch event.Kind {
				case EventChatUpsert:
					if event.Chat.Title == "Alice" {
						names++
					}
				case EventMessageUpsert:
					count++
					if !event.Message.Historical {
						t.Fatal("history must not generate live unread notifications")
					}
					if tc.want == 50 && event.Message.Timestamp.Unix() < 1700000025 {
						t.Fatal("imported old message instead of recent window")
					}
				case EventHistoryStatus:
					if tc.kind != waHistorySync.HistorySync_ON_DEMAND {
						t.Fatal("automatic history changed on-demand exhaustion cursor")
					}
				}
			}
			if count != tc.want || names == 0 {
				t.Fatalf("messages=%d, names=%d", count, names)
			}
			if got[len(got)-1].Kind != EventHistoryProgress {
				t.Fatal("completion must follow all imported events")
			}
			if conversation.Messages[0].GetMessage().GetMessageTimestamp() != 1700000000 {
				t.Fatal("mutated shared protocol batch")
			}
		})
	}
}

func TestPushNamesHistoryBecomesContactEvents(t *testing.T) {
	client := &Client{client: &whatsmeow.Client{}}
	got := client.normalizeHistorySyncEvent(context.Background(), &events.HistorySync{
		Data: &waHistorySync.HistorySync{
			SyncType:  waHistorySync.HistorySync_PUSH_NAME.Enum(),
			Pushnames: []*waHistorySync.Pushname{{ID: proto.String("123@s.whatsapp.net"), Pushname: proto.String("Alice")}},
		},
	})
	if len(got) != 2 || got[0].Kind != EventContactUpsert || got[0].Contact.NotifyName != "Alice" {
		t.Fatalf("events = %+v", got)
	}
}
