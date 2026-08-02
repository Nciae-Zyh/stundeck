package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

const serviceColumns = `
id, name, target_host, target_port, protocol, bind_port, gateway_mode, gateway_address, scheme, publish_mode,
cloudflare_connection_id, entry_hostname, origin_hostname, redirect_status,
preserve_path, preserve_query, manage_dns, enabled, status, last_error,
public_ip, public_port, mapping_changed_at, created_at, updated_at`

func (s *Store) CreateService(ctx context.Context, service Service) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO services (`+serviceColumns+`)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		serviceValues(service)...,
	)
	if err != nil {
		return fmt.Errorf("create service: %w", err)
	}
	return nil
}

func (s *Store) UpdateService(ctx context.Context, service Service) error {
	service.UpdatedAt = time.Now()
	result, err := s.db.ExecContext(ctx, `
UPDATE services SET
  name = ?, target_host = ?, target_port = ?, protocol = ?, bind_port = ?,
  gateway_mode = ?, gateway_address = ?,
  scheme = ?, publish_mode = ?, cloudflare_connection_id = ?, entry_hostname = ?,
  origin_hostname = ?, redirect_status = ?, preserve_path = ?, preserve_query = ?,
  manage_dns = ?, enabled = ?, updated_at = ?
WHERE id = ?`,
		service.Name,
		service.TargetHost,
		service.TargetPort,
		service.Protocol,
		service.BindPort,
		service.GatewayMode,
		service.GatewayAddress,
		service.Scheme,
		service.PublishMode,
		service.CloudflareConnectionID,
		service.EntryHostname,
		service.OriginHostname,
		service.RedirectStatus,
		service.PreservePath,
		service.PreserveQuery,
		service.ManageDNS,
		service.Enabled,
		timeText(service.UpdatedAt),
		service.ID,
	)
	if err != nil {
		return fmt.Errorf("update service: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) Service(ctx context.Context, id string) (Service, error) {
	service, err := scanService(s.db.QueryRowContext(ctx,
		"SELECT "+serviceColumns+" FROM services WHERE id = ?",
		id,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return Service{}, ErrNotFound
	}
	return service, err
}

func (s *Store) Services(ctx context.Context) ([]Service, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+serviceColumns+" FROM services ORDER BY created_at ASC")
	if err != nil {
		return nil, fmt.Errorf("list services: %w", err)
	}
	defer rows.Close()
	services := []Service{}
	for rows.Next() {
		service, err := scanService(rows)
		if err != nil {
			return nil, err
		}
		services = append(services, service)
	}
	return services, rows.Err()
}

func (s *Store) EnabledServices(ctx context.Context) ([]Service, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT "+serviceColumns+" FROM services WHERE enabled = 1 ORDER BY created_at ASC",
	)
	if err != nil {
		return nil, fmt.Errorf("list enabled services: %w", err)
	}
	defer rows.Close()
	services := []Service{}
	for rows.Next() {
		service, err := scanService(rows)
		if err != nil {
			return nil, err
		}
		services = append(services, service)
	}
	return services, rows.Err()
}

func (s *Store) DeleteService(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, "DELETE FROM services WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("delete service: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) SetServiceRuntime(ctx context.Context, id, status, lastError string, enabled bool) error {
	result, err := s.db.ExecContext(ctx, `
UPDATE services SET status = ?, last_error = ?, enabled = ?, updated_at = ? WHERE id = ?`,
		status, lastError, enabled, timeText(time.Now()), id,
	)
	if err != nil {
		return fmt.Errorf("update service runtime: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) SetServiceMapping(ctx context.Context, id, publicIP string, publicPort int) (bool, error) {
	service, err := s.Service(ctx, id)
	if err != nil {
		return false, err
	}
	changed := service.PublicIP != publicIP || service.PublicPort != publicPort
	_, err = s.db.ExecContext(ctx, `
UPDATE services SET public_ip = ?, public_port = ?, mapping_changed_at = ?,
status = 'mapped', last_error = '', updated_at = ? WHERE id = ?`,
		publicIP, publicPort, timeText(time.Now()), timeText(time.Now()), id,
	)
	if err != nil {
		return false, fmt.Errorf("update service mapping: %w", err)
	}
	return changed, nil
}

func serviceValues(service Service) []any {
	var mappingChangedAt any
	if !service.MappingChangedAt.IsZero() {
		mappingChangedAt = timeText(service.MappingChangedAt)
	}
	return []any{
		service.ID,
		service.Name,
		service.TargetHost,
		service.TargetPort,
		service.Protocol,
		service.BindPort,
		service.GatewayMode,
		service.GatewayAddress,
		service.Scheme,
		service.PublishMode,
		service.CloudflareConnectionID,
		service.EntryHostname,
		service.OriginHostname,
		service.RedirectStatus,
		service.PreservePath,
		service.PreserveQuery,
		service.ManageDNS,
		service.Enabled,
		service.Status,
		service.LastError,
		service.PublicIP,
		service.PublicPort,
		mappingChangedAt,
		timeText(service.CreatedAt),
		timeText(service.UpdatedAt),
	}
}

func scanService(scanner rowScanner) (Service, error) {
	var service Service
	var preservePath int
	var preserveQuery int
	var manageDNS int
	var enabled int
	var mappingChangedAt sql.NullString
	var createdAt string
	var updatedAt string
	err := scanner.Scan(
		&service.ID,
		&service.Name,
		&service.TargetHost,
		&service.TargetPort,
		&service.Protocol,
		&service.BindPort,
		&service.GatewayMode,
		&service.GatewayAddress,
		&service.Scheme,
		&service.PublishMode,
		&service.CloudflareConnectionID,
		&service.EntryHostname,
		&service.OriginHostname,
		&service.RedirectStatus,
		&preservePath,
		&preserveQuery,
		&manageDNS,
		&enabled,
		&service.Status,
		&service.LastError,
		&service.PublicIP,
		&service.PublicPort,
		&mappingChangedAt,
		&createdAt,
		&updatedAt,
	)
	if err != nil {
		return Service{}, err
	}
	service.PreservePath = preservePath != 0
	service.PreserveQuery = preserveQuery != 0
	service.ManageDNS = manageDNS != 0
	service.Enabled = enabled != 0
	if mappingChangedAt.Valid {
		service.MappingChangedAt = parseTime(mappingChangedAt.String)
	}
	service.CreatedAt = parseTime(createdAt)
	service.UpdatedAt = parseTime(updatedAt)
	return service, nil
}
