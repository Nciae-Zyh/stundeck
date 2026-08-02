package store

import (
	"context"
	"fmt"
)

func (s *Store) AddEvent(ctx context.Context, event Event) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO events (id, service_id, type, level, message, payload, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?)`,
		event.ID,
		event.ServiceID,
		event.Type,
		event.Level,
		event.Message,
		encodePayload(event.Payload),
		timeText(event.CreatedAt),
	)
	if err != nil {
		return fmt.Errorf("add event: %w", err)
	}
	return nil
}

func (s *Store) Events(ctx context.Context, limit int) ([]Event, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id, service_id, type, level, message, payload, created_at
FROM events ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}
	defer rows.Close()
	events := []Event{}
	for rows.Next() {
		var event Event
		var payload string
		var createdAt string
		if err := rows.Scan(
			&event.ID,
			&event.ServiceID,
			&event.Type,
			&event.Level,
			&event.Message,
			&payload,
			&createdAt,
		); err != nil {
			return nil, err
		}
		event.Payload = decodePayload(payload)
		event.CreatedAt = parseTime(createdAt)
		events = append(events, event)
	}
	return events, rows.Err()
}
