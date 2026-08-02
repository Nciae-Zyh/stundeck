package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

func (s *Store) CreateWebhook(ctx context.Context, webhook Webhook) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO webhooks
  (id, name, url, secret_ciphertext, allow_private, enabled, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		webhook.ID,
		webhook.Name,
		webhook.URL,
		webhook.SecretCiphertext,
		webhook.AllowPrivate,
		webhook.Enabled,
		timeText(webhook.CreatedAt),
		timeText(webhook.UpdatedAt),
	)
	if err != nil {
		return fmt.Errorf("create webhook: %w", err)
	}
	return nil
}

func (s *Store) Webhooks(ctx context.Context) ([]Webhook, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, name, url, secret_ciphertext, allow_private, enabled, created_at, updated_at
FROM webhooks ORDER BY created_at ASC`)
	if err != nil {
		return nil, fmt.Errorf("list webhooks: %w", err)
	}
	defer rows.Close()
	webhooks := []Webhook{}
	for rows.Next() {
		webhook, err := scanWebhook(rows)
		if err != nil {
			return nil, err
		}
		webhooks = append(webhooks, webhook)
	}
	return webhooks, rows.Err()
}

func (s *Store) Webhook(ctx context.Context, id string) (Webhook, error) {
	webhook, err := scanWebhook(s.db.QueryRowContext(ctx, `
SELECT id, name, url, secret_ciphertext, allow_private, enabled, created_at, updated_at
FROM webhooks WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Webhook{}, ErrNotFound
	}
	return webhook, err
}

func (s *Store) DeleteWebhook(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, "DELETE FROM webhooks WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("delete webhook: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return ErrNotFound
	}
	return nil
}

func scanWebhook(scanner rowScanner) (Webhook, error) {
	var webhook Webhook
	var allowPrivate int
	var enabled int
	var createdAt string
	var updatedAt string
	if err := scanner.Scan(
		&webhook.ID,
		&webhook.Name,
		&webhook.URL,
		&webhook.SecretCiphertext,
		&allowPrivate,
		&enabled,
		&createdAt,
		&updatedAt,
	); err != nil {
		return Webhook{}, err
	}
	webhook.AllowPrivate = allowPrivate != 0
	webhook.Enabled = enabled != 0
	webhook.CreatedAt = parseTime(createdAt)
	webhook.UpdatedAt = parseTime(updatedAt)
	return webhook, nil
}

func (s *Store) QueueWebhookDelivery(ctx context.Context, delivery WebhookDelivery) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO webhook_deliveries
  (id, webhook_id, event_id, payload, attempts, next_attempt_at, delivered_at, last_error, created_at)
VALUES (?, ?, ?, ?, ?, ?, NULL, ?, ?)`,
		delivery.ID,
		delivery.WebhookID,
		delivery.EventID,
		delivery.Payload,
		delivery.Attempts,
		timeText(delivery.NextAttemptAt),
		delivery.LastError,
		timeText(delivery.CreatedAt),
	)
	if err != nil {
		return fmt.Errorf("queue webhook delivery: %w", err)
	}
	return nil
}

func (s *Store) PendingWebhookDeliveries(ctx context.Context, limit int) ([]WebhookDelivery, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id, webhook_id, event_id, payload, attempts, next_attempt_at,
       COALESCE(delivered_at, ''), last_error, created_at
FROM webhook_deliveries
WHERE delivered_at IS NULL AND next_attempt_at <= ?
ORDER BY next_attempt_at ASC LIMIT ?`, timeText(time.Now()), limit)
	if err != nil {
		return nil, fmt.Errorf("list pending webhook deliveries: %w", err)
	}
	defer rows.Close()
	deliveries := []WebhookDelivery{}
	for rows.Next() {
		var delivery WebhookDelivery
		var nextAttemptAt string
		var deliveredAt string
		var createdAt string
		if err := rows.Scan(
			&delivery.ID,
			&delivery.WebhookID,
			&delivery.EventID,
			&delivery.Payload,
			&delivery.Attempts,
			&nextAttemptAt,
			&deliveredAt,
			&delivery.LastError,
			&createdAt,
		); err != nil {
			return nil, err
		}
		delivery.NextAttemptAt = parseTime(nextAttemptAt)
		if deliveredAt != "" {
			delivery.DeliveredAt = parseTime(deliveredAt)
		}
		delivery.CreatedAt = parseTime(createdAt)
		deliveries = append(deliveries, delivery)
	}
	return deliveries, rows.Err()
}

func (s *Store) MarkWebhookDelivered(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `
UPDATE webhook_deliveries SET delivered_at = ?, last_error = '' WHERE id = ?`,
		timeText(time.Now()), id,
	)
	if err != nil {
		return fmt.Errorf("mark webhook delivered: %w", err)
	}
	return nil
}

func (s *Store) RetryWebhookDelivery(ctx context.Context, id, lastError string, attempts int, next time.Time) error {
	_, err := s.db.ExecContext(ctx, `
UPDATE webhook_deliveries
SET attempts = ?, last_error = ?, next_attempt_at = ? WHERE id = ?`,
		attempts, lastError, timeText(next), id,
	)
	if err != nil {
		return fmt.Errorf("retry webhook delivery: %w", err)
	}
	return nil
}
