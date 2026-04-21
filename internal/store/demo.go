package store

import (
	"context"
	"fmt"
	"time"
)

func (s *Store) SeedDemoData(ctx context.Context) error {
	now := time.Now().Truncate(time.Minute)

	chats := []Chat{
		{ID: "demo-chat-alice", Title: "Alice", Unread: 2, Pinned: true},
		{ID: "demo-chat-family", Title: "Family Group", Muted: true},
		{ID: "demo-chat-project", Title: "Project Maybewhats", Unread: 5},
		{ID: "demo-chat-ops", Title: "Ops", Unread: 1},
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
			Sender:    "Alice",
			Body:      "Are you finally building the terminal client?",
			Timestamp: now.Add(-22 * time.Minute),
		},
		{
			ID:         "demo-msg-alice-2",
			ChatID:     "demo-chat-alice",
			Sender:     "me",
			Body:       "Yes. The shell is real now and backed by SQLite.",
			Timestamp:  now.Add(-20 * time.Minute),
			IsOutgoing: true,
		},
		{
			ID:        "demo-msg-alice-3",
			ChatID:    "demo-chat-alice",
			Sender:    "Alice",
			Body:      "Good. Make the motions feel strict, not chatty.",
			Timestamp: now.Add(-18 * time.Minute),
		},
		{
			ID:        "demo-msg-family-1",
			ChatID:    "demo-chat-family",
			Sender:    "Dad",
			Body:      "Dinner on Sunday?",
			Timestamp: now.Add(-4 * time.Hour),
		},
		{
			ID:         "demo-msg-family-2",
			ChatID:     "demo-chat-family",
			Sender:     "me",
			Body:       "Yes, after I finish the next storage pass.",
			Timestamp:  now.Add(-220 * time.Minute),
			IsOutgoing: true,
		},
		{
			ID:        "demo-msg-project-1",
			ChatID:    "demo-chat-project",
			Sender:    "Design",
			Body:      "Need vim-style search, registers, and no mouse dependence.",
			Timestamp: now.Add(-75 * time.Minute),
		},
		{
			ID:        "demo-msg-project-2",
			ChatID:    "demo-chat-project",
			Sender:    "Storage",
			Body:      "Render from local state before protocol sync touches the UI.",
			Timestamp: now.Add(-68 * time.Minute),
		},
		{
			ID:        "demo-msg-project-3",
			ChatID:    "demo-chat-project",
			Sender:    "UI",
			Body:      "Compact terminals should collapse to one focused pane cleanly.",
			Timestamp: now.Add(-61 * time.Minute),
		},
		{
			ID:         "demo-msg-project-4",
			ChatID:     "demo-chat-project",
			Sender:     "me",
			Body:       "That is in place. Next is protocol login and event ingestion.",
			Timestamp:  now.Add(-55 * time.Minute),
			IsOutgoing: true,
		},
		{
			ID:        "demo-msg-ops-1",
			ChatID:    "demo-chat-ops",
			Sender:    "Ops Bot",
			Body:      "Preview backend auto-selected ueberzug++ on this machine.",
			Timestamp: now.Add(-12 * time.Minute),
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

	return nil
}
