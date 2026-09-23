package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// ComposerDraft is durable composition state, independent of outgoing messages.
type ComposerDraft struct {
	Body     string
	Media    []MediaMetadata
	Reply    *Message
	Mentions []MessageMention
}

func (d ComposerDraft) Empty() bool {
	return strings.TrimSpace(d.Body) == "" && len(d.Media) == 0 && d.Reply == nil
}

func (s *Store) SaveComposerDraft(ctx context.Context, chatID string, draft ComposerDraft) error {
	if strings.TrimSpace(chatID) == "" {
		return fmt.Errorf("draft chat is required")
	}
	data, err := json.Marshal(draft)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if draft.Empty() {
		if _, err = tx.ExecContext(ctx, `DELETE FROM drafts WHERE chat_id=?`, chatID); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM ui_snapshots WHERE kind='draft' AND name='composer' AND chat_id=?`, chatID); err != nil {
			return err
		}
	} else {
		now := time.Now().Unix()
		if _, err = tx.ExecContext(ctx, `INSERT INTO drafts(chat_id,body,updated_at) VALUES(?,?,?)
			ON CONFLICT(chat_id) DO UPDATE SET body=excluded.body,updated_at=excluded.updated_at`, chatID, draft.Body, now); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO ui_snapshots(kind,name,chat_id,value,updated_at) VALUES('draft','composer',?,?,?)
			ON CONFLICT(kind,name,chat_id) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at`, chatID, string(data), now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) ListComposerDrafts(ctx context.Context) (map[string]ComposerDraft, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT d.chat_id,d.body,COALESCE(u.value,'') FROM drafts d
		LEFT JOIN ui_snapshots u ON u.chat_id=d.chat_id AND u.kind='draft' AND u.name='composer'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]ComposerDraft{}
	for rows.Next() {
		var id, body, data string
		if err := rows.Scan(&id, &body, &data); err != nil {
			return nil, err
		}
		draft := ComposerDraft{}
		if data != "" {
			if err := json.Unmarshal([]byte(data), &draft); err != nil {
				return nil, fmt.Errorf("decode draft %s: %w", id, err)
			}
		}
		draft.Body = body
		out[id] = draft
	}
	return out, rows.Err()
}
