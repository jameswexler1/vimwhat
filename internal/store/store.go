package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct {
	db   *sql.DB
	path string
}

type migration struct {
	name string
	sql  []string
}

var migrations = []migration{
	{
		name: "0001_initial_schema",
		sql: []string{
			`CREATE TABLE IF NOT EXISTS chats (
				id TEXT PRIMARY KEY,
				title TEXT NOT NULL,
				unread_count INTEGER NOT NULL DEFAULT 0,
				pinned INTEGER NOT NULL DEFAULT 0,
				muted INTEGER NOT NULL DEFAULT 0,
				last_message_at INTEGER NOT NULL DEFAULT 0,
				created_at INTEGER NOT NULL,
				updated_at INTEGER NOT NULL
			)`,
			`CREATE INDEX IF NOT EXISTS chats_sort_idx
				ON chats (pinned DESC, last_message_at DESC, title ASC)`,
			`CREATE TABLE IF NOT EXISTS messages (
				id TEXT PRIMARY KEY,
				chat_id TEXT NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
				sender TEXT NOT NULL,
				body TEXT NOT NULL DEFAULT '',
				timestamp_unix INTEGER NOT NULL,
				is_outgoing INTEGER NOT NULL DEFAULT 0
			)`,
			`CREATE INDEX IF NOT EXISTS messages_chat_time_idx
				ON messages (chat_id, timestamp_unix ASC, id ASC)`,
			`CREATE TABLE IF NOT EXISTS drafts (
				chat_id TEXT PRIMARY KEY REFERENCES chats(id) ON DELETE CASCADE,
				body TEXT NOT NULL,
				updated_at INTEGER NOT NULL
			)`,
			`CREATE TABLE IF NOT EXISTS sync_cursors (
				name TEXT PRIMARY KEY,
				value TEXT NOT NULL,
				updated_at INTEGER NOT NULL
			)`,
			`CREATE VIRTUAL TABLE IF NOT EXISTS message_fts USING fts5(
				message_id UNINDEXED,
				chat_id UNINDEXED,
				body
			)`,
		},
	},
	{
		name: "0002_protocol_ready_local_state",
		sql: []string{
			`ALTER TABLE chats ADD COLUMN jid TEXT NOT NULL DEFAULT ''`,
			`ALTER TABLE chats ADD COLUMN kind TEXT NOT NULL DEFAULT 'direct'`,
			`ALTER TABLE messages ADD COLUMN remote_id TEXT NOT NULL DEFAULT ''`,
			`ALTER TABLE messages ADD COLUMN chat_jid TEXT NOT NULL DEFAULT ''`,
			`ALTER TABLE messages ADD COLUMN sender_jid TEXT NOT NULL DEFAULT ''`,
			`ALTER TABLE messages ADD COLUMN status TEXT NOT NULL DEFAULT ''`,
			`ALTER TABLE messages ADD COLUMN quoted_message_id TEXT NOT NULL DEFAULT ''`,
			`ALTER TABLE messages ADD COLUMN quoted_remote_id TEXT NOT NULL DEFAULT ''`,
			`CREATE UNIQUE INDEX IF NOT EXISTS messages_remote_idx
				ON messages (chat_id, remote_id)
				WHERE remote_id <> ''`,
			`CREATE TABLE IF NOT EXISTS contacts (
				jid TEXT PRIMARY KEY,
				display_name TEXT NOT NULL DEFAULT '',
				notify_name TEXT NOT NULL DEFAULT '',
				phone TEXT NOT NULL DEFAULT '',
				updated_at INTEGER NOT NULL
			)`,
			`CREATE TABLE IF NOT EXISTS media_metadata (
				message_id TEXT PRIMARY KEY REFERENCES messages(id) ON DELETE CASCADE,
				mime_type TEXT NOT NULL DEFAULT '',
				file_name TEXT NOT NULL DEFAULT '',
				size_bytes INTEGER NOT NULL DEFAULT 0,
				local_path TEXT NOT NULL DEFAULT '',
				thumbnail_path TEXT NOT NULL DEFAULT '',
				download_state TEXT NOT NULL DEFAULT '',
				updated_at INTEGER NOT NULL
			)`,
			`CREATE TABLE IF NOT EXISTS ui_snapshots (
				kind TEXT NOT NULL,
				name TEXT NOT NULL,
				chat_id TEXT NOT NULL DEFAULT '',
				value TEXT NOT NULL DEFAULT '',
				updated_at INTEGER NOT NULL,
				PRIMARY KEY (kind, name, chat_id)
			)`,
		},
	},
	{
		name: "0003_local_delete_and_profile_media",
		sql: []string{
			`ALTER TABLE messages ADD COLUMN deleted_at INTEGER NOT NULL DEFAULT 0`,
			`ALTER TABLE messages ADD COLUMN deleted_reason TEXT NOT NULL DEFAULT ''`,
			`ALTER TABLE contacts ADD COLUMN avatar_path TEXT NOT NULL DEFAULT ''`,
			`ALTER TABLE contacts ADD COLUMN avatar_thumb_path TEXT NOT NULL DEFAULT ''`,
			`ALTER TABLE contacts ADD COLUMN avatar_updated_at INTEGER NOT NULL DEFAULT 0`,
			`CREATE INDEX IF NOT EXISTS messages_visible_chat_time_idx
				ON messages (chat_id, deleted_at, timestamp_unix ASC, id ASC)`,
		},
	},
	{
		name: "0004_media_download_descriptors",
		sql: []string{
			`CREATE TABLE IF NOT EXISTS media_download_descriptors (
				message_id TEXT PRIMARY KEY REFERENCES messages(id) ON DELETE CASCADE,
				kind TEXT NOT NULL DEFAULT '',
				url TEXT NOT NULL DEFAULT '',
				direct_path TEXT NOT NULL DEFAULT '',
				media_key BLOB NOT NULL DEFAULT X'',
				file_sha256 BLOB NOT NULL DEFAULT X'',
				file_enc_sha256 BLOB NOT NULL DEFAULT X'',
				file_length INTEGER NOT NULL DEFAULT 0,
				updated_at INTEGER NOT NULL
			)`,
		},
	},
	{
		name: "0005_chat_title_source",
		sql: []string{
			`ALTER TABLE chats ADD COLUMN title_source TEXT NOT NULL DEFAULT ''`,
		},
	},
	{
		name: "0006_message_interactions",
		sql: []string{
			`CREATE TABLE IF NOT EXISTS message_reactions (
				message_id TEXT NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
				sender_jid TEXT NOT NULL,
				emoji TEXT NOT NULL DEFAULT '',
				timestamp_unix INTEGER NOT NULL DEFAULT 0,
				is_outgoing INTEGER NOT NULL DEFAULT 0,
				updated_at INTEGER NOT NULL,
				PRIMARY KEY (message_id, sender_jid)
			)`,
			`CREATE INDEX IF NOT EXISTS message_reactions_message_idx
				ON message_reactions (message_id, updated_at ASC, sender_jid ASC)`,
		},
	},
	{
		name: "0007_chat_avatars_and_sticker_metadata",
		sql: []string{
			`ALTER TABLE chats ADD COLUMN avatar_id TEXT NOT NULL DEFAULT ''`,
			`ALTER TABLE chats ADD COLUMN avatar_path TEXT NOT NULL DEFAULT ''`,
			`ALTER TABLE chats ADD COLUMN avatar_thumb_path TEXT NOT NULL DEFAULT ''`,
			`ALTER TABLE chats ADD COLUMN avatar_updated_at INTEGER NOT NULL DEFAULT 0`,
			`ALTER TABLE media_metadata ADD COLUMN media_kind TEXT NOT NULL DEFAULT ''`,
			`ALTER TABLE media_metadata ADD COLUMN is_animated INTEGER NOT NULL DEFAULT 0`,
			`ALTER TABLE media_metadata ADD COLUMN is_lottie INTEGER NOT NULL DEFAULT 0`,
			`ALTER TABLE media_metadata ADD COLUMN accessibility_label TEXT NOT NULL DEFAULT ''`,
		},
	},
	{
		name: "0008_message_edits",
		sql: []string{
			`ALTER TABLE messages ADD COLUMN edited_at INTEGER NOT NULL DEFAULT 0`,
		},
	},
}

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite database: %w", err)
	}

	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	store := &Store{
		db:   db,
		path: path,
	}

	if err := store.initialize(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}

	return store, nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	db := s.db
	s.db = nil
	return db.Close()
}

func (s *Store) initialize(ctx context.Context) error {
	pragmas := []string{
		`PRAGMA foreign_keys = ON`,
		`PRAGMA journal_mode = WAL`,
		`PRAGMA busy_timeout = 5000`,
		`PRAGMA synchronous = NORMAL`,
		`PRAGMA temp_store = MEMORY`,
		`CREATE TABLE IF NOT EXISTS schema_migrations (
			id INTEGER PRIMARY KEY,
			name TEXT NOT NULL UNIQUE,
			applied_at INTEGER NOT NULL
		)`,
	}

	for _, stmt := range pragmas {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("initialize sqlite pragma/schema: %w", err)
		}
	}

	if err := s.applyMigrations(ctx); err != nil {
		return err
	}

	return nil
}

func (s *Store) applyMigrations(ctx context.Context) error {
	applied := make(map[string]struct{})

	rows, err := s.db.QueryContext(ctx, `SELECT name FROM schema_migrations`)
	if err != nil {
		return fmt.Errorf("query migrations: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return fmt.Errorf("scan migration: %w", err)
		}
		applied[name] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate migrations: %w", err)
	}

	for _, migration := range migrations {
		if _, ok := applied[migration.name]; ok {
			continue
		}

		if err := s.applyMigration(ctx, migration); err != nil {
			return err
		}
	}

	return nil
}

func (s *Store) applyMigration(ctx context.Context, migration migration) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration %s: %w", migration.name, err)
	}

	for _, stmt := range migration.sql {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply migration %s: %w", migration.name, err)
		}
	}

	if _, err := tx.ExecContext(
		ctx,
		`INSERT INTO schema_migrations (name, applied_at) VALUES (?, ?)`,
		migration.name,
		time.Now().Unix(),
	); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("record migration %s: %w", migration.name, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration %s: %w", migration.name, err)
	}

	return nil
}

func (s *Store) Stats(ctx context.Context) (Stats, error) {
	stats := Stats{}

	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM chats`).Scan(&stats.Chats); err != nil {
		return Stats{}, fmt.Errorf("count chats: %w", err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM messages`).Scan(&stats.Messages); err != nil {
		return Stats{}, fmt.Errorf("count messages: %w", err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM drafts`).Scan(&stats.Drafts); err != nil {
		return Stats{}, fmt.Errorf("count drafts: %w", err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM contacts`).Scan(&stats.Contacts); err != nil {
		return Stats{}, fmt.Errorf("count contacts: %w", err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM media_metadata`).Scan(&stats.MediaItems); err != nil {
		return Stats{}, fmt.Errorf("count media metadata: %w", err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations`).Scan(&stats.Migrations); err != nil {
		return Stats{}, fmt.Errorf("count migrations: %w", err)
	}

	return stats, nil
}

func (s *Store) MigrationStatus(ctx context.Context) (applied []string, pending []string, err error) {
	rows, err := s.db.QueryContext(ctx, `SELECT name FROM schema_migrations ORDER BY id ASC`)
	if err != nil {
		return nil, nil, fmt.Errorf("query migrations: %w", err)
	}
	defer rows.Close()

	appliedSet := make(map[string]struct{})
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, nil, fmt.Errorf("scan migration status: %w", err)
		}
		applied = append(applied, name)
		appliedSet[name] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("iterate migration status: %w", err)
	}

	for _, migration := range migrations {
		if _, ok := appliedSet[migration.name]; !ok {
			pending = append(pending, migration.name)
		}
	}

	return applied, pending, nil
}
