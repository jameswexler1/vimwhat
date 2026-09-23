package store

import (
	"testing"
	"time"

	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"
)

func TestEditRewritesForwardPayloadAndRejectsLateOriginal(t *testing.T) {
	for _, media := range []bool{false, true} {
		s, ctx := reliabilityStore(t)
		m := &waE2E.Message{Conversation: proto.String("original")}
		if media {
			m = &waE2E.Message{ImageMessage: &waE2E.ImageMessage{Caption: proto.String("original"), URL: proto.String("media-url")}}
		}
		data, err := proto.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.AddHistoricalMessageWithPayload(ctx, Message{ID: "m", ChatID: "chat", Sender: "me", Body: "original", IsOutgoing: true}, MessagePayload{MessageID: "m", Payload: data}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.UpdateMessageBody(ctx, "m", "edited", time.Now()); err != nil {
			t.Fatal(err)
		}
		if err := s.SaveOutgoingPayload(ctx, "m", data); err != nil {
			t.Fatal(err)
		}
		got, ok, err := s.MessagePayload(ctx, "m")
		if err != nil || !ok {
			t.Fatalf("payload=%v %v", ok, err)
		}
		m = &waE2E.Message{}
		if err := proto.Unmarshal(got.Payload, m); err != nil {
			t.Fatal(err)
		}
		if media {
			if m.GetImageMessage().GetCaption() != "edited" || m.GetImageMessage().GetURL() != "media-url" {
				t.Fatalf("payload %+v", m)
			}
		} else if m.GetConversation() != "edited" {
			t.Fatalf("payload %+v", m)
		}
	}
}
