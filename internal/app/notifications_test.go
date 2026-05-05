package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"vimwhat/internal/notify"
	"vimwhat/internal/store"
	"vimwhat/internal/whatsapp"
)

func TestNotificationIconPathPrefersExistingThumb(t *testing.T) {
	dir := t.TempDir()
	avatarPath := filepath.Join(dir, "avatar.png")
	thumbPath := filepath.Join(dir, "avatar-thumb.png")
	if err := os.WriteFile(avatarPath, []byte("avatar"), 0o644); err != nil {
		t.Fatalf("write avatar: %v", err)
	}
	if err := os.WriteFile(thumbPath, []byte("thumb"), 0o644); err != nil {
		t.Fatalf("write thumb: %v", err)
	}

	got := notificationIconPath(store.Chat{
		AvatarPath:      avatarPath,
		AvatarThumbPath: thumbPath,
	})
	if got != thumbPath {
		t.Fatalf("notificationIconPath() = %q, want thumb path", got)
	}
}

func TestNotificationIconPathFallsBackToFullAvatar(t *testing.T) {
	dir := t.TempDir()
	avatarPath := filepath.Join(dir, "avatar.png")
	if err := os.WriteFile(avatarPath, []byte("avatar"), 0o644); err != nil {
		t.Fatalf("write avatar: %v", err)
	}

	got := notificationIconPath(store.Chat{
		AvatarPath:      avatarPath,
		AvatarThumbPath: filepath.Join(dir, "missing-thumb.png"),
	})
	if got != avatarPath {
		t.Fatalf("notificationIconPath() = %q, want full avatar path", got)
	}
}

func TestNotificationIconPathOmitsMissingAvatars(t *testing.T) {
	dir := t.TempDir()
	got := notificationIconPath(store.Chat{
		AvatarPath:      filepath.Join(dir, "missing-avatar.png"),
		AvatarThumbPath: filepath.Join(dir, "missing-thumb.png"),
	})
	if got != "" {
		t.Fatalf("notificationIconPath() = %q, want empty path", got)
	}
}

func TestQueueNotificationReportsFullQueue(t *testing.T) {
	jobs := make(chan notify.Notification, 1)
	jobs <- notify.Notification{Title: "one"}
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()

	if queueNotification(ctx, jobs, notify.Notification{Title: "two"}) {
		t.Fatal("queueNotification() = true, want false for full queue")
	}
}

func TestBuildNotificationIncludesCachedChatAvatarIcon(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "state.sqlite3"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	dir := t.TempDir()
	avatarPath := filepath.Join(dir, "avatar.png")
	thumbPath := filepath.Join(dir, "avatar-thumb.png")
	if err := os.WriteFile(avatarPath, []byte("avatar"), 0o644); err != nil {
		t.Fatalf("write avatar: %v", err)
	}
	if err := os.WriteFile(thumbPath, []byte("thumb"), 0o644); err != nil {
		t.Fatalf("write thumb: %v", err)
	}

	when := time.Unix(1_700_000_000, 0)
	if err := db.UpsertChat(ctx, store.Chat{
		ID:              "chat-1",
		JID:             "chat-1@s.whatsapp.net",
		Title:           "Alice",
		Kind:            "direct",
		AvatarPath:      avatarPath,
		AvatarThumbPath: thumbPath,
		AvatarUpdatedAt: when,
		LastMessageAt:   when,
	}); err != nil {
		t.Fatalf("UpsertChat() error = %v", err)
	}

	note, ok := buildNotification(ctx, db, notificationContext{activeChatID: "other-chat"}, whatsapp.ApplyResult{
		MessageInserted: true,
		Message: whatsapp.MessageEvent{
			ID:                  "chat-1/msg-1",
			RemoteID:            "msg-1",
			ChatID:              "chat-1",
			ChatJID:             "chat-1@s.whatsapp.net",
			Sender:              "Alice",
			SenderJID:           "alice@s.whatsapp.net",
			Body:                "hello",
			NotificationPreview: "hello",
			Timestamp:           when,
			Status:              "received",
		},
	})
	if !ok {
		t.Fatal("buildNotification() ok = false, want true")
	}
	if note.IconPath != thumbPath {
		t.Fatalf("notification IconPath = %q, want %q", note.IconPath, thumbPath)
	}
}
