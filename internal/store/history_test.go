package store

import (
	"fmt"
	"testing"
	"time"
)

func TestHistoryAroundAndAfterUseStableTimestampTies(t *testing.T) {
	s, ctx := reliabilityStore(t)
	for i := 0; i < 30; i++ {
		if err := s.AddMessage(ctx, Message{ID: fmt.Sprintf("%02d", i), ChatID: "chat", Sender: "Alice", Body: "text", Timestamp: time.Unix(100, 0)}); err != nil {
			t.Fatal(err)
		}
	}
	for _, limit := range []int{1, 2, 5} {
		messages, err := s.ListMessagesAround(ctx, "chat", "03", limit)
		if err != nil || len(messages) != limit {
			t.Fatalf("window %d: %d %v", limit, len(messages), err)
		}
		found := false
		for _, m := range messages {
			if m.ID == "03" {
				found = true
			}
		}
		if !found {
			t.Fatal("target absent")
		}
	}
	anchor, _, _ := s.MessageByID(ctx, "03")
	after, err := s.ListMessagesAfter(ctx, "chat", anchor, 3)
	if err != nil || len(after) != 3 || after[0].ID != "04" || after[2].ID != "06" {
		t.Fatalf("after=%+v %v", after, err)
	}
}
