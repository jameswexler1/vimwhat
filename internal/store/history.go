package store

import "context"

func (s *Store) ListMessagesAfter(ctx context.Context, chatID string, after Message, limit int) ([]Message, error) {
	if limit <= 0 {
		limit = defaultMessageWindow
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,remote_id,chat_id,chat_jid,sender,sender_jid,body,
		timestamp_unix,is_outgoing,status,quoted_message_id,quoted_remote_id,deleted_at,deleted_reason,edited_at
		FROM messages m WHERE chat_id=? AND `+conversationMessageWhereSQL+`
		AND (timestamp_unix>? OR (timestamp_unix=? AND id>?)) ORDER BY timestamp_unix,id LIMIT ?`, chatID, after.Timestamp.Unix(), after.Timestamp.Unix(), after.ID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var messages []Message
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		messages = append(messages, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	return s.attachMessageDetails(ctx, messages)
}

// ListMessagesAround locates a quote directly without expanding the entire
// conversation into memory. Paging in either direction uses stable ID ties.
func (s *Store) ListMessagesAround(ctx context.Context, chatID, targetID string, limit int) ([]Message, error) {
	if limit <= 0 {
		limit = defaultMessageWindow
	}
	anchor, found, err := s.MessageByID(ctx, targetID)
	if err != nil || !found || anchor.ChatID != chatID {
		return nil, err
	}
	if !anchor.DeletedAt.IsZero() && anchor.DeletedReason != "everyone" {
		return nil, nil
	}
	var before, after []Message
	if limit > 1 {
		before, err = s.ListMessagesBefore(ctx, chatID, anchor, limit/2)
		if err != nil {
			return nil, err
		}
		if remaining := limit - len(before) - 1; remaining > 0 {
			after, err = s.ListMessagesAfter(ctx, chatID, anchor, remaining)
			if err != nil {
				return nil, err
			}
		}
	}
	return append(append(before, anchor), after...), nil
}
