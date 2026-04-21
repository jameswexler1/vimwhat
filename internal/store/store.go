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
	return s.db.Close()
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

	return stats, nil
}
