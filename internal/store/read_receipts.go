package store

import (
	"context"
	"time"
)

// AcknowledgeReadTargets is idempotent and clears only the acknowledged IDs.
// Arrivals during the network call keep their unread flag, regardless of their
// timestamps (including delayed/decrypted messages older than the read target).
func (s *Store) AcknowledgeReadTargets(ctx context.Context, chatID string, remoteIDs []string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var cleared int64
	for _, id := range remoteIDs {
		if id == "" {
			continue
		}
		result, err := tx.ExecContext(ctx, `UPDATE messages SET local_unread=0 WHERE chat_id=? AND remote_id=? AND local_unread=1 AND is_outgoing=0`, chatID, id)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		cleared += n
	}
	if _, err := tx.ExecContext(ctx, `UPDATE chats SET unread_count=MAX(0,unread_count-?),updated_at=? WHERE id=?`, cleared, time.Now().Unix(), chatID); err != nil {
		return err
	}
	return tx.Commit()
}
