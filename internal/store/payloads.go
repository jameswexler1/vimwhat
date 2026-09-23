package store

import (
	"context"
	"database/sql"
	"time"

	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"
)

// A late send acknowledgement must not overwrite an edited forwarding payload.
func (s *Store) SaveOutgoingPayload(ctx context.Context, id string, data []byte) error {
	if len(data) == 0 {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO message_payloads(message_id,payload,updated_at)
		SELECT id,?,? FROM messages WHERE id=? AND edited_at=0 AND deleted_at=0
		ON CONFLICT(message_id) DO UPDATE SET payload=excluded.payload,updated_at=excluded.updated_at`, data, time.Now().Unix(), id)
	return err
}

func rewriteEditedPayload(ctx context.Context, tx *sql.Tx, id, body string, editedAt time.Time) error {
	var data []byte
	err := tx.QueryRowContext(ctx, `SELECT payload FROM message_payloads WHERE message_id=?`, id).Scan(&data)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	message := &waE2E.Message{}
	if proto.Unmarshal(data, message) != nil || !rewritePayloadBody(message, body) {
		// Unsupported/corrupt payloads must not forward stale content.
		_, err = tx.ExecContext(ctx, `DELETE FROM message_payloads WHERE message_id=?`, id)
		return err
	}
	data, err = proto.Marshal(message)
	if err != nil {
		return err
	}
	return upsertMessagePayload(ctx, tx, MessagePayload{MessageID: id, Payload: data, UpdatedAt: editedAt})
}

func rewritePayloadBody(m *waE2E.Message, body string) bool {
	if m == nil {
		return false
	}
	switch {
	case m.EphemeralMessage != nil:
		return rewritePayloadBody(m.EphemeralMessage.GetMessage(), body)
	case m.DocumentWithCaptionMessage != nil:
		return rewritePayloadBody(m.DocumentWithCaptionMessage.GetMessage(), body)
	case m.ExtendedTextMessage != nil:
		m.ExtendedTextMessage.Text = proto.String(body)
		if m.ExtendedTextMessage.ContextInfo != nil {
			m.ExtendedTextMessage.ContextInfo.MentionedJID = nil
		}
	case m.Conversation != nil:
		m.Conversation = proto.String(body)
	case m.ImageMessage != nil:
		m.ImageMessage.Caption = proto.String(body)
	case m.VideoMessage != nil:
		m.VideoMessage.Caption = proto.String(body)
	case m.DocumentMessage != nil:
		m.DocumentMessage.Caption = proto.String(body)
	default:
		return false
	}
	return true
}
