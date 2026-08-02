package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestStoreServiceMappingLifecycle(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "stundeck.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	ctx := context.Background()
	now := time.Now()
	service := Service{
		ID: "service-1", Name: "NAS", TargetHost: "192.168.1.20", TargetPort: 8080,
		Protocol: "tcp", Scheme: "http", PublishMode: "direct", RedirectStatus: 302,
		PreservePath: true, PreserveQuery: true, Status: "stopped", CreatedAt: now, UpdatedAt: now,
	}
	if err := database.CreateService(ctx, service); err != nil {
		t.Fatal(err)
	}
	changed, err := database.SetServiceMapping(ctx, service.ID, "203.0.113.10", 45678)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("first mapping was not marked changed")
	}
	changed, err = database.SetServiceMapping(ctx, service.ID, "203.0.113.10", 45678)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("unchanged mapping was marked changed")
	}
	stored, err := database.Service(ctx, service.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.PublicIP != "203.0.113.10" || stored.PublicPort != 45678 || stored.Status != "mapped" {
		t.Fatalf("unexpected stored service: %#v", stored)
	}
}

func TestCreateAdminOnlyAllowsFirstUser(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "stundeck.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	ctx := context.Background()
	first := User{ID: "admin-1", Username: "admin", PasswordHash: "hash", CreatedAt: time.Now()}
	if err := database.CreateAdmin(ctx, first); err != nil {
		t.Fatal(err)
	}
	second := User{ID: "admin-2", Username: "other", PasswordHash: "hash", CreatedAt: time.Now()}
	if err := database.CreateAdmin(ctx, second); err == nil {
		t.Fatal("second administrator was accepted")
	}
}
