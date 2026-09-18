package httpapi

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Nciae-Zyh/stundeck/internal/engine"
	"github.com/Nciae-Zyh/stundeck/internal/security"
	"github.com/Nciae-Zyh/stundeck/internal/store"
	"github.com/Nciae-Zyh/stundeck/internal/webhook"
)

func featureServer(t *testing.T) *Server {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	cipher, err := security.LoadOrCreateCipher(filepath.Join(dir, "key"))
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	err = db.UpsertCloudflareConnection(context.Background(), store.CloudflareConnection{ID: "conn", Name: "Cloudflare", ZoneID: "zone-1", ZoneName: "example.com", CreatedAt: time.Now(), UpdatedAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	return New(Config{Store: db, Cipher: cipher, Engine: engine.NewManager(engine.Config{Binary: "missing"}, db, logger), Logger: logger, Webhooks: webhook.NewDispatcher(db, cipher, logger), SessionTTL: time.Hour})
}
func TestPublishingRequestValidation(t *testing.T) {
	s := featureServer(t)
	base := serviceRequest{Name: "NAS", TargetHost: "10.0.0.1", TargetPort: 8080, Protocol: "tcp", PublishMode: "tunnel", CloudflareConnectionID: "conn", EntryHostname: "nas.example.com", TunnelProtocol: "ssh", GatewayMode: "upnp", BindPort: 12345}
	svc, err := s.serviceFromRequest(context.Background(), base)
	if err != nil {
		t.Fatal(err)
	}
	if svc.GatewayMode != "none" || svc.BindPort != 0 || svc.TunnelProtocol != "ssh" {
		t.Fatal(svc)
	}
	for _, tc := range []struct {
		name   string
		change func(*serviceRequest)
	}{
		{"public UDP tunnel", func(r *serviceRequest) { r.Protocol = "udp" }},
		{"zone suffix collision", func(r *serviceRequest) { r.EntryHostname = "nas.badexample.com" }},
		{"outside zone", func(r *serviceRequest) { r.EntryHostname = "example.com.attacker.test" }},
		{"invalid tunnel protocol", func(r *serviceRequest) { r.TunnelProtocol = "file" }},
		{"spectrum without port", func(r *serviceRequest) { r.PublishMode = "spectrum"; r.EdgePort = 0 }},
		{"UDP proxy", func(r *serviceRequest) { r.PublishMode = "proxy"; r.Protocol = "udp" }},
		{"Quick SSH", func(r *serviceRequest) { r.PublishMode = "quick" }},
		{"WARP public target", func(r *serviceRequest) { r.PublishMode = "warp"; r.TargetHost = "8.8.8.8" }},
		{"WARP default route", func(r *serviceRequest) { r.PublishMode = "warp"; r.PrivateNetwork = "0.0.0.0/0" }},
		{"Worker no origin", func(r *serviceRequest) { r.PublishMode = "workers" }},
		{"Worker foreign origin", func(r *serviceRequest) { r.PublishMode = "workers"; r.OriginHostname = "origin.external.test" }},
		{"outside managed origin", func(r *serviceRequest) {
			r.PublishMode = "redirect"
			r.OriginHostname = "origin.other.test"
			r.ManageDNS = true
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := base
			tc.change(&input)
			if _, err := s.serviceFromRequest(context.Background(), input); err == nil {
				t.Fatal("invalid request accepted")
			}
		})
	}
}

func TestQuickAndWARPDoNotRequirePublicDomains(t *testing.T) {
	s := featureServer(t)
	base := serviceRequest{Name: "NAS", TargetHost: "10.0.0.1", TargetPort: 8080, Protocol: "tcp", TunnelProtocol: "http", PublishMode: "quick"}
	svc, err := s.serviceFromRequest(context.Background(), base)
	if err != nil || svc.UsesCloudflareAccount() || !svc.UsesConnector() {
		t.Fatalf("Quick Tunnel rejected: %+v %v", svc, err)
	}
	base.PublishMode, base.Protocol, base.CloudflareConnectionID = "warp", "udp", "conn"
	svc, err = s.serviceFromRequest(context.Background(), base)
	if err != nil || svc.PrivateNetwork != "10.0.0.1/32" || svc.ManageDNS || svc.EntryHostname != "" {
		t.Fatalf("private UDP rejected: %+v %v", svc, err)
	}
}
func TestCloudflareFeatureEndpointsRequireAuthentication(t *testing.T) {
	s := featureServer(t)
	for _, tc := range []struct{ method, path string }{{"POST", "/api/v1/cloudflare/inspect"}, {"GET", "/api/v1/cloudflare/connections/conn/inspect"}, {"PUT", "/api/v1/cloudflare/connections/conn/access"}, {"DELETE", "/api/v1/cloudflare/connections/conn/access"}, {"POST", "/api/v1/services/svc/cleanup"}} {
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(`{}`))
		req.RemoteAddr = "127.0.0.1:1234"
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("unauthenticated %s: %d", tc.path, w.Code)
		}
	}
}
func TestStoppedServiceSyncCannotReenableIt(t *testing.T) {
	s := featureServer(t)
	if _, err := s.syncCloudflare(context.Background(), store.Service{PublishMode: "proxy", Enabled: false}); err == nil {
		t.Fatal("stopped mapping service was synchronized")
	}
}
