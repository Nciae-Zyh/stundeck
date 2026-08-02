package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

func (s *Store) HasAdmin(ctx context.Context) (bool, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM users").Scan(&count); err != nil {
		return false, fmt.Errorf("count users: %w", err)
	}
	return count > 0, nil
}

func (s *Store) CreateAdmin(ctx context.Context, user User) error {
	result, err := s.db.ExecContext(ctx, `
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
	return nil
}

func (s *Store) UserByUsername(ctx context.Context, username string) (User, error) {
	return s.user(ctx, "username = ?", username)
}

func (s *Store) User(ctx context.Context, id string) (User, error) {
	return s.user(ctx, "id = ?", id)
}

func (s *Store) user(ctx context.Context, predicate, value string) (User, error) {
	var user User
	var totpEnabled int
	var createdAt string
	err := s.db.QueryRowContext(ctx, `
SELECT id, username, password_hash, totp_secret_ciphertext, totp_enabled, created_at
FROM users WHERE `+predicate,
		value,
	).Scan(&user.ID, &user.Username, &user.PasswordHash, &user.TOTPSecretCiphertext, &totpEnabled, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("get user: %w", err)
	}
	user.TOTPEnabled = totpEnabled != 0
	user.CreatedAt = parseTime(createdAt)
	return user, nil
}

func (s *Store) SetUserTOTP(ctx context.Context, userID, ciphertext string, enabled bool) error {
	result, err := s.db.ExecContext(ctx, `
UPDATE users SET totp_secret_ciphertext = ?, totp_enabled = ? WHERE id = ?`,
		ciphertext, enabled, userID,
	)
	if err != nil {
		return fmt.Errorf("update user totp: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("check updated user totp: %w", err)
	}
	if affected != 1 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) CreateSession(ctx context.Context, session Session) error {
	hash := sha256.Sum256([]byte(session.Token))
	_, err := s.db.ExecContext(ctx,
		"INSERT INTO sessions (token_hash, user_id, csrf_token, expires_at) VALUES (?, ?, ?, ?)",
		hash[:], session.UserID, session.CSRFToken, timeText(session.ExpiresAt),
	)
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	return nil
}

func (s *Store) Session(ctx context.Context, token string) (Session, error) {
	hash := sha256.Sum256([]byte(token))
	var session Session
	var expiresAt string
	err := s.db.QueryRowContext(ctx,
		"SELECT user_id, csrf_token, expires_at FROM sessions WHERE token_hash = ?",
		hash[:],
	).Scan(&session.UserID, &session.CSRFToken, &expiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	if err != nil {
		return Session{}, fmt.Errorf("get session: %w", err)
	}
	session.Token = token
	session.ExpiresAt = parseTime(expiresAt)
	if time.Now().After(session.ExpiresAt) {
		_ = s.DeleteSession(ctx, token)
		return Session{}, ErrNotFound
	}
	return session, nil
}

func (s *Store) DeleteSession(ctx context.Context, token string) error {
	hash := sha256.Sum256([]byte(token))
	if _, err := s.db.ExecContext(ctx, "DELETE FROM sessions WHERE token_hash = ?", hash[:]); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

func (s *Store) DeleteExpiredSessions(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, "DELETE FROM sessions WHERE expires_at <= ?", timeText(time.Now())); err != nil {
		return fmt.Errorf("delete expired sessions: %w", err)
	}
	return nil
}
