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

func TestFullDraftRoundTripAndLegacyReplacement(t *testing.T) {
	s, ctx := reliabilityStore(t)
	draft := ComposerDraft{Media: []MediaMetadata{{LocalPath: "/private/photo.png"}}, Reply: &Message{ID: "quoted"}, Mentions: []MessageMention{{JID: "person"}}}
	if err := s.SaveComposerDraft(ctx, "chat", draft); err != nil {
		t.Fatal(err)
	}
	drafts, err := s.ListComposerDrafts(ctx)
	if err != nil || len(drafts["chat"].Media) != 1 || drafts["chat"].Reply.ID != "quoted" {
		t.Fatalf("drafts=%+v err=%v", drafts, err)
	}
	chats, err := s.ListChats(ctx)
	if err != nil || !chats[0].HasDraft {
		t.Fatalf("attachment-only draft not flagged: %+v %v", chats, err)
	}
	if err := s.SaveDraft(ctx, "chat", "replacement"); err != nil {
		t.Fatal(err)
	}
	drafts, err = s.ListComposerDrafts(ctx)
	if err != nil || len(drafts["chat"].Media) != 0 || drafts["chat"].Reply != nil {
		t.Fatalf("stale full draft survived replacement: %+v %v", drafts, err)
	}
	if err := s.SaveDraft(ctx, "chat", ""); err != nil {
		t.Fatal(err)
	}
	drafts, err = s.ListComposerDrafts(ctx)
	if err != nil || len(drafts) != 0 {
		t.Fatalf("draft not cleared: %+v %v", drafts, err)
	}
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

func TestInterruptedOutboxRecoveryAndRetryClaim(t *testing.T) {
	s, ctx := reliabilityStore(t)
	for _, status := range []string{"sending", "sent", "read"} {
		if err := s.AddMessage(ctx, Message{ID: status, ChatID: "chat", Sender: "me", Body: "text", Status: status, IsOutgoing: true}); err != nil {
			t.Fatal(err)
		}
	}
	n, err := s.RecoverInterruptedSends(ctx)
	if err != nil || n != 1 {
		t.Fatalf("recovered %d: %v", n, err)
	}
	m, _, _ := s.MessageByID(ctx, "sending")
	if m.Status != "uncertain" {
		t.Fatalf("status=%s", m.Status)
	}
	for i := 0; i < 2; i++ {
		claimed, err := s.ClaimOutgoingRetry(ctx, "sending")
		if err != nil || claimed != (i == 0) {
			t.Fatalf("claim %d=%v %v", i, claimed, err)
		}
	}
	claimed, err := s.ClaimOutgoingRetry(ctx, "read")
	if err != nil || claimed {
		t.Fatalf("retried acknowledged message: %v %v", claimed, err)
	}
}

func TestMediaInvalidationClearsOnlyMatchingPaths(t *testing.T) {
	s, ctx := reliabilityStore(t)
	if err := s.AddMessage(ctx, Message{ID: "m", ChatID: "chat", Sender: "Alice", Body: "photo"}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"old.png", "new.png"} {
		if err := s.UpsertMediaMetadata(ctx, MediaMetadata{MessageID: "m", LocalPath: path, ThumbnailPath: path, DownloadState: "downloaded"}); err != nil {
			t.Fatal(err)
		}
		if err := s.UpsertMediaMetadata(ctx, MediaMetadata{MessageID: "m", InvalidLocalPath: "old.png", InvalidThumbnailPath: "old.png", DownloadState: "remote"}); err != nil {
			t.Fatal(err)
		}
		got, err := s.MediaMetadata(ctx, "m")
		if err != nil {
			t.Fatal(err)
		}
		if path == "old.png" && (got.LocalPath != "" || got.ThumbnailPath != "" || got.DownloadState != "remote") {
			t.Fatalf("not invalidated: %+v", got)
		}
		if path == "new.png" && (got.LocalPath != path || got.DownloadState != "downloaded") {
			t.Fatalf("new download invalidated: %+v", got)
		}
	}
}

func TestReadAcknowledgementPreservesConcurrentAndOlderArrivals(t *testing.T) {
	s, ctx := reliabilityStore(t)
	for i, id := range []string{"target", "newer", "late-old"} {
		if _, err := s.AddIncomingMessage(ctx, Message{ID: id, RemoteID: id, ChatID: "chat", Sender: "Alice", Body: "hello", Timestamp: time.Unix(int64(100-i), 0)}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 2; i++ {
		if err := s.AcknowledgeReadTargets(ctx, "chat", []string{"target", "target"}); err != nil {
			t.Fatal(err)
		}
		chats, err := s.ListChats(ctx)
		if err != nil || chats[0].Unread != 2 {
			t.Fatalf("iteration %d unread=%+v %v", i, chats, err)
		}
	}
	if err := s.ClearChatUnread(ctx, "chat"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddIncomingMessage(ctx, Message{ID: "latest", RemoteID: "latest", ChatID: "chat", Sender: "Alice", Body: "hi"}); err != nil {
		t.Fatal(err)
	}
	if err := s.AcknowledgeReadTargets(ctx, "chat", []string{"newer"}); err != nil {
		t.Fatal(err)
	}
	chats, _ := s.ListChats(ctx)
	if chats[0].Unread != 1 {
		t.Fatalf("old receipt consumed new unread: %+v", chats)
	}
}
