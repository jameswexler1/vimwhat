package store

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
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
			c.jid,
			c.title,
			c.kind,
			c.unread_count,
			c.pinned,
			c.muted,
			c.last_message_at,
			COALESCE((
				SELECT CASE
					WHEN m.body <> '' THEN m.body
					ELSE COALESCE((
						SELECT mm.file_name
						FROM media_metadata mm
						WHERE mm.message_id = m.id
						ORDER BY mm.file_name ASC
						LIMIT 1
					), '')
				END
				FROM messages m
				WHERE m.chat_id = c.id
					AND m.deleted_at = 0
				ORDER BY m.timestamp_unix DESC, m.id DESC
				LIMIT 1
			), '') AS last_preview,
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
		chat, err := scanChat(rows)
		if err != nil {
			return nil, fmt.Errorf("scan chat: %w", err)
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
		SELECT id, remote_id, chat_id, chat_jid, sender, sender_jid, body,
			timestamp_unix, is_outgoing, status, quoted_message_id, quoted_remote_id,
			deleted_at, deleted_reason
		FROM (
			SELECT id, remote_id, chat_id, chat_jid, sender, sender_jid, body,
				timestamp_unix, is_outgoing, status, quoted_message_id, quoted_remote_id,
				deleted_at, deleted_reason
			FROM messages
			WHERE chat_id = ?
				AND deleted_at = 0
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
		message, err := scanMessage(rows)
		if err != nil {
			return nil, fmt.Errorf("scan message: %w", err)
		}
		messages = append(messages, message)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate messages: %w", err)
	}

	return s.attachMediaMetadata(ctx, messages)
}

func (s *Store) ListMessagesBefore(ctx context.Context, chatID string, before Message, limit int) ([]Message, error) {
	if chatID == "" {
		return nil, nil
	}
	if before.Timestamp.IsZero() || strings.TrimSpace(before.ID) == "" {
		return nil, fmt.Errorf("message anchor is required")
	}
	if limit <= 0 {
		limit = defaultMessageWindow
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT id, remote_id, chat_id, chat_jid, sender, sender_jid, body,
			timestamp_unix, is_outgoing, status, quoted_message_id, quoted_remote_id,
			deleted_at, deleted_reason
		FROM (
			SELECT id, remote_id, chat_id, chat_jid, sender, sender_jid, body,
				timestamp_unix, is_outgoing, status, quoted_message_id, quoted_remote_id,
				deleted_at, deleted_reason
			FROM messages
			WHERE chat_id = ?
				AND deleted_at = 0
				AND (
					timestamp_unix < ?
					OR (timestamp_unix = ? AND id < ?)
				)
			ORDER BY timestamp_unix DESC, id DESC
			LIMIT ?
		)
		ORDER BY timestamp_unix ASC, id ASC
	`, chatID, before.Timestamp.Unix(), before.Timestamp.Unix(), before.ID, limit)
	if err != nil {
		return nil, fmt.Errorf("list messages before %s for %s: %w", before.ID, chatID, err)
	}
	defer rows.Close()

	var messages []Message
	for rows.Next() {
		message, err := scanMessage(rows)
		if err != nil {
			return nil, fmt.Errorf("scan older message: %w", err)
		}
		messages = append(messages, message)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate older messages: %w", err)
	}

	return s.attachMediaMetadata(ctx, messages)
}

func (s *Store) OldestMessage(ctx context.Context, chatID string) (Message, bool, error) {
	if chatID == "" {
		return Message{}, false, nil
	}

	row := s.db.QueryRowContext(ctx, `
		SELECT id, remote_id, chat_id, chat_jid, sender, sender_jid, body,
			timestamp_unix, is_outgoing, status, quoted_message_id, quoted_remote_id,
			deleted_at, deleted_reason
		FROM messages
		WHERE chat_id = ?
			AND deleted_at = 0
		ORDER BY timestamp_unix ASC, id ASC
		LIMIT 1
	`, chatID)

	message, err := scanMessage(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return Message{}, false, nil
		}
		return Message{}, false, fmt.Errorf("load oldest message for %s: %w", chatID, err)
	}
	return message, true, nil
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
			c.jid,
			c.title,
			c.kind,
			c.unread_count,
			c.pinned,
			c.muted,
			c.last_message_at,
			COALESCE((
				SELECT CASE
					WHEN m.body <> '' THEN m.body
					ELSE COALESCE((
						SELECT mm.file_name
						FROM media_metadata mm
						WHERE mm.message_id = m.id
						ORDER BY mm.file_name ASC
						LIMIT 1
					), '')
				END
				FROM messages m
				WHERE m.chat_id = c.id
					AND m.deleted_at = 0
				ORDER BY m.timestamp_unix DESC, m.id DESC
				LIMIT 1
			), '') AS last_preview,
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
		chat, err := scanChat(rows)
		if err != nil {
			return nil, fmt.Errorf("scan searched chat: %w", err)
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
		SELECT m.id, m.remote_id, m.chat_id, m.chat_jid, m.sender, m.sender_jid, m.body,
			m.timestamp_unix, m.is_outgoing, m.status, m.quoted_message_id, m.quoted_remote_id,
			m.deleted_at, m.deleted_reason
		FROM message_fts
		JOIN messages m ON m.id = message_fts.message_id
		WHERE message_fts.chat_id = ?
			AND m.deleted_at = 0
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
		message, err := scanMessage(rows)
		if err != nil {
			return nil, fmt.Errorf("scan searched message: %w", err)
		}
		messages = append(messages, message)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate searched messages: %w", err)
	}

	return s.attachMediaMetadata(ctx, messages)
}

func (s *Store) UpsertChat(ctx context.Context, chat Chat) error {
	return s.upsertChat(ctx, chat, false)
}

func (s *Store) UpsertChatPreserveUnread(ctx context.Context, chat Chat) error {
	return s.upsertChat(ctx, chat, true)
}

func (s *Store) upsertChat(ctx context.Context, chat Chat, preserveUnreadOnUpdate bool) error {
	if strings.TrimSpace(chat.ID) == "" {
		return fmt.Errorf("chat id is required")
	}
	if strings.TrimSpace(chat.Title) == "" {
		return fmt.Errorf("chat title is required")
	}
	if strings.TrimSpace(chat.Kind) == "" {
		chat.Kind = "direct"
	}
	if strings.TrimSpace(chat.JID) == "" {
		chat.JID = chat.ID
	}

	now := time.Now().Unix()
	lastMessageAt := chat.LastMessageAt.Unix()
	if chat.LastMessageAt.IsZero() {
		lastMessageAt = 0
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO chats (
			id, jid, title, kind, unread_count, pinned, muted, last_message_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			jid = excluded.jid,
			title = excluded.title,
			kind = excluded.kind,
			unread_count = CASE
				WHEN ? THEN chats.unread_count
				ELSE excluded.unread_count
			END,
			pinned = excluded.pinned,
			muted = excluded.muted,
			last_message_at = CASE
				WHEN excluded.last_message_at > chats.last_message_at THEN excluded.last_message_at
				ELSE chats.last_message_at
			END,
			updated_at = excluded.updated_at
	`,
		chat.ID,
		chat.JID,
		chat.Title,
		chat.Kind,
		chat.Unread,
		boolToInt(chat.Pinned),
		boolToInt(chat.Muted),
		lastMessageAt,
		now,
		now,
		boolToInt(preserveUnreadOnUpdate),
	)
	if err != nil {
		return fmt.Errorf("upsert chat %s: %w", chat.ID, err)
	}

	return nil
}

func (s *Store) AddMessage(ctx context.Context, message Message) error {
	_, err := s.addMessage(ctx, message, false)
	return err
}

func (s *Store) AddMessageWithMedia(ctx context.Context, message Message, mediaItems []MediaMetadata) error {
	if len(mediaItems) > 0 {
		message.Media = slices.Clone(mediaItems)
	}
	_, err := s.addMessage(ctx, message, false)
	return err
}

func (s *Store) AddIncomingMessage(ctx context.Context, message Message) (bool, error) {
	return s.addMessage(ctx, message, !message.IsOutgoing)
}

func (s *Store) AddHistoricalMessage(ctx context.Context, message Message) (bool, error) {
	return s.addMessage(ctx, message, false)
}

func (s *Store) addMessage(ctx context.Context, message Message, incrementUnreadOnNew bool) (bool, error) {
	if strings.TrimSpace(message.ID) == "" {
		return false, fmt.Errorf("message id is required")
	}
	if strings.TrimSpace(message.ChatID) == "" {
		return false, fmt.Errorf("chat id is required")
	}
	if strings.TrimSpace(message.Sender) == "" {
		return false, fmt.Errorf("sender is required")
	}
	if message.Timestamp.IsZero() {
		message.Timestamp = time.Now()
	}
	if strings.TrimSpace(message.Status) == "" && message.IsOutgoing {
		message.Status = "pending"
	}
	if strings.TrimSpace(message.ChatJID) == "" {
		message.ChatJID = message.ChatID
	}
	if strings.TrimSpace(message.SenderJID) == "" {
		message.SenderJID = message.Sender
	}
	if len(message.Media) > 1 {
		return false, fmt.Errorf("only one media attachment per message is supported")
	}
	deletedAt := int64(0)
	if !message.DeletedAt.IsZero() {
		deletedAt = message.DeletedAt.Unix()
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin add message: %w", err)
	}

	var existing int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM messages WHERE id = ?`, message.ID).Scan(&existing); err != nil {
		_ = tx.Rollback()
		return false, fmt.Errorf("check existing message %s: %w", message.ID, err)
	}
	isNew := existing == 0

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO messages (
			id, remote_id, chat_id, chat_jid, sender, sender_jid, body,
			timestamp_unix, is_outgoing, status, quoted_message_id, quoted_remote_id,
			deleted_at, deleted_reason
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			remote_id = excluded.remote_id,
			chat_jid = excluded.chat_jid,
			sender = excluded.sender,
			sender_jid = excluded.sender_jid,
			body = excluded.body,
			timestamp_unix = excluded.timestamp_unix,
			is_outgoing = excluded.is_outgoing,
			status = excluded.status,
			quoted_message_id = excluded.quoted_message_id,
			quoted_remote_id = excluded.quoted_remote_id,
			deleted_at = CASE
				WHEN excluded.deleted_at > 0 THEN excluded.deleted_at
				ELSE messages.deleted_at
			END,
			deleted_reason = CASE
				WHEN excluded.deleted_at > 0 THEN excluded.deleted_reason
				ELSE messages.deleted_reason
			END
	`,
		message.ID,
		message.RemoteID,
		message.ChatID,
		message.ChatJID,
		message.Sender,
		message.SenderJID,
		message.Body,
		message.Timestamp.Unix(),
		boolToInt(message.IsOutgoing),
		message.Status,
		message.QuotedMessageID,
		message.QuotedRemoteID,
		deletedAt,
		message.DeletedReason,
	); err != nil {
		_ = tx.Rollback()
		return false, fmt.Errorf("insert message %s: %w", message.ID, err)
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM message_fts WHERE message_id = ?`, message.ID); err != nil {
		_ = tx.Rollback()
		return false, fmt.Errorf("clear fts for message %s: %w", message.ID, err)
	}

	if _, err := tx.ExecContext(
		ctx,
		`INSERT INTO message_fts (message_id, chat_id, body) VALUES (?, ?, ?)`,
		message.ID,
		message.ChatID,
		message.Body,
	); err != nil {
		_ = tx.Rollback()
		return false, fmt.Errorf("index message %s: %w", message.ID, err)
	}

	for _, media := range message.Media {
		media.MessageID = message.ID
		if err := upsertMediaMetadata(ctx, tx, media); err != nil {
			_ = tx.Rollback()
			return false, err
		}
	}

	unreadDelta := 0
	if isNew && incrementUnreadOnNew {
		unreadDelta = 1
	}
	result, err := tx.ExecContext(
		ctx,
		`UPDATE chats
		SET last_message_at = CASE
				WHEN ? > last_message_at THEN ?
				ELSE last_message_at
			END,
			unread_count = unread_count + ?,
			updated_at = ?
		WHERE id = ?`,
		message.Timestamp.Unix(),
		message.Timestamp.Unix(),
		unreadDelta,
		time.Now().Unix(),
		message.ChatID,
	)
	if err != nil {
		_ = tx.Rollback()
		return false, fmt.Errorf("touch chat %s: %w", message.ChatID, err)
	}

	if rows, _ := result.RowsAffected(); rows == 0 {
		_ = tx.Rollback()
		return false, fmt.Errorf("chat %s does not exist", message.ChatID)
	}

	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit add message: %w", err)
	}

	return isNew, nil
}

func (s *Store) UpdateMessageStatus(ctx context.Context, messageID, status string) error {
	updated, err := s.UpdateMessageStatusIfExists(ctx, messageID, status)
	if err != nil {
		return err
	}
	if !updated {
		return fmt.Errorf("message %s does not exist", messageID)
	}
	return nil
}

func (s *Store) UpdateMessageStatusIfExists(ctx context.Context, messageID, status string) (bool, error) {
	if strings.TrimSpace(messageID) == "" {
		return false, fmt.Errorf("message id is required")
	}
	if strings.TrimSpace(status) == "" {
		return false, fmt.Errorf("message status is required")
	}

	result, err := s.db.ExecContext(ctx, `
		UPDATE messages
		SET status = ?
		WHERE id = ?
	`, status, messageID)
	if err != nil {
		return false, fmt.Errorf("update message status %s: %w", messageID, err)
	}

	rows, _ := result.RowsAffected()
	return rows > 0, nil
}

func (s *Store) DeleteMessage(ctx context.Context, messageID string) error {
	if strings.TrimSpace(messageID) == "" {
		return fmt.Errorf("message id is required")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin delete message: %w", err)
	}

	var chatID string
	err = tx.QueryRowContext(ctx, `
		SELECT chat_id
		FROM messages
		WHERE id = ?
			AND deleted_at = 0
	`, messageID).Scan(&chatID)
	if err != nil {
		_ = tx.Rollback()
		if err == sql.ErrNoRows {
			return fmt.Errorf("message %s does not exist", messageID)
		}
		return fmt.Errorf("load message %s for delete: %w", messageID, err)
	}

	now := time.Now().Unix()
	result, err := tx.ExecContext(ctx, `
		UPDATE messages
		SET body = '',
			deleted_at = ?,
			deleted_reason = 'local'
		WHERE id = ?
			AND deleted_at = 0
	`, now, messageID)
	if err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("delete message %s: %w", messageID, err)
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		_ = tx.Rollback()
		return fmt.Errorf("message %s does not exist", messageID)
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM message_fts WHERE message_id = ?`, messageID); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("clear fts for deleted message %s: %w", messageID, err)
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE chats
		SET last_message_at = COALESCE((
				SELECT MAX(timestamp_unix)
				FROM messages
				WHERE chat_id = ?
					AND deleted_at = 0
			), 0),
			updated_at = ?
		WHERE id = ?
	`, chatID, now, chatID); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("refresh chat %s after delete: %w", chatID, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit delete message: %w", err)
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

func (s *Store) UpsertContact(ctx context.Context, contact Contact) error {
	if strings.TrimSpace(contact.JID) == "" {
		return fmt.Errorf("contact jid is required")
	}
	if contact.UpdatedAt.IsZero() {
		contact.UpdatedAt = time.Now()
	}
	avatarUpdatedUnix := int64(0)
	if !contact.AvatarUpdatedAt.IsZero() {
		avatarUpdatedUnix = contact.AvatarUpdatedAt.Unix()
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO contacts (
			jid, display_name, notify_name, phone, avatar_path,
			avatar_thumb_path, avatar_updated_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(jid) DO UPDATE SET
			display_name = excluded.display_name,
			notify_name = excluded.notify_name,
			phone = excluded.phone,
			avatar_path = excluded.avatar_path,
			avatar_thumb_path = excluded.avatar_thumb_path,
			avatar_updated_at = excluded.avatar_updated_at,
			updated_at = excluded.updated_at
	`,
		contact.JID,
		contact.DisplayName,
		contact.NotifyName,
		contact.Phone,
		contact.AvatarPath,
		contact.AvatarThumbPath,
		avatarUpdatedUnix,
		contact.UpdatedAt.Unix(),
	)
	if err != nil {
		return fmt.Errorf("upsert contact %s: %w", contact.JID, err)
	}

	return nil
}

func (s *Store) Contact(ctx context.Context, jid string) (Contact, error) {
	var (
		contact           Contact
		avatarUpdatedUnix int64
		updatedUnix       int64
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT jid, display_name, notify_name, phone, avatar_path,
			avatar_thumb_path, avatar_updated_at, updated_at
		FROM contacts
		WHERE jid = ?
	`, jid).Scan(
		&contact.JID,
		&contact.DisplayName,
		&contact.NotifyName,
		&contact.Phone,
		&contact.AvatarPath,
		&contact.AvatarThumbPath,
		&avatarUpdatedUnix,
		&updatedUnix,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return Contact{}, nil
		}
		return Contact{}, fmt.Errorf("load contact %s: %w", jid, err)
	}
	if avatarUpdatedUnix > 0 {
		contact.AvatarUpdatedAt = time.Unix(avatarUpdatedUnix, 0)
	}
	contact.UpdatedAt = time.Unix(updatedUnix, 0)

	return contact, nil
}

func (s *Store) UpsertMediaMetadata(ctx context.Context, media MediaMetadata) error {
	if err := upsertMediaMetadata(ctx, s.db, media); err != nil {
		return err
	}

	return nil
}

type mediaMetadataExecer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func upsertMediaMetadata(ctx context.Context, execer mediaMetadataExecer, media MediaMetadata) error {
	if strings.TrimSpace(media.MessageID) == "" {
		return fmt.Errorf("message id is required")
	}
	if media.UpdatedAt.IsZero() {
		media.UpdatedAt = time.Now()
	}

	_, err := execer.ExecContext(ctx, `
		INSERT INTO media_metadata (
			message_id, mime_type, file_name, size_bytes, local_path,
			thumbnail_path, download_state, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(message_id) DO UPDATE SET
			mime_type = CASE
				WHEN excluded.mime_type != '' THEN excluded.mime_type
				ELSE media_metadata.mime_type
			END,
			file_name = CASE
				WHEN excluded.file_name != '' THEN excluded.file_name
				ELSE media_metadata.file_name
			END,
			size_bytes = CASE
				WHEN excluded.size_bytes > 0 THEN excluded.size_bytes
				ELSE media_metadata.size_bytes
			END,
			local_path = CASE
				WHEN excluded.local_path != '' THEN excluded.local_path
				ELSE media_metadata.local_path
			END,
			thumbnail_path = CASE
				WHEN excluded.thumbnail_path != '' THEN excluded.thumbnail_path
				ELSE media_metadata.thumbnail_path
			END,
			download_state = CASE
				WHEN media_metadata.local_path != '' AND excluded.local_path = '' THEN media_metadata.download_state
				WHEN excluded.download_state != '' THEN excluded.download_state
				ELSE media_metadata.download_state
			END,
			updated_at = excluded.updated_at
	`,
		media.MessageID,
		media.MIMEType,
		media.FileName,
		media.SizeBytes,
		media.LocalPath,
		media.ThumbnailPath,
		media.DownloadState,
		media.UpdatedAt.Unix(),
	)
	if err != nil {
		return fmt.Errorf("upsert media metadata for %s: %w", media.MessageID, err)
	}

	return nil
}

func (s *Store) MediaMetadata(ctx context.Context, messageID string) (MediaMetadata, error) {
	var (
		media       MediaMetadata
		updatedUnix int64
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT message_id, mime_type, file_name, size_bytes, local_path,
			thumbnail_path, download_state, updated_at
		FROM media_metadata
		WHERE message_id = ?
	`, messageID).Scan(
		&media.MessageID,
		&media.MIMEType,
		&media.FileName,
		&media.SizeBytes,
		&media.LocalPath,
		&media.ThumbnailPath,
		&media.DownloadState,
		&updatedUnix,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return MediaMetadata{}, nil
		}
		return MediaMetadata{}, fmt.Errorf("load media metadata for %s: %w", messageID, err)
	}
	media.UpdatedAt = time.Unix(updatedUnix, 0)

	return media, nil
}

func (s *Store) attachMediaMetadata(ctx context.Context, messages []Message) ([]Message, error) {
	if len(messages) == 0 {
		return messages, nil
	}

	ids := make([]string, 0, len(messages))
	seen := make(map[string]struct{}, len(messages))
	for _, message := range messages {
		if message.ID == "" {
			continue
		}
		if _, ok := seen[message.ID]; ok {
			continue
		}
		seen[message.ID] = struct{}{}
		ids = append(ids, message.ID)
	}
	if len(ids) == 0 {
		return messages, nil
	}

	placeholders := strings.TrimRight(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, 0, len(ids))
	for _, id := range ids {
		args = append(args, id)
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT message_id, mime_type, file_name, size_bytes, local_path,
			thumbnail_path, download_state, updated_at
		FROM media_metadata
		WHERE message_id IN (`+placeholders+`)
		ORDER BY file_name ASC
	`, args...)
	if err != nil {
		return nil, fmt.Errorf("list media metadata: %w", err)
	}
	defer rows.Close()

	byMessage := make(map[string][]MediaMetadata, len(messages))
	for rows.Next() {
		var (
			media       MediaMetadata
			updatedUnix int64
		)
		if err := rows.Scan(
			&media.MessageID,
			&media.MIMEType,
			&media.FileName,
			&media.SizeBytes,
			&media.LocalPath,
			&media.ThumbnailPath,
			&media.DownloadState,
			&updatedUnix,
		); err != nil {
			return nil, fmt.Errorf("scan media metadata: %w", err)
		}
		media.UpdatedAt = time.Unix(updatedUnix, 0)
		byMessage[media.MessageID] = append(byMessage[media.MessageID], media)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate media metadata: %w", err)
	}

	for i := range messages {
		messages[i].Media = byMessage[messages[i].ID]
	}

	return messages, nil
}

func (s *Store) SaveUISnapshot(ctx context.Context, snapshot UISnapshot) error {
	if strings.TrimSpace(snapshot.Kind) == "" {
		return fmt.Errorf("snapshot kind is required")
	}
	if strings.TrimSpace(snapshot.Name) == "" {
		return fmt.Errorf("snapshot name is required")
	}
	if snapshot.UpdatedAt.IsZero() {
		snapshot.UpdatedAt = time.Now()
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO ui_snapshots (kind, name, chat_id, value, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(kind, name, chat_id) DO UPDATE SET
			value = excluded.value,
			updated_at = excluded.updated_at
	`,
		snapshot.Kind,
		snapshot.Name,
		snapshot.ChatID,
		snapshot.Value,
		snapshot.UpdatedAt.Unix(),
	)
	if err != nil {
		return fmt.Errorf("save ui snapshot %s/%s: %w", snapshot.Kind, snapshot.Name, err)
	}

	return nil
}

func (s *Store) UISnapshot(ctx context.Context, kind, name, chatID string) (UISnapshot, error) {
	var (
		snapshot    UISnapshot
		updatedUnix int64
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT kind, name, chat_id, value, updated_at
		FROM ui_snapshots
		WHERE kind = ? AND name = ? AND chat_id = ?
	`, kind, name, chatID).Scan(
		&snapshot.Kind,
		&snapshot.Name,
		&snapshot.ChatID,
		&snapshot.Value,
		&updatedUnix,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return UISnapshot{}, nil
		}
		return UISnapshot{}, fmt.Errorf("load ui snapshot %s/%s: %w", kind, name, err)
	}
	snapshot.UpdatedAt = time.Unix(updatedUnix, 0)

	return snapshot, nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanChat(row scanner) (Chat, error) {
	var (
		chat            Chat
		pinned          int
		muted           int
		hasDraft        int
		lastMessageUnix int64
	)
	if err := row.Scan(
		&chat.ID,
		&chat.JID,
		&chat.Title,
		&chat.Kind,
		&chat.Unread,
		&pinned,
		&muted,
		&lastMessageUnix,
		&chat.LastPreview,
		&hasDraft,
	); err != nil {
		return Chat{}, err
	}

	chat.Pinned = pinned == 1
	chat.Muted = muted == 1
	chat.HasDraft = hasDraft == 1
	if lastMessageUnix > 0 {
		chat.LastMessageAt = time.Unix(lastMessageUnix, 0)
	}
	if chat.Kind == "" {
		chat.Kind = "direct"
	}
	if chat.JID == "" {
		chat.JID = chat.ID
	}

	return chat, nil
}

func scanMessage(row scanner) (Message, error) {
	var (
		message       Message
		timestampUnix int64
		isOutgoing    int
		deletedUnix   int64
	)
	if err := row.Scan(
		&message.ID,
		&message.RemoteID,
		&message.ChatID,
		&message.ChatJID,
		&message.Sender,
		&message.SenderJID,
		&message.Body,
		&timestampUnix,
		&isOutgoing,
		&message.Status,
		&message.QuotedMessageID,
		&message.QuotedRemoteID,
		&deletedUnix,
		&message.DeletedReason,
	); err != nil {
		return Message{}, err
	}

	message.Timestamp = time.Unix(timestampUnix, 0)
	if deletedUnix > 0 {
		message.DeletedAt = time.Unix(deletedUnix, 0)
	}
	message.IsOutgoing = isOutgoing == 1
	if message.ChatJID == "" {
		message.ChatJID = message.ChatID
	}
	if message.SenderJID == "" {
		message.SenderJID = message.Sender
	}

	return message, nil
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
