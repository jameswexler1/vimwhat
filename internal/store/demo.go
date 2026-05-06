package store

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"time"

	"vimwhat/internal/securefs"
)

func (s *Store) SeedDemoData(ctx context.Context) error {
	now := time.Now().Truncate(time.Minute)
	demoImagePath, demoImageSize, err := s.ensureDemoImage()
	if err != nil {
		return err
	}

	chats := []Chat{
		{ID: "demo-chat-alice", JID: "alice@s.whatsapp.net", Title: "Alice", Kind: "direct", Unread: 2, Pinned: true},
		{ID: "demo-chat-family", JID: "family@g.us", Title: "Family Group", Kind: "group", Muted: true},
		{ID: "demo-chat-project", JID: "project@g.us", Title: "Project Maybewhats", Kind: "group", Unread: 5},
		{ID: "demo-chat-ops", JID: "ops@g.us", Title: "Ops", Kind: "group", Unread: 1},
		{ID: "demo-chat-long", JID: "long@s.whatsapp.net", Title: "Very Long Contact Name That Must Truncate Cleanly", Kind: "direct", Unread: 3},
	}

	for _, chat := range chats {
		if err := s.UpsertChat(ctx, chat); err != nil {
			return err
		}
	}

	messages := []Message{
		{
			ID:        "demo-msg-alice-1",
			ChatID:    "demo-chat-alice",
			ChatJID:   "alice@s.whatsapp.net",
			Sender:    "Alice",
			SenderJID: "alice@s.whatsapp.net",
			Body:      "Are you finally building the terminal client?",
			Timestamp: now.Add(-26 * time.Hour),
		},
		{
			ID:         "demo-msg-alice-2",
			ChatID:     "demo-chat-alice",
			ChatJID:    "alice@s.whatsapp.net",
			Sender:     "me",
			SenderJID:  "me",
			Body:       "Yes. The shell is real now and backed by SQLite.",
			Timestamp:  now.Add(-25*time.Hour + 12*time.Minute),
			IsOutgoing: true,
			Status:     "sent",
		},
		{
			ID:        "demo-msg-alice-3",
			ChatID:    "demo-chat-alice",
			ChatJID:   "alice@s.whatsapp.net",
			Sender:    "Alice",
			SenderJID: "alice@s.whatsapp.net",
			Body:      "Good. Make the motions feel strict, not chatty.",
			Timestamp: now.Add(-18 * time.Minute),
		},
		{
			ID:         "demo-msg-alice-4",
			ChatID:     "demo-chat-alice",
			ChatJID:    "alice@s.whatsapp.net",
			Sender:     "me",
			SenderJID:  "me",
			Body:       "I'm tightening the TUI before touching live sync.",
			Timestamp:  now.Add(-2 * time.Minute),
			IsOutgoing: true,
			Status:     "pending",
		},
		{
			ID:        "demo-msg-family-1",
			ChatID:    "demo-chat-family",
			ChatJID:   "family@g.us",
			Sender:    "Dad",
			SenderJID: "dad@s.whatsapp.net",
			Body:      "Dinner on Sunday?",
			Timestamp: now.Add(-4 * time.Hour),
		},
		{
			ID:         "demo-msg-family-2",
			ChatID:     "demo-chat-family",
			ChatJID:    "family@g.us",
			Sender:     "me",
			SenderJID:  "me",
			Body:       "Yes, after I finish the next storage pass.",
			Timestamp:  now.Add(-220 * time.Minute),
			IsOutgoing: true,
			Status:     "read",
		},
		{
			ID:        "demo-msg-project-1",
			ChatID:    "demo-chat-project",
			ChatJID:   "project@g.us",
			Sender:    "Design",
			SenderJID: "design@s.whatsapp.net",
			Body:      "Need vim-style search, registers, and no mouse dependence.",
			Timestamp: now.Add(-75 * time.Minute),
		},
		{
			ID:        "demo-msg-project-2",
			ChatID:    "demo-chat-project",
			ChatJID:   "project@g.us",
			Sender:    "Storage",
			SenderJID: "storage@s.whatsapp.net",
			Body:      "Render from local state before protocol sync touches the UI.",
			Timestamp: now.Add(-68 * time.Minute),
		},
		{
			ID:        "demo-msg-project-3",
			ChatID:    "demo-chat-project",
			ChatJID:   "project@g.us",
			Sender:    "UI",
			SenderJID: "ui@s.whatsapp.net",
			Body:      "Compact terminals should collapse to one focused pane cleanly.",
			Timestamp: now.Add(-61 * time.Minute),
		},
		{
			ID:         "demo-msg-project-4",
			ChatID:     "demo-chat-project",
			ChatJID:    "project@g.us",
			Sender:     "me",
			SenderJID:  "me",
			Body:       "That is in place. Next is protocol login and event ingestion.",
			Timestamp:  now.Add(-55 * time.Minute),
			IsOutgoing: true,
			Status:     "sent",
		},
		{
			ID:        "demo-msg-project-5",
			ChatID:    "demo-chat-project",
			ChatJID:   "project@g.us",
			Sender:    "QA",
			SenderJID: "qa@s.whatsapp.net",
			Body:      "Long message check: this line should wrap without pushing the layout wider than the terminal, and it should still be easy to scan when the active cursor lands here.",
			Timestamp: now.Add(-45 * time.Minute),
		},
		{
			ID:        "demo-msg-project-media-1",
			ChatID:    "demo-chat-project",
			ChatJID:   "project@g.us",
			Sender:    "Design",
			SenderJID: "design@s.whatsapp.net",
			Body:      "Local demo image. Focus this message and press Enter to render the preview.",
			Timestamp: now.Add(-35 * time.Minute),
			Media: []MediaMetadata{{
				MessageID:     "demo-msg-project-media-1",
				MIMEType:      "image/png",
				FileName:      filepath.Base(demoImagePath),
				SizeBytes:     demoImageSize,
				LocalPath:     demoImagePath,
				DownloadState: "downloaded",
				UpdatedAt:     now,
			}},
		},
		{
			ID:        "demo-msg-ops-1",
			ChatID:    "demo-chat-ops",
			ChatJID:   "ops@g.us",
			Sender:    "Ops Bot",
			SenderJID: "opsbot@s.whatsapp.net",
			Body:      "Preview backend auto-selected ueberzug++ on this machine.",
			Timestamp: now.Add(-12 * time.Minute),
		},
		{
			ID:        "demo-msg-long-1",
			ChatID:    "demo-chat-long",
			ChatJID:   "long@s.whatsapp.net",
			Sender:    "Long Name",
			SenderJID: "long@s.whatsapp.net",
			Body:      "supercalifragilisticexpialidocious-even-this-long-word-should-not-break-the-panel",
			Timestamp: now.Add(-8 * time.Minute),
		},
	}

	for _, message := range messages {
		if err := s.AddMessage(ctx, message); err != nil {
			return err
		}
	}

	if err := s.SaveDraft(ctx, "demo-chat-project", "wire whatsmeow login next"); err != nil {
		return err
	}

	if err := s.SetSyncCursor(ctx, "demo:last-seed", now.Format(time.RFC3339)); err != nil {
		return err
	}

	return nil
}

func (s *Store) ClearDemoData(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin clear demo data: %w", err)
	}

	statements := []struct {
		query string
		arg   string
	}{
		{query: `DELETE FROM message_fts WHERE message_id LIKE ?`, arg: "demo-msg-%"},
		{query: `DELETE FROM media_metadata WHERE message_id LIKE ?`, arg: "demo-msg-%"},
		{query: `DELETE FROM messages WHERE id LIKE ?`, arg: "demo-msg-%"},
		{query: `DELETE FROM drafts WHERE chat_id LIKE ?`, arg: "demo-chat-%"},
		{query: `DELETE FROM chats WHERE id LIKE ?`, arg: "demo-chat-%"},
		{query: `DELETE FROM sync_cursors WHERE name = ?`, arg: "demo:last-seed"},
	}

	for _, stmt := range statements {
		if _, err := tx.ExecContext(ctx, stmt.query, stmt.arg); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("clear demo data: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit clear demo data: %w", err)
	}

	_ = os.RemoveAll(s.demoMediaDir())
	return nil
}

func (s *Store) ensureDemoImage() (string, int64, error) {
	dir := s.demoMediaDir()
	if err := securefs.EnsurePrivateDir(dir); err != nil {
		return "", 0, fmt.Errorf("create demo media dir: %w", err)
	}

	path := filepath.Join(dir, "vimwhat-demo.png")
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, securefs.PrivateFileMode)
	if err != nil {
		return "", 0, fmt.Errorf("create demo image: %w", err)
	}
	if err := securefs.RepairPrivateFile(path); err != nil {
		_ = file.Close()
		return "", 0, err
	}
	defer file.Close()

	const (
		width  = 1024
		height = 512
	)
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			ramp := uint8((x * 180) / width)
			shade := uint8((y * 120) / height)
			switch {
			case x < width/3:
				img.Set(x, y, color.RGBA{R: 38 + ramp/4, G: 130 + shade, B: 154, A: 255})
			case x < 2*width/3:
				img.Set(x, y, color.RGBA{R: 170 + ramp/3, G: 130 + shade/2, B: 55, A: 255})
			default:
				img.Set(x, y, color.RGBA{R: 150 + ramp/3, G: 70 + shade/3, B: 110 + shade/2, A: 255})
			}
			if x%64 == 0 || y%64 == 0 || (x+y)%127 == 0 {
				img.Set(x, y, color.RGBA{R: 250, G: 250, B: 250, A: 255})
			}
		}
	}

	if err := png.Encode(file, img); err != nil {
		return "", 0, fmt.Errorf("encode demo image: %w", err)
	}
	if err := file.Close(); err != nil {
		return "", 0, fmt.Errorf("close demo image: %w", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", 0, fmt.Errorf("stat demo image: %w", err)
	}
	return path, info.Size(), nil
}

func (s *Store) demoMediaDir() string {
	baseDir := filepath.Dir(s.path)
	if baseDir == "." || !filepath.IsAbs(baseDir) {
		baseDir = os.TempDir()
	}
	return filepath.Join(baseDir, "demo-media")
}
