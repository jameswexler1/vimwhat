package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

const defaultMessageWindow = 200

func (s *Store) LoadSnapshot(ctx context.Context, messageLimit int) (Snapshot, error) {
	if messageLimit <= 0 {
		messageLimit = defaultMessageWindow
	}

	chats, err := s.ListChats(ctx)
	if err != nil {
		return Snapshot{}, err
	}

	snapshot := Snapshot{
		Chats:          chats,
		MessagesByChat: make(map[string][]Message),
		DraftsByChat:   make(map[string]string),
	}

	drafts, err := s.ListDrafts(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	snapshot.DraftsByChat = drafts

	if len(chats) == 0 {
		return snapshot, nil
	}

	snapshot.ActiveChatID = chats[0].ID
	messages, err := s.ListMessages(ctx, snapshot.ActiveChatID, messageLimit)
	if err != nil {
		return Snapshot{}, err
	}
	snapshot.MessagesByChat[snapshot.ActiveChatID] = messages

	return snapshot, nil
}

func (s *Store) ListChats(ctx context.Context) ([]Chat, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			c.id,
			c.title,
			c.unread_count,
			c.pinned,
			c.muted,
			c.last_message_at,
			CASE WHEN d.body IS NOT NULL AND d.body <> '' THEN 1 ELSE 0 END AS has_draft
		FROM chats c
		LEFT JOIN drafts d ON d.chat_id = c.id
		ORDER BY c.pinned DESC, c.last_message_at DESC, c.title ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("list chats: %w", err)
	}
	defer rows.Close()

	var chats []Chat
	for rows.Next() {
		var (
			chat            Chat
			pinned          int
			muted           int
			hasDraft        int
			lastMessageUnix int64
		)

		if err := rows.Scan(
			&chat.ID,
			&chat.Title,
			&chat.Unread,
			&pinned,
			&muted,
			&lastMessageUnix,
			&hasDraft,
		); err != nil {
			return nil, fmt.Errorf("scan chat: %w", err)
		}

		chat.Pinned = pinned == 1
		chat.Muted = muted == 1
		chat.HasDraft = hasDraft == 1
		if lastMessageUnix > 0 {
			chat.LastMessageAt = time.Unix(lastMessageUnix, 0)
		}

		chats = append(chats, chat)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate chats: %w", err)
	}

	return chats, nil
}

func (s *Store) ListMessages(ctx context.Context, chatID string, limit int) ([]Message, error) {
	if chatID == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = defaultMessageWindow
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT id, chat_id, sender, body, timestamp_unix, is_outgoing
		FROM (
			SELECT id, chat_id, sender, body, timestamp_unix, is_outgoing
			FROM messages
			WHERE chat_id = ?
			ORDER BY timestamp_unix DESC, id DESC
			LIMIT ?
		)
		ORDER BY timestamp_unix ASC, id ASC
	`, chatID, limit)
	if err != nil {
		return nil, fmt.Errorf("list messages for %s: %w", chatID, err)
	}
	defer rows.Close()

	var messages []Message
	for rows.Next() {
		var (
			message       Message
			timestampUnix int64
			isOutgoing    int
		)
		if err := rows.Scan(
			&message.ID,
			&message.ChatID,
			&message.Sender,
			&message.Body,
			&timestampUnix,
			&isOutgoing,
		); err != nil {
			return nil, fmt.Errorf("scan message: %w", err)
		}

		message.Timestamp = time.Unix(timestampUnix, 0)
		message.IsOutgoing = isOutgoing == 1
		messages = append(messages, message)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate messages: %w", err)
	}

	return messages, nil
}

func (s *Store) SearchChats(ctx context.Context, query string, limit int) ([]Chat, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return s.ListChats(ctx)
	}
	if limit <= 0 {
		limit = 100
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT
			c.id,
			c.title,
			c.unread_count,
			c.pinned,
			c.muted,
			c.last_message_at,
			CASE WHEN d.body IS NOT NULL AND d.body <> '' THEN 1 ELSE 0 END AS has_draft
		FROM chats c
		LEFT JOIN drafts d ON d.chat_id = c.id
		WHERE lower(c.title) LIKE '%' || lower(?) || '%' ESCAPE '\'
		ORDER BY c.pinned DESC, c.last_message_at DESC, c.title ASC
		LIMIT ?
	`, escapeLike(query), limit)
	if err != nil {
		return nil, fmt.Errorf("search chats: %w", err)
	}
	defer rows.Close()

	var chats []Chat
	for rows.Next() {
		var (
			chat            Chat
			pinned          int
			muted           int
			hasDraft        int
			lastMessageUnix int64
		)
		if err := rows.Scan(
			&chat.ID,
			&chat.Title,
			&chat.Unread,
			&pinned,
			&muted,
			&lastMessageUnix,
			&hasDraft,
		); err != nil {
			return nil, fmt.Errorf("scan searched chat: %w", err)
		}
		chat.Pinned = pinned == 1
		chat.Muted = muted == 1
		chat.HasDraft = hasDraft == 1
		if lastMessageUnix > 0 {
			chat.LastMessageAt = time.Unix(lastMessageUnix, 0)
		}
		chats = append(chats, chat)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate searched chats: %w", err)
	}

	return chats, nil
}

func (s *Store) SearchMessages(ctx context.Context, chatID, query string, limit int) ([]Message, error) {
	if strings.TrimSpace(chatID) == "" {
		return nil, nil
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return s.ListMessages(ctx, chatID, limit)
	}
	if limit <= 0 {
		limit = defaultMessageWindow
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT m.id, m.chat_id, m.sender, m.body, m.timestamp_unix, m.is_outgoing
		FROM message_fts
		JOIN messages m ON m.id = message_fts.message_id
		WHERE message_fts.chat_id = ?
			AND message_fts MATCH ?
		ORDER BY m.timestamp_unix ASC, m.id ASC
		LIMIT ?
	`, chatID, quoteFTSQuery(query), limit)
	if err != nil {
		return nil, fmt.Errorf("search messages for %s: %w", chatID, err)
	}
	defer rows.Close()

	var messages []Message
	for rows.Next() {
		var (
			message       Message
			timestampUnix int64
			isOutgoing    int
		)
		if err := rows.Scan(
			&message.ID,
			&message.ChatID,
			&message.Sender,
			&message.Body,
			&timestampUnix,
			&isOutgoing,
		); err != nil {
			return nil, fmt.Errorf("scan searched message: %w", err)
		}
		message.Timestamp = time.Unix(timestampUnix, 0)
		message.IsOutgoing = isOutgoing == 1
		messages = append(messages, message)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate searched messages: %w", err)
	}

	return messages, nil
}

func (s *Store) UpsertChat(ctx context.Context, chat Chat) error {
	if strings.TrimSpace(chat.ID) == "" {
		return fmt.Errorf("chat id is required")
	}
	if strings.TrimSpace(chat.Title) == "" {
		return fmt.Errorf("chat title is required")
	}

	now := time.Now().Unix()
	lastMessageAt := chat.LastMessageAt.Unix()
	if chat.LastMessageAt.IsZero() {
		lastMessageAt = 0
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO chats (
			id, title, unread_count, pinned, muted, last_message_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			title = excluded.title,
			unread_count = excluded.unread_count,
			pinned = excluded.pinned,
			muted = excluded.muted,
			last_message_at = CASE
				WHEN excluded.last_message_at > chats.last_message_at THEN excluded.last_message_at
				ELSE chats.last_message_at
			END,
			updated_at = excluded.updated_at
	`,
		chat.ID,
		chat.Title,
		chat.Unread,
		boolToInt(chat.Pinned),
		boolToInt(chat.Muted),
		lastMessageAt,
		now,
		now,
	)
	if err != nil {
		return fmt.Errorf("upsert chat %s: %w", chat.ID, err)
	}

	return nil
}

func (s *Store) AddMessage(ctx context.Context, message Message) error {
	if strings.TrimSpace(message.ID) == "" {
		return fmt.Errorf("message id is required")
	}
	if strings.TrimSpace(message.ChatID) == "" {
		return fmt.Errorf("chat id is required")
	}
	if strings.TrimSpace(message.Sender) == "" {
		return fmt.Errorf("sender is required")
	}
	if message.Timestamp.IsZero() {
		message.Timestamp = time.Now()
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin add message: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO messages (id, chat_id, sender, body, timestamp_unix, is_outgoing)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			sender = excluded.sender,
			body = excluded.body,
			timestamp_unix = excluded.timestamp_unix,
			is_outgoing = excluded.is_outgoing
	`,
		message.ID,
		message.ChatID,
		message.Sender,
		message.Body,
		message.Timestamp.Unix(),
		boolToInt(message.IsOutgoing),
	); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("insert message %s: %w", message.ID, err)
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM message_fts WHERE message_id = ?`, message.ID); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("clear fts for message %s: %w", message.ID, err)
	}

	if _, err := tx.ExecContext(
		ctx,
		`INSERT INTO message_fts (message_id, chat_id, body) VALUES (?, ?, ?)`,
		message.ID,
		message.ChatID,
		message.Body,
	); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("index message %s: %w", message.ID, err)
	}

	result, err := tx.ExecContext(
		ctx,
		`UPDATE chats
		SET last_message_at = CASE
				WHEN ? > last_message_at THEN ?
				ELSE last_message_at
			END,
			updated_at = ?
		WHERE id = ?`,
		message.Timestamp.Unix(),
		message.Timestamp.Unix(),
		time.Now().Unix(),
		message.ChatID,
	)
	if err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("touch chat %s: %w", message.ChatID, err)
	}

	if rows, _ := result.RowsAffected(); rows == 0 {
		_ = tx.Rollback()
		return fmt.Errorf("chat %s does not exist", message.ChatID)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit add message: %w", err)
	}

	return nil
}

func (s *Store) SaveDraft(ctx context.Context, chatID, body string) error {
	if strings.TrimSpace(chatID) == "" {
		return fmt.Errorf("chat id is required")
	}

	trimmed := strings.TrimSpace(body)
	if trimmed == "" {
		if _, err := s.db.ExecContext(ctx, `DELETE FROM drafts WHERE chat_id = ?`, chatID); err != nil {
			return fmt.Errorf("delete draft for %s: %w", chatID, err)
		}
		return nil
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO drafts (chat_id, body, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT(chat_id) DO UPDATE SET
			body = excluded.body,
			updated_at = excluded.updated_at
	`, chatID, body, time.Now().Unix())
	if err != nil {
		return fmt.Errorf("save draft for %s: %w", chatID, err)
	}

	return nil
}

func (s *Store) ListDrafts(ctx context.Context) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT chat_id, body FROM drafts WHERE body <> ''`)
	if err != nil {
		return nil, fmt.Errorf("list drafts: %w", err)
	}
	defer rows.Close()

	drafts := make(map[string]string)
	for rows.Next() {
		var chatID, body string
		if err := rows.Scan(&chatID, &body); err != nil {
			return nil, fmt.Errorf("scan draft: %w", err)
		}
		drafts[chatID] = body
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate drafts: %w", err)
	}

	return drafts, nil
}

func (s *Store) Draft(ctx context.Context, chatID string) (string, error) {
	var body string
	err := s.db.QueryRowContext(ctx, `SELECT body FROM drafts WHERE chat_id = ?`, chatID).Scan(&body)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", nil
		}
		return "", fmt.Errorf("load draft for %s: %w", chatID, err)
	}

	return body, nil
}

func (s *Store) SetSyncCursor(ctx context.Context, name, value string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("cursor name is required")
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO sync_cursors (name, value, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET
			value = excluded.value,
			updated_at = excluded.updated_at
	`, name, value, time.Now().Unix())
	if err != nil {
		return fmt.Errorf("set sync cursor %s: %w", name, err)
	}

	return nil
}

func (s *Store) SyncCursor(ctx context.Context, name string) (string, error) {
	var value string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM sync_cursors WHERE name = ?`, name).Scan(&value)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", nil
		}
		return "", fmt.Errorf("load sync cursor %s: %w", name, err)
	}

	return value, nil
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func escapeLike(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return replacer.Replace(value)
}

func quoteFTSQuery(value string) string {
	terms := strings.Fields(value)
	if len(terms) == 0 {
		return `""`
	}

	quoted := make([]string, 0, len(terms))
	for _, term := range terms {
		term = strings.ReplaceAll(term, `"`, `""`)
		quoted = append(quoted, `"`+term+`"`)
	}
	return strings.Join(quoted, " AND ")
}
