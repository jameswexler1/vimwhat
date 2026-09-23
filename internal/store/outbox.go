package store

import "context"

// Messages are the durable outbox. A process restart cannot prove whether a
// sending message reached the server; never automatically resend these rows.
func (s *Store) RecoverInterruptedSends(ctx context.Context) (int64, error) {
	result, err := s.db.ExecContext(ctx, `UPDATE messages SET status='uncertain' WHERE is_outgoing=1 AND status IN ('sending','queued')`)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// ClaimOutgoingRetry prevents duplicate retries, including stale UI requests.
func (s *Store) ClaimOutgoingRetry(ctx context.Context, id string) (bool, error) {
	result, err := s.db.ExecContext(ctx, `UPDATE messages SET status='sending' WHERE id=? AND is_outgoing=1 AND status IN ('failed','uncertain') AND deleted_at=0`, id)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}
