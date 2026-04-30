package store

import (
	"context"
	"crypto/sha1"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

func (s *Store) MergeChatAlias(ctx context.Context, canonicalID, aliasID string) error {
	canonicalID = strings.TrimSpace(canonicalID)
	aliasID = strings.TrimSpace(aliasID)
	if canonicalID == "" || aliasID == "" || canonicalID == aliasID {
		return nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin merge chat alias: %w", err)
	}

	canonical, canonicalExists, err := loadChatMergeRow(ctx, tx, canonicalID)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	alias, aliasExists, err := loadChatMergeRow(ctx, tx, aliasID)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	if !aliasExists {
		_ = tx.Rollback()
		return nil
	}
	if alias.Kind != "" && alias.Kind != "direct" {
		_ = tx.Rollback()
		return nil
	}
	if canonicalExists && canonical.Kind != "" && canonical.Kind != "direct" {
		_ = tx.Rollback()
		return fmt.Errorf("canonical chat %s is not direct", canonicalID)
	}

	merged := mergeChatRows(canonical, canonicalExists, alias, canonicalID)
	if err := upsertMergedChatRow(ctx, tx, merged); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := mergeDraftRows(ctx, tx, canonicalID, aliasID); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := mergeUISnapshotRows(ctx, tx, canonicalID, aliasID); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := mergeHistoryCursorRows(ctx, tx, canonicalID, aliasID); err != nil {
		_ = tx.Rollback()
		return err
	}

	messages, err := loadMessagesForChatMerge(ctx, tx, aliasID)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	messageIDMap := make(map[string]string, len(messages))
	for _, message := range messages {
		targetID, duplicate, err := resolveMergedMessageID(ctx, tx, canonicalID, message)
		if err != nil {
			_ = tx.Rollback()
			return err
		}
		messageIDMap[message.ID] = targetID
		if duplicate {
			if err := mergeAliasMessageIntoExisting(ctx, tx, message, targetID, canonicalID, aliasID); err != nil {
				_ = tx.Rollback()
				return err
			}
			continue
		}
		if err := insertMovedMessage(ctx, tx, message, targetID, canonicalID); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := mergeMessageChildren(ctx, tx, message.ID, targetID, canonicalID, aliasID); err != nil {
			_ = tx.Rollback()
			return err
		}
	}

	for oldID, newID := range messageIDMap {
		if oldID == newID {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE messages
			SET quoted_message_id = ?
			WHERE quoted_message_id = ?
		`, newID, oldID); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("rewrite quoted message ids for %s: %w", oldID, err)
		}
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM message_fts WHERE message_id IN (SELECT id FROM messages WHERE chat_id = ?)`, aliasID); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("clear alias fts rows for %s: %w", aliasID, err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM chats WHERE id = ?`, aliasID); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("delete alias chat %s: %w", aliasID, err)
	}
	if err := refreshMergedChatState(ctx, tx, merged, canonicalID); err != nil {
		_ = tx.Rollback()
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit merge chat alias %s -> %s: %w", aliasID, canonicalID, err)
	}
	return nil
}

type chatMergeRow struct {
	ID              string
	JID             string
	Title           string
	TitleSource     string
	Kind            string
	AvatarID        string
	AvatarPath      string
	AvatarThumbPath string
	AvatarUpdatedAt time.Time
	Unread          int
	Pinned          bool
	Muted           bool
	LastMessageAt   time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

func loadChatMergeRow(ctx context.Context, tx *sql.Tx, chatID string) (chatMergeRow, bool, error) {
	var (
		row                chatMergeRow
		avatarUpdatedUnix  int64
		lastMessageUnix    int64
		createdUnix        int64
		updatedUnix        int64
		pinnedInt, muteInt int
	)
	err := tx.QueryRowContext(ctx, `
		SELECT id, jid, title, title_source, kind, avatar_id, avatar_path, avatar_thumb_path,
			avatar_updated_at, unread_count, pinned, muted, last_message_at, created_at, updated_at
		FROM chats
		WHERE id = ?
	`, chatID).Scan(
		&row.ID,
		&row.JID,
		&row.Title,
		&row.TitleSource,
		&row.Kind,
		&row.AvatarID,
		&row.AvatarPath,
		&row.AvatarThumbPath,
		&avatarUpdatedUnix,
		&row.Unread,
		&pinnedInt,
		&muteInt,
		&lastMessageUnix,
		&createdUnix,
		&updatedUnix,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return chatMergeRow{}, false, nil
		}
		return chatMergeRow{}, false, fmt.Errorf("load chat %s for merge: %w", chatID, err)
	}
	if row.JID == "" {
		row.JID = row.ID
	}
	if row.Kind == "" {
		row.Kind = "direct"
	}
	row.TitleSource = NormalizeChatTitleSource(row.TitleSource)
	row.Pinned = pinnedInt != 0
	row.Muted = muteInt != 0
	if avatarUpdatedUnix > 0 {
		row.AvatarUpdatedAt = time.Unix(avatarUpdatedUnix, 0)
	}
	if lastMessageUnix > 0 {
		row.LastMessageAt = time.Unix(lastMessageUnix, 0)
	}
	if createdUnix > 0 {
		row.CreatedAt = time.Unix(createdUnix, 0)
	}
	if updatedUnix > 0 {
		row.UpdatedAt = time.Unix(updatedUnix, 0)
	}
	return row, true, nil
}

func mergeChatRows(canonical chatMergeRow, canonicalExists bool, alias chatMergeRow, canonicalID string) chatMergeRow {
	now := time.Now()
	if !canonicalExists {
		merged := alias
		merged.ID = canonicalID
		merged.JID = canonicalID
		if merged.Kind == "" {
			merged.Kind = "direct"
		}
		if strings.TrimSpace(merged.Title) == "" {
			merged.Title = fallbackChatMergeTitle(canonicalID)
			merged.TitleSource = ChatTitleSourceJID
		}
		merged.UpdatedAt = now
		if merged.CreatedAt.IsZero() {
			merged.CreatedAt = now
		}
		return merged
	}

	merged := canonical
	merged.ID = canonicalID
	merged.JID = canonicalID
	merged.Unread += alias.Unread
	merged.Pinned = merged.Pinned || alias.Pinned
	merged.Muted = merged.Muted || alias.Muted
	if alias.LastMessageAt.After(merged.LastMessageAt) {
		merged.LastMessageAt = alias.LastMessageAt
	}
	if merged.CreatedAt.IsZero() || (!alias.CreatedAt.IsZero() && alias.CreatedAt.Before(merged.CreatedAt)) {
		merged.CreatedAt = alias.CreatedAt
	}
	if hasChatAvatar(alias) && (!hasChatAvatar(merged) || alias.AvatarUpdatedAt.After(merged.AvatarUpdatedAt)) {
		merged.AvatarID = alias.AvatarID
		merged.AvatarPath = alias.AvatarPath
		merged.AvatarThumbPath = alias.AvatarThumbPath
		merged.AvatarUpdatedAt = alias.AvatarUpdatedAt
	}
	existingChat := Chat{ID: canonical.ID, JID: canonical.JID, Title: canonical.Title, TitleSource: canonical.TitleSource, Kind: canonical.Kind}
	aliasChat := Chat{ID: alias.ID, JID: alias.JID, Title: alias.Title, TitleSource: alias.TitleSource, Kind: alias.Kind}
	if shouldReplaceChatTitle(existingChat, aliasChat) {
		merged.Title = alias.Title
		merged.TitleSource = NormalizeChatTitleSource(alias.TitleSource)
	}
	if strings.TrimSpace(merged.Title) == "" {
		merged.Title = fallbackChatMergeTitle(canonicalID)
		merged.TitleSource = ChatTitleSourceJID
	}
	merged.UpdatedAt = now
	return merged
}

func hasChatAvatar(chat chatMergeRow) bool {
	return strings.TrimSpace(chat.AvatarID) != "" ||
		strings.TrimSpace(chat.AvatarPath) != "" ||
		strings.TrimSpace(chat.AvatarThumbPath) != ""
}

func fallbackChatMergeTitle(chatID string) string {
	if user := jidUserPart(chatID); user != "" {
		return user
	}
	return chatID
}

func upsertMergedChatRow(ctx context.Context, tx *sql.Tx, chat chatMergeRow) error {
	if chat.Kind == "" {
		chat.Kind = "direct"
	}
	now := time.Now()
	if chat.CreatedAt.IsZero() {
		chat.CreatedAt = now
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO chats (
			id, jid, title, title_source, kind, avatar_id, avatar_path, avatar_thumb_path,
			avatar_updated_at, unread_count, pinned, muted, last_message_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			jid = excluded.jid,
			title = excluded.title,
			title_source = excluded.title_source,
			kind = excluded.kind,
			avatar_id = excluded.avatar_id,
			avatar_path = excluded.avatar_path,
			avatar_thumb_path = excluded.avatar_thumb_path,
			avatar_updated_at = excluded.avatar_updated_at,
			unread_count = excluded.unread_count,
			pinned = excluded.pinned,
			muted = excluded.muted,
			last_message_at = excluded.last_message_at,
			updated_at = excluded.updated_at
	`,
		chat.ID,
		chat.JID,
		chat.Title,
		NormalizeChatTitleSource(chat.TitleSource),
		chat.Kind,
		chat.AvatarID,
		chat.AvatarPath,
		chat.AvatarThumbPath,
		chat.AvatarUpdatedAt.Unix(),
		chat.Unread,
		boolToInt(chat.Pinned),
		boolToInt(chat.Muted),
		chat.LastMessageAt.Unix(),
		chat.CreatedAt.Unix(),
		now.Unix(),
	)
	if err != nil {
		return fmt.Errorf("upsert merged chat %s: %w", chat.ID, err)
	}
	return nil
}

func mergeDraftRows(ctx context.Context, tx *sql.Tx, canonicalID, aliasID string) error {
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO drafts (chat_id, body, updated_at)
		SELECT ?, body, updated_at
		FROM drafts
		WHERE chat_id = ?
			AND body <> ''
		ON CONFLICT(chat_id) DO UPDATE SET
			body = CASE
				WHEN excluded.updated_at >= drafts.updated_at THEN excluded.body
				ELSE drafts.body
			END,
			updated_at = CASE
				WHEN excluded.updated_at >= drafts.updated_at THEN excluded.updated_at
				ELSE drafts.updated_at
			END
	`, canonicalID, aliasID); err != nil {
		return fmt.Errorf("merge drafts for %s -> %s: %w", aliasID, canonicalID, err)
	}
	return nil
}

func mergeUISnapshotRows(ctx context.Context, tx *sql.Tx, canonicalID, aliasID string) error {
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO ui_snapshots (kind, name, chat_id, value, updated_at)
		SELECT kind, name, ?, value, updated_at
		FROM ui_snapshots
		WHERE chat_id = ?
		ON CONFLICT(kind, name, chat_id) DO UPDATE SET
			value = CASE
				WHEN excluded.updated_at >= ui_snapshots.updated_at THEN excluded.value
				ELSE ui_snapshots.value
			END,
			updated_at = CASE
				WHEN excluded.updated_at >= ui_snapshots.updated_at THEN excluded.updated_at
				ELSE ui_snapshots.updated_at
			END
	`, canonicalID, aliasID); err != nil {
		return fmt.Errorf("merge ui snapshots for %s -> %s: %w", aliasID, canonicalID, err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM ui_snapshots WHERE chat_id = ?`, aliasID); err != nil {
		return fmt.Errorf("delete ui snapshots for alias %s: %w", aliasID, err)
	}
	return nil
}

func mergeHistoryCursorRows(ctx context.Context, tx *sql.Tx, canonicalID, aliasID string) error {
	canonicalName := historyExhaustedCursorName(canonicalID)
	aliasName := historyExhaustedCursorName(aliasID)
	if canonicalName == aliasName {
		return nil
	}

	type cursorRow struct {
		name      string
		value     string
		updatedAt int64
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT name, value, updated_at
		FROM sync_cursors
		WHERE name = ?
			OR name = ?
	`, canonicalName, aliasName)
	if err != nil {
		return fmt.Errorf("query history cursors for %s/%s: %w", canonicalID, aliasID, err)
	}
	defer rows.Close()

	var canonical cursorRow
	var alias cursorRow
	var haveCanonical bool
	var haveAlias bool
	for rows.Next() {
		var row cursorRow
		if err := rows.Scan(&row.name, &row.value, &row.updatedAt); err != nil {
			return fmt.Errorf("scan history cursor for %s/%s: %w", canonicalID, aliasID, err)
		}
		switch row.name {
		case canonicalName:
			canonical = row
			haveCanonical = true
		case aliasName:
			alias = row
			haveAlias = true
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate history cursors for %s/%s: %w", canonicalID, aliasID, err)
	}

	if haveCanonical || haveAlias {
		value, updatedAt := mergeHistoryCursorValue(canonical.value, canonical.updatedAt, alias.value, alias.updatedAt)
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO sync_cursors (name, value, updated_at)
			VALUES (?, ?, ?)
			ON CONFLICT(name) DO UPDATE SET
				value = excluded.value,
				updated_at = excluded.updated_at
		`, canonicalName, value, updatedAt); err != nil {
			return fmt.Errorf("upsert history cursor for %s: %w", canonicalID, err)
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM sync_cursors WHERE name = ?`, aliasName); err != nil {
		return fmt.Errorf("delete alias history cursor for %s: %w", aliasID, err)
	}
	return nil
}

func mergeHistoryCursorValue(canonicalValue string, canonicalUpdatedAt int64, aliasValue string, aliasUpdatedAt int64) (string, int64) {
	type candidate struct {
		value     string
		updatedAt int64
	}
	choose := candidate{value: canonicalValue, updatedAt: canonicalUpdatedAt}
	if historyCursorRank(aliasValue) > historyCursorRank(choose.value) ||
		(historyCursorRank(aliasValue) == historyCursorRank(choose.value) && aliasUpdatedAt > choose.updatedAt) {
		choose = candidate{value: aliasValue, updatedAt: aliasUpdatedAt}
	}
	if choose.updatedAt == 0 {
		choose.updatedAt = time.Now().Unix()
	}
	return choose.value, choose.updatedAt
}

func historyCursorRank(value string) int {
	switch strings.TrimSpace(value) {
	case "no_more":
		return 3
	case "no_access":
		return 2
	case "more":
		return 1
	default:
		return 0
	}
}

func historyExhaustedCursorName(chatID string) string {
	return "history:" + strings.TrimSpace(chatID) + ":exhausted"
}

func loadMessagesForChatMerge(ctx context.Context, tx *sql.Tx, chatID string) ([]Message, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT id, remote_id, chat_id, chat_jid, sender, sender_jid, body,
			timestamp_unix, is_outgoing, status, quoted_message_id, quoted_remote_id,
			deleted_at, deleted_reason, edited_at
		FROM messages
		WHERE chat_id = ?
		ORDER BY timestamp_unix ASC, id ASC
	`, chatID)
	if err != nil {
		return nil, fmt.Errorf("list alias messages for %s: %w", chatID, err)
	}
	defer rows.Close()

	var messages []Message
	for rows.Next() {
		message, err := scanMessage(rows)
		if err != nil {
			return nil, fmt.Errorf("scan alias message: %w", err)
		}
		messages = append(messages, message)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate alias messages for %s: %w", chatID, err)
	}
	return messages, nil
}

func resolveMergedMessageID(ctx context.Context, tx *sql.Tx, canonicalID string, message Message) (string, bool, error) {
	if remoteID := strings.TrimSpace(message.RemoteID); remoteID != "" {
		var existingID string
		err := tx.QueryRowContext(ctx, `
			SELECT id
			FROM messages
			WHERE chat_id = ?
				AND remote_id = ?
		`, canonicalID, remoteID).Scan(&existingID)
		if err == nil {
			return existingID, true, nil
		}
		if err != nil && err != sql.ErrNoRows {
			return "", false, fmt.Errorf("load canonical message for %s/%s: %w", canonicalID, remoteID, err)
		}
		return localMessageID(canonicalID, remoteID), false, nil
	}

	targetID := fallbackMergedMessageID(canonicalID, message.ID)
	var existingID string
	err := tx.QueryRowContext(ctx, `SELECT id FROM messages WHERE id = ?`, targetID).Scan(&existingID)
	if err == nil {
		targetID = localMessageID(canonicalID, "local-"+shortMessageHash(message.ID))
	}
	if err != nil && err != sql.ErrNoRows {
		return "", false, fmt.Errorf("check canonical message id %s: %w", targetID, err)
	}
	return targetID, false, nil
}

func fallbackMergedMessageID(canonicalID, oldMessageID string) string {
	suffix := strings.TrimSpace(oldMessageID)
	if index := strings.IndexByte(suffix, '/'); index >= 0 && index+1 < len(suffix) {
		suffix = suffix[index+1:]
	}
	suffix = strings.TrimSpace(suffix)
	if suffix == "" {
		suffix = "local-" + shortMessageHash(oldMessageID)
	}
	return localMessageID(canonicalID, suffix)
}

func shortMessageHash(value string) string {
	sum := sha1.Sum([]byte(value))
	return hex.EncodeToString(sum[:])[:12]
}

func loadMessageForMerge(ctx context.Context, tx *sql.Tx, messageID string) (Message, bool, error) {
	row := tx.QueryRowContext(ctx, `
		SELECT id, remote_id, chat_id, chat_jid, sender, sender_jid, body,
			timestamp_unix, is_outgoing, status, quoted_message_id, quoted_remote_id,
			deleted_at, deleted_reason, edited_at
		FROM messages
		WHERE id = ?
	`, messageID)
	message, err := scanMessage(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return Message{}, false, nil
		}
		return Message{}, false, fmt.Errorf("load message %s for merge: %w", messageID, err)
	}
	return message, true, nil
}

func mergeAliasMessageIntoExisting(ctx context.Context, tx *sql.Tx, alias Message, targetID, canonicalID, aliasID string) error {
	target, ok, err := loadMessageForMerge(ctx, tx, targetID)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("canonical message %s missing during merge", targetID)
	}
	merged := mergeMessageRows(target, alias, canonicalID)
	if err := writeMergedMessage(ctx, tx, merged); err != nil {
		return err
	}
	if err := mergeMessageChildren(ctx, tx, alias.ID, targetID, canonicalID, aliasID); err != nil {
		return err
	}
	return nil
}

func mergeMessageRows(target, alias Message, canonicalID string) Message {
	merged := target
	merged.ChatID = canonicalID
	merged.ChatJID = canonicalID
	if strings.TrimSpace(merged.RemoteID) == "" {
		merged.RemoteID = alias.RemoteID
	}
	if strings.TrimSpace(merged.Sender) == "" {
		merged.Sender = alias.Sender
	}
	if strings.TrimSpace(merged.SenderJID) != "me" {
		merged.SenderJID = canonicalID
	}
	if strings.TrimSpace(merged.Body) == "" && strings.TrimSpace(alias.Body) != "" {
		merged.Body = alias.Body
	}
	if merged.Timestamp.IsZero() {
		merged.Timestamp = alias.Timestamp
	}
	merged.IsOutgoing = merged.IsOutgoing || alias.IsOutgoing
	if messageStatusRank(alias.Status) > messageStatusRank(merged.Status) {
		merged.Status = alias.Status
	}
	if strings.TrimSpace(merged.QuotedMessageID) == "" {
		merged.QuotedMessageID = alias.QuotedMessageID
	}
	if strings.TrimSpace(merged.QuotedRemoteID) == "" {
		merged.QuotedRemoteID = alias.QuotedRemoteID
	}
	if merged.DeletedAt.IsZero() && !alias.DeletedAt.IsZero() {
		merged.DeletedAt = alias.DeletedAt
		merged.DeletedReason = alias.DeletedReason
	}
	return merged
}

func insertMovedMessage(ctx context.Context, tx *sql.Tx, message Message, targetID, canonicalID string) error {
	moved := message
	moved.ID = targetID
	moved.ChatID = canonicalID
	moved.ChatJID = canonicalID
	if strings.TrimSpace(moved.SenderJID) != "me" {
		moved.SenderJID = canonicalID
	}
	if err := writeMergedMessage(ctx, tx, moved); err != nil {
		return err
	}
	return nil
}

func writeMergedMessage(ctx context.Context, tx *sql.Tx, message Message) error {
	deletedAt := int64(0)
	if !message.DeletedAt.IsZero() {
		deletedAt = message.DeletedAt.Unix()
	}
	editedAt := int64(0)
	if !message.EditedAt.IsZero() {
		editedAt = message.EditedAt.Unix()
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO messages (
			id, remote_id, chat_id, chat_jid, sender, sender_jid, body,
			timestamp_unix, is_outgoing, status, quoted_message_id, quoted_remote_id,
			deleted_at, deleted_reason, edited_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			remote_id = excluded.remote_id,
			chat_id = excluded.chat_id,
			chat_jid = excluded.chat_jid,
			sender = excluded.sender,
			sender_jid = excluded.sender_jid,
			body = excluded.body,
			timestamp_unix = excluded.timestamp_unix,
			is_outgoing = excluded.is_outgoing,
			status = excluded.status,
			quoted_message_id = excluded.quoted_message_id,
			quoted_remote_id = excluded.quoted_remote_id,
			deleted_at = excluded.deleted_at,
			deleted_reason = excluded.deleted_reason,
			edited_at = excluded.edited_at
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
		editedAt,
	)
	if err != nil {
		return fmt.Errorf("write merged message %s: %w", message.ID, err)
	}
	if err := reindexMessageFTS(ctx, tx, message); err != nil {
		return err
	}
	return nil
}

func reindexMessageFTS(ctx context.Context, tx *sql.Tx, message Message) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM message_fts WHERE message_id = ?`, message.ID); err != nil {
		return fmt.Errorf("clear fts for merged message %s: %w", message.ID, err)
	}
	if !message.DeletedAt.IsZero() {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO message_fts (message_id, chat_id, body)
		VALUES (?, ?, ?)
	`, message.ID, message.ChatID, message.Body); err != nil {
		return fmt.Errorf("reindex merged message %s: %w", message.ID, err)
	}
	return nil
}

func mergeMessageChildren(ctx context.Context, tx *sql.Tx, oldID, targetID, canonicalID, aliasID string) error {
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO media_metadata (
			message_id, media_kind, mime_type, file_name, size_bytes, local_path,
			thumbnail_path, download_state, is_animated, is_lottie, accessibility_label, updated_at
		)
		SELECT ?, media_kind, mime_type, file_name, size_bytes, local_path,
			thumbnail_path, download_state, is_animated, is_lottie, accessibility_label, updated_at
		FROM media_metadata
		WHERE message_id = ?
		ON CONFLICT(message_id) DO UPDATE SET
			media_kind = CASE
				WHEN excluded.media_kind <> '' THEN excluded.media_kind
				ELSE media_metadata.media_kind
			END,
			mime_type = CASE
				WHEN excluded.mime_type <> '' THEN excluded.mime_type
				ELSE media_metadata.mime_type
			END,
			file_name = CASE
				WHEN excluded.file_name <> '' THEN excluded.file_name
				ELSE media_metadata.file_name
			END,
			size_bytes = CASE
				WHEN excluded.size_bytes > 0 THEN excluded.size_bytes
				ELSE media_metadata.size_bytes
			END,
			local_path = CASE
				WHEN excluded.local_path <> '' THEN excluded.local_path
				ELSE media_metadata.local_path
			END,
			thumbnail_path = CASE
				WHEN excluded.thumbnail_path <> '' THEN excluded.thumbnail_path
				ELSE media_metadata.thumbnail_path
			END,
			download_state = CASE
				WHEN media_metadata.local_path <> '' AND excluded.local_path = '' THEN media_metadata.download_state
				WHEN excluded.download_state <> '' THEN excluded.download_state
				ELSE media_metadata.download_state
			END,
			is_animated = CASE
				WHEN excluded.is_animated <> 0 THEN excluded.is_animated
				ELSE media_metadata.is_animated
			END,
			is_lottie = CASE
				WHEN excluded.is_lottie <> 0 THEN excluded.is_lottie
				ELSE media_metadata.is_lottie
			END,
			accessibility_label = CASE
				WHEN excluded.accessibility_label <> '' THEN excluded.accessibility_label
				ELSE media_metadata.accessibility_label
			END,
			updated_at = CASE
				WHEN excluded.updated_at > media_metadata.updated_at THEN excluded.updated_at
				ELSE media_metadata.updated_at
			END
	`, targetID, oldID); err != nil {
		return fmt.Errorf("merge media metadata for %s -> %s: %w", oldID, targetID, err)
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO media_download_descriptors (
			message_id, kind, url, direct_path, media_key, file_sha256, file_enc_sha256, file_length, updated_at
		)
		SELECT ?, kind, url, direct_path, media_key, file_sha256, file_enc_sha256, file_length, updated_at
		FROM media_download_descriptors
		WHERE message_id = ?
		ON CONFLICT(message_id) DO UPDATE SET
			kind = CASE
				WHEN excluded.kind <> '' THEN excluded.kind
				ELSE media_download_descriptors.kind
			END,
			url = CASE
				WHEN excluded.url <> '' THEN excluded.url
				ELSE media_download_descriptors.url
			END,
			direct_path = CASE
				WHEN excluded.direct_path <> '' THEN excluded.direct_path
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
			updated_at = CASE
				WHEN excluded.updated_at > media_download_descriptors.updated_at THEN excluded.updated_at
				ELSE media_download_descriptors.updated_at
			END
	`, targetID, oldID); err != nil {
		return fmt.Errorf("merge media download descriptors for %s -> %s: %w", oldID, targetID, err)
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO message_reactions (
			message_id, sender_jid, emoji, timestamp_unix, is_outgoing, updated_at
		)
		SELECT ?, CASE
				WHEN sender_jid = ? THEN ?
				ELSE sender_jid
			END, emoji, timestamp_unix, is_outgoing, updated_at
		FROM message_reactions
		WHERE message_id = ?
		ON CONFLICT(message_id, sender_jid) DO UPDATE SET
			emoji = excluded.emoji,
			timestamp_unix = CASE
				WHEN excluded.updated_at >= message_reactions.updated_at THEN excluded.timestamp_unix
				ELSE message_reactions.timestamp_unix
			END,
			is_outgoing = CASE
				WHEN excluded.updated_at >= message_reactions.updated_at THEN excluded.is_outgoing
				ELSE message_reactions.is_outgoing
			END,
			updated_at = CASE
				WHEN excluded.updated_at >= message_reactions.updated_at THEN excluded.updated_at
				ELSE message_reactions.updated_at
			END
	`, targetID, aliasID, canonicalID, oldID); err != nil {
		return fmt.Errorf("merge reactions for %s -> %s: %w", oldID, targetID, err)
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM message_fts WHERE message_id = ?`, oldID); err != nil {
		return fmt.Errorf("clear old fts row for %s: %w", oldID, err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM messages WHERE id = ?`, oldID); err != nil {
		return fmt.Errorf("delete alias message %s: %w", oldID, err)
	}
	return nil
}

func refreshMergedChatState(ctx context.Context, tx *sql.Tx, merged chatMergeRow, canonicalID string) error {
	var visibleLastMessageAt int64
	err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(timestamp_unix), 0)
		FROM messages
		WHERE chat_id = ?
			AND deleted_at = 0
	`, canonicalID).Scan(&visibleLastMessageAt)
	if err != nil {
		return fmt.Errorf("query merged chat timestamp for %s: %w", canonicalID, err)
	}
	lastMessageAt := merged.LastMessageAt.Unix()
	if visibleLastMessageAt > lastMessageAt {
		lastMessageAt = visibleLastMessageAt
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE chats
		SET unread_count = ?, pinned = ?, muted = ?, last_message_at = ?, updated_at = ?
		WHERE id = ?
	`, merged.Unread, boolToInt(merged.Pinned), boolToInt(merged.Muted), lastMessageAt, time.Now().Unix(), canonicalID); err != nil {
		return fmt.Errorf("refresh merged chat %s: %w", canonicalID, err)
	}
	return nil
}

func localMessageID(chatID, remoteID string) string {
	chatID = strings.TrimSpace(chatID)
	remoteID = strings.TrimSpace(remoteID)
	if chatID == "" || remoteID == "" {
		return ""
	}
	return chatID + "/" + remoteID
}
