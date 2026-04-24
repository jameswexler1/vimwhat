package store

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strings"
	"time"
)

const (
	defaultMessageWindow      = 200
	renderableMessageWhereSQL = `(trim(m.body) <> '' OR EXISTS (
					SELECT 1
					FROM media_metadata visible_mm
					WHERE visible_mm.message_id = m.id
				))`
	chatLastPreviewSQL = `COALESCE((
					SELECT CASE
						WHEN trim(m.body) <> '' THEN m.body
						ELSE COALESCE((
							SELECT CASE
								WHEN trim(mm.media_kind) = 'sticker' AND trim(mm.accessibility_label) <> '' THEN 'Sticker: ' || mm.accessibility_label
								WHEN trim(mm.media_kind) = 'sticker' THEN 'Sticker'
								ELSE COALESCE(NULLIF(mm.file_name, ''), NULLIF(mm.mime_type, ''), 'media')
							END
							FROM media_metadata mm
							WHERE mm.message_id = m.id
							ORDER BY mm.file_name ASC
							LIMIT 1
						), '')
				END
					FROM messages m
					WHERE m.chat_id = c.id
						AND m.deleted_at = 0
						AND ` + renderableMessageWhereSQL + `
					ORDER BY m.timestamp_unix DESC, m.id DESC
					LIMIT 1
				), '') AS last_preview`
)

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
			c.title_source,
			c.kind,
			c.avatar_id,
			c.avatar_path,
			c.avatar_thumb_path,
			c.avatar_updated_at,
			c.unread_count,
			c.pinned,
			c.muted,
			c.last_message_at,
			`+chatLastPreviewSQL+`,
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

func (s *Store) ChatByJID(ctx context.Context, jid string) (Chat, bool, error) {
	jid = strings.TrimSpace(jid)
	if jid == "" {
		return Chat{}, false, nil
	}

	row := s.db.QueryRowContext(ctx, `
		SELECT
			c.id,
			c.jid,
			c.title,
			c.title_source,
			c.kind,
			c.avatar_id,
			c.avatar_path,
			c.avatar_thumb_path,
			c.avatar_updated_at,
			c.unread_count,
			c.pinned,
			c.muted,
			c.last_message_at,
			`+chatLastPreviewSQL+`,
			CASE WHEN d.body IS NOT NULL AND d.body <> '' THEN 1 ELSE 0 END AS has_draft
		FROM chats c
		LEFT JOIN drafts d ON d.chat_id = c.id
		WHERE c.jid = ?
	`, jid)

	chat, err := scanChat(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return Chat{}, false, nil
		}
		return Chat{}, false, fmt.Errorf("load chat for jid %s: %w", jid, err)
	}
	return chat, true, nil
}

func (s *Store) ChatByID(ctx context.Context, id string) (Chat, bool, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return Chat{}, false, nil
	}

	row := s.db.QueryRowContext(ctx, `
		SELECT
			c.id,
			c.jid,
			c.title,
			c.title_source,
			c.kind,
			c.avatar_id,
			c.avatar_path,
			c.avatar_thumb_path,
			c.avatar_updated_at,
			c.unread_count,
			c.pinned,
			c.muted,
			c.last_message_at,
			`+chatLastPreviewSQL+`,
			CASE WHEN d.body IS NOT NULL AND d.body <> '' THEN 1 ELSE 0 END AS has_draft
		FROM chats c
		LEFT JOIN drafts d ON d.chat_id = c.id
		WHERE c.id = ?
	`, id)

	chat, err := scanChat(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return Chat{}, false, nil
		}
		return Chat{}, false, fmt.Errorf("load chat %s: %w", id, err)
	}
	return chat, true, nil
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
				FROM messages m
				WHERE m.chat_id = ?
					AND m.deleted_at = 0
					AND `+renderableMessageWhereSQL+`
				ORDER BY m.timestamp_unix DESC, m.id DESC
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

	return s.attachMessageDetails(ctx, messages)
}

func (s *Store) MessageByID(ctx context.Context, id string) (Message, bool, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return Message{}, false, nil
	}

	row := s.db.QueryRowContext(ctx, `
		SELECT id, remote_id, chat_id, chat_jid, sender, sender_jid, body,
			timestamp_unix, is_outgoing, status, quoted_message_id, quoted_remote_id,
			deleted_at, deleted_reason
		FROM messages m
		WHERE m.id = ?
			AND m.deleted_at = 0
			AND `+renderableMessageWhereSQL+`
	`, id)

	message, err := scanMessage(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return Message{}, false, nil
		}
		return Message{}, false, fmt.Errorf("load message %s: %w", id, err)
	}

	messages, err := s.attachMessageDetails(ctx, []Message{message})
	if err != nil {
		return Message{}, false, err
	}
	return messages[0], true, nil
}

func (s *Store) ListAllMessages(ctx context.Context, chatID string) ([]Message, error) {
	if chatID == "" {
		return nil, nil
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT id, remote_id, chat_id, chat_jid, sender, sender_jid, body,
			timestamp_unix, is_outgoing, status, quoted_message_id, quoted_remote_id,
			deleted_at, deleted_reason
		FROM messages m
		WHERE m.chat_id = ?
			AND m.deleted_at = 0
			AND `+renderableMessageWhereSQL+`
		ORDER BY m.timestamp_unix ASC, m.id ASC
	`, chatID)
	if err != nil {
		return nil, fmt.Errorf("list all messages for %s: %w", chatID, err)
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

	return s.attachMessageDetails(ctx, messages)
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
				FROM messages m
				WHERE m.chat_id = ?
					AND m.deleted_at = 0
					AND `+renderableMessageWhereSQL+`
					AND (
						m.timestamp_unix < ?
						OR (m.timestamp_unix = ? AND m.id < ?)
					)
				ORDER BY m.timestamp_unix DESC, m.id DESC
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

	return s.attachMessageDetails(ctx, messages)
}

func (s *Store) OldestMessage(ctx context.Context, chatID string) (Message, bool, error) {
	if chatID == "" {
		return Message{}, false, nil
	}

	row := s.db.QueryRowContext(ctx, `
		SELECT id, remote_id, chat_id, chat_jid, sender, sender_jid, body,
			timestamp_unix, is_outgoing, status, quoted_message_id, quoted_remote_id,
			deleted_at, deleted_reason
			FROM messages m
			WHERE m.chat_id = ?
				AND m.deleted_at = 0
				AND `+renderableMessageWhereSQL+`
			ORDER BY m.timestamp_unix ASC, m.id ASC
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
			c.title_source,
			c.kind,
			c.avatar_id,
			c.avatar_path,
			c.avatar_thumb_path,
			c.avatar_updated_at,
			c.unread_count,
			c.pinned,
			c.muted,
			c.last_message_at,
			`+chatLastPreviewSQL+`,
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
				AND `+renderableMessageWhereSQL+`
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

	return s.attachMessageDetails(ctx, messages)
}

func (s *Store) UpsertChat(ctx context.Context, chat Chat) error {
	return s.upsertChat(ctx, chat, false)
}

func (s *Store) UpsertChatPreserveUnread(ctx context.Context, chat Chat) error {
	return s.upsertChat(ctx, chat, true)
}

func (s *Store) UpdateChatTitleIfExists(ctx context.Context, chat Chat) (bool, error) {
	if strings.TrimSpace(chat.ID) == "" {
		return false, fmt.Errorf("chat id is required")
	}
	if strings.TrimSpace(chat.Title) == "" {
		return false, fmt.Errorf("chat title is required")
	}
	existing, ok, err := s.chatTitleState(ctx, chat.ID)
	if err != nil || !ok {
		return false, err
	}
	if strings.TrimSpace(chat.Kind) == "" {
		chat.Kind = existing.Kind
	}
	if strings.TrimSpace(chat.JID) == "" {
		chat.JID = existing.JID
	}
	chat.TitleSource = NormalizeChatTitleSource(chat.TitleSource)
	if !shouldReplaceChatTitle(existing, chat) {
		return false, nil
	}
	now := time.Now().Unix()
	if _, err := s.db.ExecContext(ctx, `
		UPDATE chats
		SET title = ?, title_source = ?, updated_at = ?
		WHERE id = ?
	`, chat.Title, chat.TitleSource, now, chat.ID); err != nil {
		return false, fmt.Errorf("update chat title %s: %w", chat.ID, err)
	}
	return true, nil
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
	chat.TitleSource = NormalizeChatTitleSource(chat.TitleSource)
	if existing, ok, err := s.chatTitleState(ctx, chat.ID); err != nil {
		return err
	} else if ok && !shouldReplaceChatTitle(existing, chat) {
		chat.Title = existing.Title
		chat.TitleSource = existing.TitleSource
	}

	now := time.Now().Unix()
	lastMessageAt := chat.LastMessageAt.Unix()
	if chat.LastMessageAt.IsZero() {
		lastMessageAt = 0
	}
	avatarUpdatedAt := chat.AvatarUpdatedAt.Unix()
	if chat.AvatarUpdatedAt.IsZero() {
		avatarUpdatedAt = 0
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO chats (
			id, jid, title, title_source, kind, avatar_id, avatar_path, avatar_thumb_path,
			avatar_updated_at, unread_count, pinned, muted, last_message_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			jid = excluded.jid,
			title = excluded.title,
			title_source = excluded.title_source,
			kind = excluded.kind,
			avatar_id = CASE
				WHEN excluded.avatar_id != '' THEN excluded.avatar_id
				ELSE chats.avatar_id
			END,
			avatar_path = CASE
				WHEN excluded.avatar_path != '' THEN excluded.avatar_path
				ELSE chats.avatar_path
			END,
			avatar_thumb_path = CASE
				WHEN excluded.avatar_thumb_path != '' THEN excluded.avatar_thumb_path
				ELSE chats.avatar_thumb_path
			END,
			avatar_updated_at = CASE
				WHEN excluded.avatar_updated_at > 0 THEN excluded.avatar_updated_at
				ELSE chats.avatar_updated_at
			END,
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
		chat.TitleSource,
		chat.Kind,
		chat.AvatarID,
		chat.AvatarPath,
		chat.AvatarThumbPath,
		avatarUpdatedAt,
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

func (s *Store) SetChatAvatar(ctx context.Context, chatID, avatarID, avatarPath, avatarThumbPath string, updatedAt time.Time) error {
	if strings.TrimSpace(chatID) == "" {
		return fmt.Errorf("chat id is required")
	}
	if updatedAt.IsZero() {
		updatedAt = time.Now()
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE chats
		SET avatar_id = ?, avatar_path = ?, avatar_thumb_path = ?, avatar_updated_at = ?, updated_at = ?
		WHERE id = ?
	`, avatarID, avatarPath, avatarThumbPath, updatedAt.Unix(), updatedAt.Unix(), chatID)
	if err != nil {
		return fmt.Errorf("set chat avatar %s: %w", chatID, err)
	}
	return nil
}

func (s *Store) chatTitleState(ctx context.Context, id string) (Chat, bool, error) {
	var chat Chat
	err := s.db.QueryRowContext(ctx, `
		SELECT id, jid, title, title_source, kind
		FROM chats
		WHERE id = ?
	`, id).Scan(&chat.ID, &chat.JID, &chat.Title, &chat.TitleSource, &chat.Kind)
	if err != nil {
		if err == sql.ErrNoRows {
			return Chat{}, false, nil
		}
		return Chat{}, false, fmt.Errorf("load chat title state %s: %w", id, err)
	}
	if chat.JID == "" {
		chat.JID = chat.ID
	}
	if chat.Kind == "" {
		chat.Kind = "direct"
	}
	chat.TitleSource = NormalizeChatTitleSource(chat.TitleSource)
	return chat, true, nil
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

func (s *Store) ClearChatUnread(ctx context.Context, chatID string) error {
	if strings.TrimSpace(chatID) == "" {
		return fmt.Errorf("chat id is required")
	}

	result, err := s.db.ExecContext(ctx, `
		UPDATE chats
		SET unread_count = 0,
			updated_at = ?
		WHERE id = ?
	`, time.Now().Unix(), chatID)
	if err != nil {
		return fmt.Errorf("clear chat unread %s: %w", chatID, err)
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		return fmt.Errorf("chat %s does not exist", chatID)
	}
	return nil
}

func (s *Store) UpsertReaction(ctx context.Context, reaction Reaction) error {
	if strings.TrimSpace(reaction.MessageID) == "" {
		return fmt.Errorf("reaction message id is required")
	}
	if strings.TrimSpace(reaction.SenderJID) == "" {
		return fmt.Errorf("reaction sender jid is required")
	}
	if reaction.UpdatedAt.IsZero() {
		reaction.UpdatedAt = time.Now()
	}
	if reaction.Timestamp.IsZero() {
		reaction.Timestamp = reaction.UpdatedAt
	}
	if strings.TrimSpace(reaction.Emoji) == "" {
		_, err := s.db.ExecContext(ctx, `
			DELETE FROM message_reactions
			WHERE message_id = ?
				AND sender_jid = ?
		`, reaction.MessageID, reaction.SenderJID)
		if err != nil {
			return fmt.Errorf("delete reaction for %s/%s: %w", reaction.MessageID, reaction.SenderJID, err)
		}
		return nil
	}
	var messageExists int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM messages WHERE id = ?`, reaction.MessageID).Scan(&messageExists); err != nil {
		return fmt.Errorf("check reaction target %s: %w", reaction.MessageID, err)
	}
	if messageExists == 0 {
		return nil
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO message_reactions (
			message_id, sender_jid, emoji, timestamp_unix, is_outgoing, updated_at
		) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(message_id, sender_jid) DO UPDATE SET
			emoji = excluded.emoji,
			timestamp_unix = excluded.timestamp_unix,
			is_outgoing = excluded.is_outgoing,
			updated_at = excluded.updated_at
	`, reaction.MessageID, reaction.SenderJID, reaction.Emoji, reaction.Timestamp.Unix(), boolToInt(reaction.IsOutgoing), reaction.UpdatedAt.Unix())
	if err != nil {
		return fmt.Errorf("upsert reaction for %s/%s: %w", reaction.MessageID, reaction.SenderJID, err)
	}
	return nil
}

func (s *Store) ListMessageReactions(ctx context.Context, messageID string) ([]Reaction, error) {
	if strings.TrimSpace(messageID) == "" {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT message_id, sender_jid, emoji, timestamp_unix, is_outgoing, updated_at
		FROM message_reactions
		WHERE message_id = ?
		ORDER BY updated_at ASC, sender_jid ASC
	`, messageID)
	if err != nil {
		return nil, fmt.Errorf("list reactions for %s: %w", messageID, err)
	}
	defer rows.Close()

	var reactions []Reaction
	for rows.Next() {
		reaction, err := scanReaction(rows)
		if err != nil {
			return nil, fmt.Errorf("scan reaction: %w", err)
		}
		reactions = append(reactions, reaction)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate reactions: %w", err)
	}
	return reactions, nil
}

func (s *Store) DeleteMessage(ctx context.Context, messageID string) error {
	deleted, err := s.markMessageDeleted(ctx, messageID, "local", true)
	if err != nil {
		return err
	}
	if !deleted {
		return fmt.Errorf("message %s does not exist", messageID)
	}
	return nil
}

func (s *Store) DeleteMessageForEveryone(ctx context.Context, messageID string) (bool, error) {
	return s.markMessageDeleted(ctx, messageID, "everyone", false)
}

func (s *Store) markMessageDeleted(ctx context.Context, messageID, reason string, requireExisting bool) (bool, error) {
	if strings.TrimSpace(messageID) == "" {
		return false, fmt.Errorf("message id is required")
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "local"
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin delete message: %w", err)
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
			if requireExisting {
				return false, fmt.Errorf("message %s does not exist", messageID)
			}
			return false, nil
		}
		return false, fmt.Errorf("load message %s for delete: %w", messageID, err)
	}

	now := time.Now().Unix()
	result, err := tx.ExecContext(ctx, `
		UPDATE messages
		SET body = '',
			deleted_at = ?,
			deleted_reason = ?
		WHERE id = ?
			AND deleted_at = 0
	`, now, reason, messageID)
	if err != nil {
		_ = tx.Rollback()
		return false, fmt.Errorf("delete message %s: %w", messageID, err)
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		_ = tx.Rollback()
		if requireExisting {
			return false, fmt.Errorf("message %s does not exist", messageID)
		}
		return false, nil
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM message_fts WHERE message_id = ?`, messageID); err != nil {
		_ = tx.Rollback()
		return false, fmt.Errorf("clear fts for deleted message %s: %w", messageID, err)
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
		return false, fmt.Errorf("refresh chat %s after delete: %w", chatID, err)
	}

	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit delete message: %w", err)
	}

	return true, nil
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
			display_name = CASE WHEN excluded.display_name <> '' THEN excluded.display_name ELSE contacts.display_name END,
			notify_name = CASE WHEN excluded.notify_name <> '' THEN excluded.notify_name ELSE contacts.notify_name END,
			phone = CASE WHEN excluded.phone <> '' THEN excluded.phone ELSE contacts.phone END,
			avatar_path = CASE WHEN excluded.avatar_path <> '' THEN excluded.avatar_path ELSE contacts.avatar_path END,
			avatar_thumb_path = CASE WHEN excluded.avatar_thumb_path <> '' THEN excluded.avatar_thumb_path ELSE contacts.avatar_thumb_path END,
			avatar_updated_at = CASE WHEN excluded.avatar_updated_at > 0 THEN excluded.avatar_updated_at ELSE contacts.avatar_updated_at END,
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
	return s.UpsertMediaMetadataWithDownload(ctx, media, nil)
}

func (s *Store) UpsertMediaMetadataWithDownload(ctx context.Context, media MediaMetadata, descriptor *MediaDownloadDescriptor) error {
	if descriptor == nil {
		return upsertMediaMetadata(ctx, s.db, media)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin upsert media metadata: %w", err)
	}
	if err := upsertMediaMetadata(ctx, tx, media); err != nil {
		_ = tx.Rollback()
		return err
	}
	if strings.TrimSpace(descriptor.MessageID) == "" {
		descriptor.MessageID = media.MessageID
	}
	if err := upsertMediaDownloadDescriptor(ctx, tx, *descriptor); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit upsert media metadata: %w", err)
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
			message_id, media_kind, mime_type, file_name, size_bytes, local_path,
			thumbnail_path, download_state, is_animated, is_lottie, accessibility_label, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(message_id) DO UPDATE SET
			media_kind = CASE
				WHEN excluded.media_kind != '' THEN excluded.media_kind
				ELSE media_metadata.media_kind
			END,
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
			is_animated = CASE
				WHEN excluded.media_kind != '' OR excluded.is_animated != 0 THEN excluded.is_animated
				ELSE media_metadata.is_animated
			END,
			is_lottie = CASE
				WHEN excluded.media_kind != '' OR excluded.is_lottie != 0 THEN excluded.is_lottie
				ELSE media_metadata.is_lottie
			END,
			accessibility_label = CASE
				WHEN excluded.accessibility_label != '' THEN excluded.accessibility_label
				ELSE media_metadata.accessibility_label
			END,
			updated_at = excluded.updated_at
	`,
		media.MessageID,
		media.Kind,
		media.MIMEType,
		media.FileName,
		media.SizeBytes,
		media.LocalPath,
		media.ThumbnailPath,
		media.DownloadState,
		boolToInt(media.IsAnimated),
		boolToInt(media.IsLottie),
		media.AccessibilityLabel,
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
		isAnimated  int
		isLottie    int
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT message_id, media_kind, mime_type, file_name, size_bytes, local_path,
			thumbnail_path, download_state, is_animated, is_lottie, accessibility_label, updated_at
		FROM media_metadata
		WHERE message_id = ?
	`, messageID).Scan(
		&media.MessageID,
		&media.Kind,
		&media.MIMEType,
		&media.FileName,
		&media.SizeBytes,
		&media.LocalPath,
		&media.ThumbnailPath,
		&media.DownloadState,
		&isAnimated,
		&isLottie,
		&media.AccessibilityLabel,
		&updatedUnix,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return MediaMetadata{}, nil
		}
		return MediaMetadata{}, fmt.Errorf("load media metadata for %s: %w", messageID, err)
	}
	media.IsAnimated = isAnimated == 1
	media.IsLottie = isLottie == 1
	media.UpdatedAt = time.Unix(updatedUnix, 0)

	return media, nil
}

func (s *Store) UpsertMediaDownloadDescriptor(ctx context.Context, descriptor MediaDownloadDescriptor) error {
	return upsertMediaDownloadDescriptor(ctx, s.db, descriptor)
}

func upsertMediaDownloadDescriptor(ctx context.Context, execer mediaMetadataExecer, descriptor MediaDownloadDescriptor) error {
	if strings.TrimSpace(descriptor.MessageID) == "" {
		return fmt.Errorf("descriptor message id is required")
	}
	if strings.TrimSpace(descriptor.Kind) == "" {
		return fmt.Errorf("descriptor kind is required")
	}
	if strings.TrimSpace(descriptor.URL) == "" && strings.TrimSpace(descriptor.DirectPath) == "" {
		return fmt.Errorf("descriptor download source is required")
	}
	if descriptor.UpdatedAt.IsZero() {
		descriptor.UpdatedAt = time.Now()
	}

	_, err := execer.ExecContext(ctx, `
		INSERT INTO media_download_descriptors (
			message_id, kind, url, direct_path, media_key, file_sha256,
			file_enc_sha256, file_length, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(message_id) DO UPDATE SET
			kind = CASE
				WHEN excluded.kind != '' THEN excluded.kind
				ELSE media_download_descriptors.kind
			END,
			url = CASE
				WHEN excluded.url != '' THEN excluded.url
				ELSE media_download_descriptors.url
			END,
			direct_path = CASE
				WHEN excluded.direct_path != '' THEN excluded.direct_path
				ELSE media_download_descriptors.direct_path
			END,
			media_key = CASE
				WHEN length(excluded.media_key) > 0 THEN excluded.media_key
				ELSE media_download_descriptors.media_key
			END,
			file_sha256 = CASE
				WHEN length(excluded.file_sha256) > 0 THEN excluded.file_sha256
				ELSE media_download_descriptors.file_sha256
			END,
			file_enc_sha256 = CASE
				WHEN length(excluded.file_enc_sha256) > 0 THEN excluded.file_enc_sha256
				ELSE media_download_descriptors.file_enc_sha256
			END,
			file_length = CASE
				WHEN excluded.file_length > 0 THEN excluded.file_length
				ELSE media_download_descriptors.file_length
			END,
			updated_at = excluded.updated_at
	`,
		descriptor.MessageID,
		descriptor.Kind,
		descriptor.URL,
		descriptor.DirectPath,
		nonNilBytes(descriptor.MediaKey),
		nonNilBytes(descriptor.FileSHA256),
		nonNilBytes(descriptor.FileEncSHA256),
		descriptor.FileLength,
		descriptor.UpdatedAt.Unix(),
	)
	if err != nil {
		return fmt.Errorf("upsert media download descriptor for %s: %w", descriptor.MessageID, err)
	}

	return nil
}

func nonNilBytes(input []byte) []byte {
	if input == nil {
		return []byte{}
	}
	return input
}

func (s *Store) MediaDownloadDescriptor(ctx context.Context, messageID string) (MediaDownloadDescriptor, bool, error) {
	var (
		descriptor  MediaDownloadDescriptor
		updatedUnix int64
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT message_id, kind, url, direct_path, media_key, file_sha256,
			file_enc_sha256, file_length, updated_at
		FROM media_download_descriptors
		WHERE message_id = ?
	`, messageID).Scan(
		&descriptor.MessageID,
		&descriptor.Kind,
		&descriptor.URL,
		&descriptor.DirectPath,
		&descriptor.MediaKey,
		&descriptor.FileSHA256,
		&descriptor.FileEncSHA256,
		&descriptor.FileLength,
		&updatedUnix,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return MediaDownloadDescriptor{}, false, nil
		}
		return MediaDownloadDescriptor{}, false, fmt.Errorf("load media download descriptor for %s: %w", messageID, err)
	}
	descriptor.UpdatedAt = time.Unix(updatedUnix, 0)

	return descriptor, true, nil
}

func (s *Store) attachMessageDetails(ctx context.Context, messages []Message) ([]Message, error) {
	var err error
	messages, err = s.attachMediaMetadata(ctx, messages)
	if err != nil {
		return nil, err
	}
	return s.attachMessageReactions(ctx, messages)
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
		SELECT message_id, media_kind, mime_type, file_name, size_bytes, local_path,
			thumbnail_path, download_state, is_animated, is_lottie, accessibility_label, updated_at
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
			isAnimated  int
			isLottie    int
		)
		if err := rows.Scan(
			&media.MessageID,
			&media.Kind,
			&media.MIMEType,
			&media.FileName,
			&media.SizeBytes,
			&media.LocalPath,
			&media.ThumbnailPath,
			&media.DownloadState,
			&isAnimated,
			&isLottie,
			&media.AccessibilityLabel,
			&updatedUnix,
		); err != nil {
			return nil, fmt.Errorf("scan media metadata: %w", err)
		}
		media.IsAnimated = isAnimated == 1
		media.IsLottie = isLottie == 1
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

func (s *Store) attachMessageReactions(ctx context.Context, messages []Message) ([]Message, error) {
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
		SELECT message_id, sender_jid, emoji, timestamp_unix, is_outgoing, updated_at
		FROM message_reactions
		WHERE message_id IN (`+placeholders+`)
		ORDER BY updated_at ASC, sender_jid ASC
	`, args...)
	if err != nil {
		return nil, fmt.Errorf("list message reactions: %w", err)
	}
	defer rows.Close()

	byMessage := make(map[string][]Reaction, len(messages))
	for rows.Next() {
		reaction, err := scanReaction(rows)
		if err != nil {
			return nil, fmt.Errorf("scan message reaction: %w", err)
		}
		byMessage[reaction.MessageID] = append(byMessage[reaction.MessageID], reaction)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate message reactions: %w", err)
	}

	for i := range messages {
		messages[i].Reactions = byMessage[messages[i].ID]
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
		chat              Chat
		pinned            int
		muted             int
		hasDraft          int
		lastMessageUnix   int64
		avatarUpdatedUnix int64
	)
	if err := row.Scan(
		&chat.ID,
		&chat.JID,
		&chat.Title,
		&chat.TitleSource,
		&chat.Kind,
		&chat.AvatarID,
		&chat.AvatarPath,
		&chat.AvatarThumbPath,
		&avatarUpdatedUnix,
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
	if avatarUpdatedUnix > 0 {
		chat.AvatarUpdatedAt = time.Unix(avatarUpdatedUnix, 0)
	}
	if chat.Kind == "" {
		chat.Kind = "direct"
	}
	if chat.JID == "" {
		chat.JID = chat.ID
	}
	chat.TitleSource = NormalizeChatTitleSource(chat.TitleSource)

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

func scanReaction(row scanner) (Reaction, error) {
	var (
		reaction      Reaction
		timestampUnix int64
		updatedUnix   int64
		isOutgoing    int
	)
	if err := row.Scan(
		&reaction.MessageID,
		&reaction.SenderJID,
		&reaction.Emoji,
		&timestampUnix,
		&isOutgoing,
		&updatedUnix,
	); err != nil {
		return Reaction{}, err
	}
	if timestampUnix > 0 {
		reaction.Timestamp = time.Unix(timestampUnix, 0)
	}
	if updatedUnix > 0 {
		reaction.UpdatedAt = time.Unix(updatedUnix, 0)
	}
	reaction.IsOutgoing = isOutgoing == 1
	return reaction, nil
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
