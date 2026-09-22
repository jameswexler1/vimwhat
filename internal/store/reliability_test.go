package store

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func reliabilityStore(t *testing.T) (*Store, context.Context) {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "state.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ctx := context.Background()
	if err := s.UpsertChat(ctx, Chat{ID: "chat", Title: "Alice"}); err != nil {
		t.Fatal(err)
	}
	return s, ctx
}

func TestSearchReleasesConnectionAtLimit(t *testing.T) {
	for _, count := range []int{1, 2, 3} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			s, ctx := reliabilityStore(t)
			for n := 0; n < count; n++ {
				id := fmt.Sprintf("m%d", n)
				if err := s.AddMessage(ctx, Message{ID: id, ChatID: "chat", Sender: "Alice", Body: "hello", Media: []MediaMetadata{{MessageID: id, FileName: "image.png"}}}); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithTimeout(ctx, time.Second)
			defer cancel()
			messages, err := s.SearchMessages(ctx, "chat", "hello", 2)
			if err != nil {
				t.Fatal(err)
			}
			if len(messages) != min(count, 2) || len(messages[0].Media) != 1 {
				t.Fatalf("incomplete search results: %+v", messages)
			}
		})
	}
}

func TestReplayPreservesNewerContentAndReceipts(t *testing.T) {
	s, ctx := reliabilityStore(t)
	m := Message{ID: "m", ChatID: "chat", Sender: "me", Body: "original", IsOutgoing: true, Status: "sent"}
	if err := s.AddMessage(ctx, m); err != nil {
		t.Fatal(err)
	}
	newer := time.Unix(1700000100, 0)
	if _, err := s.UpdateMessageBody(ctx, "m", "corrected", newer); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateMessageReceiptStatusIfExists(ctx, "m", "read"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddHistoricalMessage(ctx, m); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateMessageBody(ctx, "m", "older edit", newer.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	for _, status := range []string{"sent", "delivered", "failed"} {
		if err := s.UpdateMessageStatus(ctx, "m", status); err != nil {
			t.Fatal(err)
		}
	}
	got, _, err := s.MessageByID(ctx, "m")
	if err != nil {
		t.Fatal(err)
	}
	if got.Body != "corrected" || got.Status != "read" || !got.EditedAt.Equal(newer) {
		t.Fatalf("newer state lost: %+v", got)
	}
	matches, err := s.SearchMessages(ctx, "chat", "corrected", 10)
	if err != nil || len(matches) != 1 {
		t.Fatalf("search index disagrees with body: %+v, %v", matches, err)
	}
}

func TestSendingCanFailBeforeAcknowledgement(t *testing.T) {
	s, ctx := reliabilityStore(t)
	if err := s.AddMessage(ctx, Message{ID: "m", ChatID: "chat", Sender: "me", Body: "text", Status: "sending", IsOutgoing: true}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateMessageStatus(ctx, "m", "failed"); err != nil {
		t.Fatal(err)
	}
	got, _, err := s.MessageByID(ctx, "m")
	if err != nil || got.Status != "failed" {
		t.Fatalf("status=%q error=%v", got.Status, err)
	}
}
