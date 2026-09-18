package cloudflare

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Nciae-Zyh/stundeck/internal/security"
	"github.com/Nciae-Zyh/stundeck/internal/store"
)

func cfReply(w http.ResponseWriter, status int, result any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"success": status < 400, "result": result, "errors": []map[string]any{{"code": 10000, "message": "fixture response"}}})
}
func testZone() Zone {
	var zone Zone
	_ = json.Unmarshal([]byte(`{"id":"zone-1","name":"example.com","status":"active","account":{"id":"account-1","name":"Example"},"permissions":["#dns_records:edit"]}`), &zone)
	return zone
}
func fixtureClient(t *testing.T, handle func(http.ResponseWriter, *http.Request) bool) (*Client, *[]string) {
	t.Helper()
	calls := []string{}
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, r.Method+" "+r.URL.Path)
		if handle != nil && handle(w, r) {
			return
		}
		switch r.URL.Path {
		case "/zones/zone-1":
			cfReply(w, 200, testZone())
		case "/zones":
			cfReply(w, 200, []Zone{testZone()})
		case "/user/tokens/verify":
			cfReply(w, 200, TokenStatus{ID: "token-1", Status: "active"})
		case "/user/tokens/token-1":
			cfReply(w, 403, nil)
		case "/zones/zone-1/dns_records":
			cfReply(w, 200, []DNSRecord{})
		case "/accounts/account-1/teamnet/routes", "/accounts/account-1/workers/scripts", "/zones/zone-1/workers/routes", "/zones/zone-1/rulesets", "/zones/zone-1/spectrum/apps", "/accounts/account-1/access/apps", "/accounts/account-1/cfd_tunnel":
			cfReply(w, 200, []any{})
		default:
			t.Errorf("unexpected CF call: %s %s", r.Method, r.URL)
			cfReply(w, 500, nil)
		}
	}))
	t.Cleanup(server.Close)
	return NewForTest(server.URL, "fixture-secret", server.Client()), &calls
}
func testPublisher(t *testing.T, client *Client) *Publisher {
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
	encrypted, err := cipher.Encrypt("fixture-secret")
	if err != nil {
		t.Fatal(err)
	}
	err = db.UpsertCloudflareConnection(context.Background(), store.CloudflareConnection{ID: "conn", Name: "fixture", ZoneID: "zone-1", ZoneName: "example.com", TokenCiphertext: encrypted, CreatedAt: time.Now(), UpdatedAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	p := NewPublisher(db, cipher)
	p.NewClient = func(string) *Client { return client }
	return p
}
func testService(mode string) store.Service {
	return store.Service{ID: "svc", Name: "NAS", CloudflareConnectionID: "conn", PublishMode: mode, EntryHostname: "nas.example.com", TargetHost: "192.168.1.2", TargetPort: 8080, Protocol: "tcp", TunnelProtocol: "http", Scheme: "http", PublicIP: "203.0.113.1", PublicPort: 8080, EdgePort: 443, Enabled: true}
}

func TestInspectionDoesNotConfuseReadWithWrite(t *testing.T) {
	c, calls := fixtureClient(t, func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path == "/zones/zone-1/dns_records" {
			cfReply(w, 200, []DNSRecord{{Type: "CNAME", Name: "nas.example.com", Proxied: true}, {Type: "A", Name: "direct.example.com", Proxied: false}})
			return true
		}
		return false
	})
	report, err := c.Inspect(context.Background(), "zone-1")
	if err != nil {
		t.Fatal(err)
	}
	for _, feature := range []string{"tunnel", "access", "redirect", "spectrum"} {
		if report.Capabilities[feature].State != "unverified" {
			t.Fatalf("%s unexpectedly writable: %+v", feature, report.Capabilities[feature])
		}
	}
	if report.Capabilities["dns"].State != "available" {
		t.Fatal("effective DNS write permission not used")
	}
	if len(report.Domains) != 2 || report.Domains[0].AccessEligible || !report.Domains[1].AccessEligible {
		t.Fatalf("domains: %+v", report.Domains)
	}
	for _, call := range *calls {
		if !strings.HasPrefix(call, "GET ") {
			t.Fatalf("inspection mutated provider: %s", call)
		}
	}
}
func TestInspectionPermissionFallbackAndTransientFailure(t *testing.T) {
	c, _ := fixtureClient(t, func(w http.ResponseWriter, r *http.Request) bool {
		switch r.URL.Path {
		case "/accounts/account-1/access/apps":
			cfReply(w, 403, nil)
			return true
		case "/zones/zone-1/access/apps":
			cfReply(w, 200, []any{})
			return true
		case "/accounts/account-1/cfd_tunnel":
			cfReply(w, 503, nil)
			return true
		}
		return false
	})
	report, err := c.Inspect(context.Background(), "zone-1")
	if err != nil {
		t.Fatal(err)
	}
	if report.AccessScope != "zones" || report.Capabilities["access"].State != "unverified" || report.Capabilities["tunnel"].State != "error" {
		t.Fatalf("bad permission classification: %+v", report)
	}
}
func TestTokenPolicyScopeIsNotLeakedAcrossZones(t *testing.T) {
	for _, tc := range []struct {
		resources string
		account   bool
		want      string
	}{
		{`{"com.cloudflare.api.account.zone.other":"*"}`, false, ""},
		{`{"com.cloudflare.api.account.zone.zone-1":"*"}`, false, "write"},
		{`{"com.cloudflare.api.account.other":{"com.cloudflare.api.account.zone.*":"*"}}`, false, ""},
		{`{"com.cloudflare.api.account.account-1":{"com.cloudflare.api.account.zone.*":"*"}}`, false, "write"},
		{`{"com.cloudflare.api.account.account-1":{"com.cloudflare.api.account.zone.*":"*"}}`, true, ""},
		{`{"com.cloudflare.api.account.account-1":"*"}`, true, "write"},
	} {
		var details tokenDetails
		raw := `{"policies":[{"effect":"allow","permission_groups":[{"name":"DNS Write"}],"resources":` + tc.resources + `}]}`
		if err := json.Unmarshal([]byte(raw), &details); err != nil {
			t.Fatal(err)
		}
		if got := details.writeEvidence(testZone(), tc.account, "DNS Write"); got != tc.want {
			t.Errorf("%s: %s, want %s", tc.resources, got, tc.want)
		}
	}
}
func TestZonesAndDNSPagination(t *testing.T) {
	c, _ := fixtureClient(t, func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/zones" && r.URL.Path != "/zones/zone-1/dns_records" {
			return false
		}
		page := r.URL.Query().Get("page")
		var result any = []Zone{{ID: "zone-" + page}}
		if strings.Contains(r.URL.Path, "dns_records") {
			result = []DNSRecord{{ID: page, Type: "A", Name: "host" + page + ".example.com"}}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "result": result, "result_info": map[string]int{"total_pages": 2}})
		return true
	})
	zones, err := c.Zones(context.Background())
	if err != nil || len(zones) != 2 || zones[1].ID != "zone-2" {
		t.Fatalf("zones: %+v %v", zones, err)
	}
	records, err := c.DNSRecords(context.Background(), "zone-1", "")
	if err != nil || len(records) != 2 {
		t.Fatalf("DNS: %+v %v", records, err)
	}
}
func TestTunnelRetriesUsePersistedOwnership(t *testing.T) {
	creates, configs := 0, 0
	var record *DNSRecord
	c, _ := fixtureClient(t, func(w http.ResponseWriter, r *http.Request) bool {
		switch r.URL.Path {
		case "/accounts/account-1/cfd_tunnel":
			if r.Method == "POST" {
				creates++
				cfReply(w, 200, Tunnel{ID: "tunnel-1", Name: "stundeck-svc", ConfigSrc: "cloudflare"})
				return true
			}
		case "/accounts/account-1/cfd_tunnel/tunnel-1":
			cfReply(w, 200, Tunnel{ID: "tunnel-1", Name: "stundeck-svc", ConfigSrc: "cloudflare"})
			return true
		case "/accounts/account-1/cfd_tunnel/tunnel-1/configurations":
			configs++
			var payload struct {
				Config struct {
					Ingress []map[string]any `json:"ingress"`
				} `json:"config"`
			}
			_ = json.NewDecoder(r.Body).Decode(&payload)
			if len(payload.Config.Ingress) != 2 || payload.Config.Ingress[1]["service"] != "http_status:404" {
				t.Errorf("missing catch-all: %+v", payload)
			}
			if configs == 1 {
				cfReply(w, 403, nil)
			} else {
				cfReply(w, 200, map[string]any{})
			}
			return true
		case "/zones/zone-1/dns_records":
			if r.Method == "POST" {
				record = &DNSRecord{}
				_ = json.NewDecoder(r.Body).Decode(record)
				record.ID = "dns-1"
				if record.Type != "CNAME" || record.Content != "tunnel-1.cfargotunnel.com" || !record.Proxied {
					t.Errorf("bad tunnel DNS: %+v", record)
				}
				cfReply(w, 200, record)
				return true
			}
			if record != nil {
				cfReply(w, 200, []DNSRecord{*record})
				return true
			}
		case "/accounts/account-1/cfd_tunnel/tunnel-1/token":
			cfReply(w, 200, "connector-fixture")
			return true
		}
		return false
	})
	p := testPublisher(t, c)
	svc := testService("tunnel")
	if _, err := p.Sync(context.Background(), svc); err == nil {
		t.Fatal("expected first configuration failure")
	}
	if record != nil {
		t.Fatal("DNS was published before ingress succeeded")
	}
	token, err := p.TunnelToken(context.Background(), svc)
	if err != nil || token != "connector-fixture" {
		t.Fatalf("token retry: %s %v", token, err)
	}
	if creates != 1 || configs != 2 {
		t.Fatalf("duplicate tunnel created: %d %d", creates, configs)
	}
	encoded, _ := json.Marshal(svc)
	if strings.Contains(string(encoded), "connector-fixture") {
		t.Fatal("token leaked")
	}
}
func TestPublishingRejectsForeignDNSBeforeTunnelCreation(t *testing.T) {
	c, calls := fixtureClient(t, func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path == "/zones/zone-1/dns_records" {
			cfReply(w, 200, []DNSRecord{{ID: "foreign", Name: "nas.example.com", Type: "A", Comment: "someone else"}})
			return true
		}
		return false
	})
	p := testPublisher(t, c)
	if _, err := p.Sync(context.Background(), testService("tunnel")); err == nil {
		t.Fatal("foreign DNS was accepted")
	}
	for _, call := range *calls {
		if strings.HasPrefix(call, "POST ") {
			t.Fatalf("created resource before conflict validation: %s", call)
		}
	}
}
func TestProxyRejectsUnsupportedPortBeforeDNSMutation(t *testing.T) {
	c, calls := fixtureClient(t, nil)
	p := testPublisher(t, c)
	svc := testService("proxy")
	svc.PublicPort = 54321
	if _, err := p.Sync(context.Background(), svc); err == nil {
		t.Fatal("accepted unsupported proxy port")
	}
	for _, call := range *calls {
		if !strings.HasPrefix(call, "GET ") {
			t.Fatal(call)
		}
	}
	for _, tc := range []struct {
		scheme string
		port   int
		want   bool
	}{{"http", 8080, true}, {"http", 443, false}, {"https", 8443, true}, {"https", 8080, false}, {"https", 54321, false}} {
		if ProxyPortAllowed(tc.scheme, tc.port) != tc.want {
			t.Errorf("wrong port result %+v", tc)
		}
	}
}
func TestAccessCreatesInlineAllowPolicyAndVerifies(t *testing.T) {
	var app AccessApplication
	posts := 0
	c, _ := fixtureClient(t, func(w http.ResponseWriter, r *http.Request) bool {
		switch r.URL.Path {
		case "/zones/zone-1/dns_records":
			cfReply(w, 200, []DNSRecord{{Type: "CNAME", Name: "nas.example.com", Proxied: true}})
			return true
		case "/accounts/account-1/access/apps":
			if r.Method == "POST" {
				posts++
				if err := json.NewDecoder(r.Body).Decode(&app); err != nil {
					t.Error(err)
				}
				app.ID = "app-1"
				if !matchesAccessPolicy(app.Policies, []string{"user@example.com"}) {
					t.Errorf("application created without exact allow policy: %+v", app)
				}
				cfReply(w, 200, app)
				return true
			}
			if app.ID != "" {
				cfReply(w, 200, []AccessApplication{app})
				return true
			}
		case "/accounts/account-1/access/apps/app-1":
			if r.Method == "PUT" {
				var updated AccessApplication
				_ = json.NewDecoder(r.Body).Decode(&updated)
				app = updated
				app.ID = "app-1"
			}
			cfReply(w, 200, app)
			return true
		case "/accounts/account-1/access/apps/app-1/policies":
			cfReply(w, 200, app.Policies)
			return true
		}
		return false
	})
	p := testPublisher(t, c)
	request := AccessRequest{Hostname: "nas.example.com", Emails: []string{"user@example.com"}}
	result, err := p.SaveAccess(context.Background(), "conn", request)
	if err != nil {
		t.Fatal(err)
	}
	if result.ID != "app-1" {
		t.Fatal(result)
	}
	app.AllowedIDPs = []string{"private-idp"}
	app.MFAConfig = map[string]any{"mfa_disabled": false}
	if _, err = p.SaveAccess(context.Background(), "conn", request); err != nil {
		t.Fatal(err)
	}
	if len(app.AllowedIDPs) != 1 || app.AllowedIDPs[0] != "private-idp" || app.MFAConfig["mfa_disabled"] != false {
		t.Fatal("Access update removed existing identity/MFA settings")
	}
	if posts != 1 {
		t.Fatalf("retry created %d apps", posts)
	}
	report, err := p.Inspect(context.Background(), "conn")
	if err != nil || !report.Applications[0].Managed {
		t.Fatalf("ownership missing: %+v %v", report, err)
	}
}
func TestAccessDoesNotReplaceExistingOrRedirectApplications(t *testing.T) {
	for _, foreign := range []string{"nas.example.com", "*.example.com", "nas.example.com/admin"} {
		t.Run(foreign, func(t *testing.T) {
			c, calls := fixtureClient(t, func(w http.ResponseWriter, r *http.Request) bool {
				switch r.URL.Path {
				case "/zones/zone-1/dns_records":
					cfReply(w, 200, []DNSRecord{{Name: "nas.example.com", Type: "A", Proxied: true}})
					return true
				case "/accounts/account-1/access/apps":
					cfReply(w, 200, []AccessApplication{{ID: "foreign", Domain: foreign, Type: "self_hosted"}})
					return true
				}
				return false
			})
			p := testPublisher(t, c)
			if _, err := p.SaveAccess(context.Background(), "conn", AccessRequest{Hostname: "nas.example.com", Emails: []string{"user@example.com"}}); err == nil {
				t.Fatal("foreign app was accepted")
			}
			for _, call := range *calls {
				if !strings.HasPrefix(call, "GET ") {
					t.Fatal(call)
				}
			}
		})
	}
	c, _ := fixtureClient(t, func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path == "/zones/zone-1/dns_records" {
			cfReply(w, 200, []DNSRecord{{Name: "nas.example.com", Type: "A", Proxied: true}})
			return true
		}
		return false
	})
	p := testPublisher(t, c)
	svc := testService("redirect")
	svc.CreatedAt = time.Now()
	svc.UpdatedAt = svc.CreatedAt
	if err := p.Store.CreateService(context.Background(), svc); err != nil {
		t.Fatal(err)
	}
	if _, err := p.SaveAccess(context.Background(), "conn", AccessRequest{Hostname: svc.EntryHostname, Emails: []string{"user@example.com"}}); err == nil {
		t.Fatal("Access accepted Redirect second hop")
	}
}
func TestAccessValidationAndDomainBoundaries(t *testing.T) {
	for _, emails := range [][]string{nil, {"*"}, {"User <user@example.com>"}, {"a@b\nInjected"}} {
		if _, err := ValidateAccessRequest(AccessRequest{Hostname: "nas.example.com", Emails: emails}); err == nil {
			t.Errorf("accepted %+v", emails)
		}
	}
	for _, tc := range []struct {
		host string
		want bool
	}{{"example.com", true}, {"nas.example.com", true}, {"badexample.com", false}, {"example.com.evil.test", false}} {
		if HostInZone(tc.host, "example.com") != tc.want {
			t.Fatal(tc)
		}
	}
}
func TestRuleMutationExtractsRuleIDFromRulesetResponse(t *testing.T) {
	c, _ := fixtureClient(t, func(w http.ResponseWriter, r *http.Request) bool {
		if strings.Contains(r.URL.Path, "/rules/") || strings.HasSuffix(r.URL.Path, "/rules") {
			cfReply(w, 200, Ruleset{ID: "ruleset", Rules: []Rule{{ID: "actual-rule", Ref: "stundeck_svc"}}})
			return true
		}
		return false
	})
	rule, err := c.updateRule(context.Background(), "zone-1", "ruleset", "actual-rule", Rule{Ref: "stundeck_svc"})
	if err != nil || rule.ID != "actual-rule" {
		t.Fatalf("wrong managed rule id: %+v %v", rule, err)
	}
}
func TestSpectrumUpdatesOnlyRecordedApplication(t *testing.T) {
	var payload map[string]any
	c, _ := fixtureClient(t, func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path == "/zones/zone-1/spectrum/apps" && r.Method == "GET" {
			var app SpectrumApplication
			app.ID = "spectrum-1"
			app.TLS = "strict"
			app.IPFirewall = true
			app.ProxyProtocol = "v2"
			app.DNS.Name = "nas.example.com"
			cfReply(w, 200, []SpectrumApplication{app})
			return true
		}
		if r.URL.Path == "/zones/zone-1/spectrum/apps/spectrum-1" && r.Method == "PUT" {
			_ = json.NewDecoder(r.Body).Decode(&payload)
			cfReply(w, 200, SpectrumApplication{ID: "spectrum-1"})
			return true
		}
		return false
	})
	p := testPublisher(t, c)
	svc := testService("spectrum")
	if _, err := p.Sync(context.Background(), svc); err == nil {
		t.Fatal("adopted foreign Spectrum app")
	}
	if err := p.Store.SaveCloudflareResource(context.Background(), store.CloudflareResource{ConnectionID: "conn", OwnerID: "svc", Kind: "spectrum", RemoteID: "spectrum-1", Hostname: svc.EntryHostname}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Sync(context.Background(), svc); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(payload["origin_direct"]) != "[tcp://203.0.113.1:8080]" || payload["protocol"] != "tcp/443" || payload["tls"] != "strict" || payload["ip_firewall"] != true || payload["proxy_protocol"] != "v2" {
		t.Fatal(payload)
	}
}

func TestCleanupRetriesWithoutDeletingAccessOrForeignDNS(t *testing.T) {
	deletedTunnel, failDNS := false, true
	c, calls := fixtureClient(t, func(w http.ResponseWriter, r *http.Request) bool {
		switch r.URL.Path {
		case "/accounts/account-1/cfd_tunnel/tunnel-1":
			if r.Method == "DELETE" {
				deletedTunnel = true
				cfReply(w, 200, map[string]any{})
				return true
			}
			if deletedTunnel {
				cfReply(w, 404, nil)
			} else {
				cfReply(w, 200, Tunnel{ID: "tunnel-1", Name: "stundeck-svc", ConfigSrc: "cloudflare"})
			}
			return true
		case "/zones/zone-1/dns_records":
			cfReply(w, 200, []DNSRecord{{ID: "owned", Name: "nas.example.com", Comment: "managed-by=stundeck:svc"}, {ID: "foreign", Name: "other.example.com", Comment: "another-manager"}})
			return true
		case "/zones/zone-1/dns_records/owned":
			if failDNS {
				failDNS = false
				cfReply(w, 503, nil)
			} else {
				cfReply(w, 200, map[string]any{})
			}
			return true
		}
		return false
	})
	p := testPublisher(t, c)
	ctx := context.Background()
	for _, r := range []store.CloudflareResource{
		{ConnectionID: "conn", OwnerID: "svc", Kind: "tunnel", RemoteID: "tunnel-1", Hostname: "nas.example.com"},
		{ConnectionID: "conn", OwnerID: "nas.example.com", Kind: "access:accounts", RemoteID: "app-1", Hostname: "nas.example.com"},
	} {
		if err := p.Store.SaveCloudflareResource(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	if err := p.Cleanup(ctx, testService("tunnel")); err == nil {
		t.Fatal("expected partial cleanup failure")
	}
	if err := p.Cleanup(ctx, testService("tunnel")); err != nil {
		t.Fatal(err)
	}
	resources, err := p.Store.CloudflareResources(ctx, "conn")
	if err != nil || len(resources) != 1 || resources[0].Kind != "access:accounts" {
		t.Fatalf("Access ownership lost: %+v %v", resources, err)
	}
	for _, call := range *calls {
		if strings.Contains(call, "/access/") || strings.Contains(call, "/dns_records/foreign") {
			t.Fatalf("removed unrelated protection/resource: %s", call)
		}
	}
}
func TestAccountOwnedTokenVerification(t *testing.T) {
	c, _ := fixtureClient(t, func(w http.ResponseWriter, r *http.Request) bool {
		switch r.URL.Path {
		case "/user/tokens/verify":
			cfReply(w, 403, nil)
			return true
		case "/accounts/account-1/tokens/verify":
			cfReply(w, 200, TokenStatus{ID: "account-token", Status: "active"})
			return true
		}
		return false
	})
	result, err := c.VerifyForZones(context.Background(), []Zone{testZone()})
	if err != nil || result.ID != "account-token" {
		t.Fatalf("account verify failed: %+v %v", result, err)
	}
}
func TestAPIErrorRedactsTokenAndPreservesHTTPStatus(t *testing.T) {
	c, _ := fixtureClient(t, func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path == "/json-error" {
			w.WriteHeader(403)
			_, _ = w.Write([]byte(`{"success":false,"errors":[{"code":10000,"message":"rejected fixture-secret"}]}`))
			return true
		}
		if r.URL.Path == "/html-error" {
			w.WriteHeader(403)
			_, _ = w.Write([]byte(`<html>Forbidden</html>`))
			return true
		}
		return false
	})
	for _, path := range []string{"/json-error", "/html-error"} {
		err := c.do(context.Background(), "GET", path, nil, nil)
		if err == nil || !permissionDenied(err) || strings.Contains(err.Error(), "fixture-secret") {
			t.Fatalf("unsafe error: %v", err)
		}
	}
}

func TestTokenPolicyReadOnlyWinsOverZoneMetadata(t *testing.T) {
	c, _ := fixtureClient(t, func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path == "/user/tokens/token-1" {
			var details tokenDetails
			_ = json.Unmarshal([]byte(`{"policies":[{"effect":"allow","resources":{"com.cloudflare.api.account.zone.zone-1":"*"},"permission_groups":[{"name":"DNS Read"}]}]}`), &details)
			cfReply(w, 200, details)
			return true
		}
		return false
	})
	report, err := c.Inspect(context.Background(), "zone-1")
	if err != nil {
		t.Fatal(err)
	}
	if report.Capabilities["dns"].State != "read_only" {
		t.Fatalf("token read scope was overridden: %+v", report.Capabilities["dns"])
	}
}
