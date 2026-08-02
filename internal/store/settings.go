package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

const accessPolicyKey = "access_policy"

func DefaultAccessPolicy() AccessPolicy {
	return AccessPolicy{Mode: "lan", AllowedHosts: []string{}}
}

func (s *Store) AccessPolicy(ctx context.Context) (AccessPolicy, error) {
	var value string
	err := s.db.QueryRowContext(ctx, "SELECT value FROM settings WHERE key = ?", accessPolicyKey).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return DefaultAccessPolicy(), nil
	}
	if err != nil {
		return AccessPolicy{}, fmt.Errorf("load access policy: %w", err)
	}
	var policy AccessPolicy
	if err := json.Unmarshal([]byte(value), &policy); err != nil {
		return AccessPolicy{}, fmt.Errorf("decode access policy: %w", err)
	}
	return policy, nil
}

func (s *Store) InitializeAdmin(ctx context.Context, user User, policy AccessPolicy) error {
	value, err := json.Marshal(policy)
	if err != nil {
		return fmt.Errorf("encode access policy: %w", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin initialization: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
INSERT INTO users (id, username, password_hash, created_at)
SELECT ?, ?, ?, ?
WHERE NOT EXISTS (SELECT 1 FROM users)`,
		user.ID, user.Username, user.PasswordHash, timeText(user.CreatedAt),
	)
	if err != nil {
		return fmt.Errorf("create admin: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("check created admin: %w", err)
	}
	if affected != 1 {
		return errors.New("admin already exists")
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		accessPolicyKey, string(value), timeText(time.Now()),
	); err != nil {
		return fmt.Errorf("save access policy: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit initialization: %w", err)
	}
	return nil
}

func (s *Store) SetAccessPolicy(ctx context.Context, policy AccessPolicy) error {
	value, err := json.Marshal(policy)
	if err != nil {
		return fmt.Errorf("encode access policy: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		accessPolicyKey, string(value), timeText(time.Now()),
	)
	if err != nil {
		return fmt.Errorf("save access policy: %w", err)
	}
	return nil
}
