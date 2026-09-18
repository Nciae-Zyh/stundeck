package store

import (
	"context"
	"database/sql"
	"errors"
)

// Remote IDs are ownership records, not credentials. They survive restarts and
// partial API failures, so retries never adopt another application's resources.
type CloudflareResource struct {
	ConnectionID string `json:"connectionId"`
	OwnerID      string `json:"ownerId"`
	Kind         string `json:"kind"`
	RemoteID     string `json:"remoteId"`
	Hostname     string `json:"hostname"`
}

func (s *Store) SaveCloudflareResource(ctx context.Context, r CloudflareResource) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO cloudflare_resources
	(connection_id, owner_id, kind, remote_id, hostname) VALUES (?, ?, ?, ?, ?)
	ON CONFLICT(connection_id, owner_id, kind) DO UPDATE SET remote_id=excluded.remote_id, hostname=excluded.hostname`,
		r.ConnectionID, r.OwnerID, r.Kind, r.RemoteID, r.Hostname)
	return err
}

func (s *Store) CloudflareResource(ctx context.Context, connectionID, ownerID, kind string) (CloudflareResource, error) {
	var r CloudflareResource
	err := s.db.QueryRowContext(ctx, `SELECT connection_id, owner_id, kind, remote_id, hostname
	FROM cloudflare_resources WHERE connection_id=? AND owner_id=? AND kind=?`, connectionID, ownerID, kind).
		Scan(&r.ConnectionID, &r.OwnerID, &r.Kind, &r.RemoteID, &r.Hostname)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return r, err
}

func (s *Store) CloudflareResources(ctx context.Context, connectionID string) ([]CloudflareResource, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT connection_id, owner_id, kind, remote_id, hostname
	FROM cloudflare_resources WHERE connection_id=?`, connectionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []CloudflareResource{}
	for rows.Next() {
		var r CloudflareResource
		if err := rows.Scan(&r.ConnectionID, &r.OwnerID, &r.Kind, &r.RemoteID, &r.Hostname); err != nil {
			return nil, err
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

func (s *Store) DeleteCloudflareResource(ctx context.Context, connectionID, ownerID, kind string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM cloudflare_resources WHERE connection_id=? AND owner_id=? AND kind=?`, connectionID, ownerID, kind)
	return err
}

func (s *Store) ensureCloudflareColumns(ctx context.Context) error {
	for _, column := range []string{"private_network", "runtime_url"} {
		var count int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info('services') WHERE name=?`, column).Scan(&count); err != nil {
			return err
		}
		if count == 0 {
			if _, err := s.db.ExecContext(ctx, `ALTER TABLE services ADD COLUMN `+column+` TEXT NOT NULL DEFAULT ''`); err != nil {
				return err
			}
		}
	}
	// A separate migration marker allows upgrades from all existing installations.
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info('services') WHERE name='tunnel_protocol'`).Scan(&count); err != nil {
		return err
	}
	if count == 0 {
		if _, err := s.db.ExecContext(ctx, `ALTER TABLE services ADD COLUMN tunnel_protocol TEXT NOT NULL DEFAULT 'http'`); err != nil {
			return err
		}
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info('services') WHERE name='edge_port'`).Scan(&count); err != nil {
		return err
	}
	if count == 0 {
		if _, err := s.db.ExecContext(ctx, `ALTER TABLE services ADD COLUMN edge_port INTEGER NOT NULL DEFAULT 0`); err != nil {
			return err
		}
	}
	return nil
}
