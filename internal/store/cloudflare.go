package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

func (s *Store) UpsertCloudflareConnection(ctx context.Context, connection CloudflareConnection) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO cloudflare_connections
  (id, name, token_ciphertext, zone_id, zone_name, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
  name = excluded.name,
  token_ciphertext = excluded.token_ciphertext,
  zone_id = excluded.zone_id,
  zone_name = excluded.zone_name,
  updated_at = excluded.updated_at`,
		connection.ID,
		connection.Name,
		connection.TokenCiphertext,
		connection.ZoneID,
		connection.ZoneName,
		timeText(connection.CreatedAt),
		timeText(connection.UpdatedAt),
	)
	if err != nil {
		return fmt.Errorf("save cloudflare connection: %w", err)
	}
	return nil
}

func (s *Store) CloudflareConnections(ctx context.Context) ([]CloudflareConnection, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, name, token_ciphertext, zone_id, zone_name, created_at, updated_at
FROM cloudflare_connections ORDER BY created_at ASC`)
	if err != nil {
		return nil, fmt.Errorf("list cloudflare connections: %w", err)
	}
	defer rows.Close()
	connections := []CloudflareConnection{}
	for rows.Next() {
		connection, err := scanCloudflareConnection(rows)
		if err != nil {
			return nil, err
		}
		connections = append(connections, connection)
	}
	return connections, rows.Err()
}

func (s *Store) CloudflareConnection(ctx context.Context, id string) (CloudflareConnection, error) {
	connection, err := scanCloudflareConnection(s.db.QueryRowContext(ctx, `
SELECT id, name, token_ciphertext, zone_id, zone_name, created_at, updated_at
FROM cloudflare_connections WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return CloudflareConnection{}, ErrNotFound
	}
	return connection, err
}

func (s *Store) DeleteCloudflareConnection(ctx context.Context, id string) error {
	var count int
	if err := s.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM services WHERE cloudflare_connection_id = ?",
		id,
	).Scan(&count); err != nil {
		return fmt.Errorf("check cloudflare connection usage: %w", err)
	}
	if count > 0 {
		return errors.New("cloudflare connection is still used by a service")
	}
	if _, err := s.db.ExecContext(ctx, "DELETE FROM cloudflare_connections WHERE id = ?", id); err != nil {
		return fmt.Errorf("delete cloudflare connection: %w", err)
	}
	return nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanCloudflareConnection(scanner rowScanner) (CloudflareConnection, error) {
	var connection CloudflareConnection
	var createdAt string
	var updatedAt string
	err := scanner.Scan(
		&connection.ID,
		&connection.Name,
		&connection.TokenCiphertext,
		&connection.ZoneID,
		&connection.ZoneName,
		&createdAt,
		&updatedAt,
	)
	if err != nil {
		return CloudflareConnection{}, err
	}
	connection.CreatedAt = parseTime(createdAt)
	connection.UpdatedAt = parseTime(updatedAt)
	return connection, nil
}
