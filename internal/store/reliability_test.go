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
