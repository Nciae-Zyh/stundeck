package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestCloudflareFeatureMigrationPreservesExistingServices(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	old := Service{ID: "legacy", Name: "Old NAS", TargetHost: "10.0.0.1", TargetPort: 8080, Protocol: "tcp", PublishMode: "redirect", EntryHostname: "nas.example.com", PublicIP: "203.0.113.1", PublicPort: 12345, Enabled: true, Status: "healthy", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err = db.CreateService(ctx, old); err != nil {
		t.Fatal(err)
	}
	for _, column := range []string{"tunnel_protocol", "edge_port", "private_network", "runtime_url"} {
		if _, err = db.db.Exec("ALTER TABLE services DROP COLUMN " + column); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	loaded, err := db.Service(ctx, old.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.PublicPort != old.PublicPort || loaded.PublishMode != old.PublishMode || !loaded.Enabled || loaded.TunnelProtocol != "http" {
		t.Fatalf("legacy service lost data: %+v", loaded)
	}
	loaded.PublishMode = "tunnel"
	loaded.TunnelProtocol = "ssh"
	loaded.EdgePort = 2222
	loaded.PrivateNetwork = "10.0.0.1/32"
	if err = db.UpdateService(ctx, loaded); err != nil {
		t.Fatal(err)
	}
	loaded, err = db.Service(ctx, old.ID)
	if err != nil || loaded.TunnelProtocol != "ssh" || loaded.EdgePort != 2222 || loaded.PrivateNetwork != "10.0.0.1/32" {
		t.Fatalf("new options not persisted: %+v %v", loaded, err)
	}
}
func TestCloudflareResourcesPreventConnectionDeletion(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if err = db.UpsertCloudflareConnection(ctx, CloudflareConnection{ID: "conn", Name: "test", CreatedAt: time.Now(), UpdatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	r := CloudflareResource{ConnectionID: "conn", OwnerID: "nas.example.com", Kind: "access:accounts", RemoteID: "app", Hostname: "nas.example.com"}
	if err = db.SaveCloudflareResource(ctx, r); err != nil {
		t.Fatal(err)
	}
	if err = db.DeleteCloudflareConnection(ctx, "conn"); err == nil {
		t.Fatal("deleted connection with owned Access app")
	}
	if err = db.DeleteCloudflareResource(ctx, r.ConnectionID, r.OwnerID, r.Kind); err != nil {
		t.Fatal(err)
	}
	if err = db.DeleteCloudflareConnection(ctx, "conn"); err != nil {
		t.Fatal(err)
	}
}
