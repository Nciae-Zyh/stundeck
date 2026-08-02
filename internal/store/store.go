package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

var ErrNotFound = errors.New("not found")

type Store struct {
	db *sql.DB
}

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create database directory: %w", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	db.SetMaxOpenConns(1)
	store := &Store{db: db}
	if err := store.migrate(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) migrate(ctx context.Context) error {
	const schema = `
PRAGMA journal_mode=WAL;
PRAGMA foreign_keys=ON;
PRAGMA busy_timeout=5000;

CREATE TABLE IF NOT EXISTS users (
  id TEXT PRIMARY KEY,
  username TEXT NOT NULL UNIQUE,
  password_hash TEXT NOT NULL,
  totp_secret_ciphertext TEXT NOT NULL DEFAULT '',
  totp_enabled INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS sessions (
  token_hash TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  csrf_token TEXT NOT NULL,
  expires_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS cloudflare_connections (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  token_ciphertext TEXT NOT NULL,
  zone_id TEXT NOT NULL,
  zone_name TEXT NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS services (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  target_host TEXT NOT NULL,
  target_port INTEGER NOT NULL,
  protocol TEXT NOT NULL,
  bind_port INTEGER NOT NULL DEFAULT 0,
  scheme TEXT NOT NULL DEFAULT 'http',
  publish_mode TEXT NOT NULL DEFAULT 'direct',
  cloudflare_connection_id TEXT NOT NULL DEFAULT '',
  entry_hostname TEXT NOT NULL DEFAULT '',
  origin_hostname TEXT NOT NULL DEFAULT '',
  redirect_status INTEGER NOT NULL DEFAULT 302,
  preserve_path INTEGER NOT NULL DEFAULT 1,
  preserve_query INTEGER NOT NULL DEFAULT 1,
  manage_dns INTEGER NOT NULL DEFAULT 0,
  enabled INTEGER NOT NULL DEFAULT 0,
  status TEXT NOT NULL DEFAULT 'stopped',
  last_error TEXT NOT NULL DEFAULT '',
  public_ip TEXT NOT NULL DEFAULT '',
  public_port INTEGER NOT NULL DEFAULT 0,
  mapping_changed_at TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS events (
  id TEXT PRIMARY KEY,
  service_id TEXT NOT NULL DEFAULT '',
  type TEXT NOT NULL,
  level TEXT NOT NULL,
  message TEXT NOT NULL,
  payload TEXT NOT NULL DEFAULT '{}',
  created_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_events_created_at ON events(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_events_service_id ON events(service_id, created_at DESC);

CREATE TABLE IF NOT EXISTS webhooks (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  url TEXT NOT NULL,
  secret_ciphertext TEXT NOT NULL,
  allow_private INTEGER NOT NULL DEFAULT 0,
  enabled INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS webhook_deliveries (
  id TEXT PRIMARY KEY,
  webhook_id TEXT NOT NULL REFERENCES webhooks(id) ON DELETE CASCADE,
  event_id TEXT NOT NULL,
  payload TEXT NOT NULL,
  attempts INTEGER NOT NULL DEFAULT 0,
  next_attempt_at TEXT NOT NULL,
  delivered_at TEXT,
  last_error TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_webhook_deliveries_pending
ON webhook_deliveries(delivered_at, next_attempt_at);

CREATE TABLE IF NOT EXISTS settings (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
`
	if _, err := s.db.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("migrate database: %w", err)
	}
	if err := s.ensureUserSecurityColumns(ctx); err != nil {
		return err
	}
	return nil
}

func (s *Store) ensureUserSecurityColumns(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, "PRAGMA table_info(users)")
	if err != nil {
		return fmt.Errorf("inspect users schema: %w", err)
	}
	columns := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull int
		var defaultValue any
		var primaryKey int
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			rows.Close()
			return fmt.Errorf("scan users schema: %w", err)
		}
		columns[name] = true
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close users schema rows: %w", err)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate users schema: %w", err)
	}
	if !columns["totp_secret_ciphertext"] {
		if _, err := s.db.ExecContext(ctx, "ALTER TABLE users ADD COLUMN totp_secret_ciphertext TEXT NOT NULL DEFAULT ''"); err != nil {
			return fmt.Errorf("add totp secret column: %w", err)
		}
	}
	if !columns["totp_enabled"] {
		if _, err := s.db.ExecContext(ctx, "ALTER TABLE users ADD COLUMN totp_enabled INTEGER NOT NULL DEFAULT 0"); err != nil {
			return fmt.Errorf("add totp enabled column: %w", err)
		}
	}
	return nil
}

func timeText(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}

func parseTime(value string) time.Time {
	parsed, _ := time.Parse(time.RFC3339Nano, value)
	return parsed
}

func encodePayload(payload map[string]any) string {
	if payload == nil {
		return "{}"
	}
	encoded, _ := json.Marshal(payload)
	return string(encoded)
}

func decodePayload(value string) map[string]any {
	payload := map[string]any{}
	_ = json.Unmarshal([]byte(value), &payload)
	return payload
}
