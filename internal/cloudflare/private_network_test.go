package cloudflare

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Nciae-Zyh/stundeck/internal/store"
)

func TestPrivateNetworkValidation(t *testing.T) {
	for _, tc := range []struct{ target, network, want string }{
		{"192.168.1.2", "", "192.168.1.2/32"}, {"fd00::5", "", "fd00::5/128"}, {"10.1.2.3", "10.1.0.0/16", "10.1.0.0/16"},
		{"127.0.0.1", "", ""}, {"8.8.8.8", "", ""}, {"nas.local", "", ""}, {"10.0.0.1", "0.0.0.0/0", ""},
		{"192.168.1.2", "192.168.1.2/24", ""}, {"192.168.1.2", "192.168.2.0/24", ""}, {"172.16.1.1", "172.0.0.0/8", ""},
	} {
		network, err := PrivateNetwork(tc.target, tc.network)
		if tc.want == "" {
			if err == nil {
				t.Errorf("accepted %v", tc)
			}
			continue
		}
		if err != nil || network.String() != tc.want {
			t.Errorf("%v: %s %v", tc, network, err)
		}
	}
}

func TestPrivateRouteSyncAndCleanupOwnsOnlyItsRoute(t *testing.T) {
	ctx := context.Background()
	svc := testService("warp")
	svc.PrivateNetwork = "192.168.1.2/32"
	var route PrivateRoute
	var networkEnabled bool
	var creates int
	c, calls := fixtureClient(t, func(w http.ResponseWriter, r *http.Request) bool {
		switch r.URL.Path {
		case "/accounts/account-1/cfd_tunnel/owned":
			if r.Method == "GET" {
				cfReply(w, 200, Tunnel{ID: "owned", Name: tunnelName(svc.ID), ConfigSrc: "cloudflare"})
			} else {
				cfReply(w, 200, nil)
			}
			return true
		case "/accounts/account-1/cfd_tunnel/owned/configurations":
			var body struct {
				Config struct {
					Warp struct {
						Enabled bool `json:"enabled"`
					} `json:"warp-routing"`
					Ingress []struct {
						Service string `json:"service"`
					} `json:"ingress"`
				} `json:"config"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			networkEnabled = body.Config.Warp.Enabled
			if len(body.Config.Ingress) != 1 || body.Config.Ingress[0].Service != "http_status:404" {
				t.Error("private route published a hostname")
			}
			cfReply(w, 200, nil)
			return true
		case "/accounts/account-1/teamnet/routes":
			if r.Method == "GET" {
				routes := []PrivateRoute{}
				if route.ID != "" {
					routes = append(routes, route)
				}
				cfReply(w, 200, routes)
			} else {
				_ = json.NewDecoder(r.Body).Decode(&route)
				route.ID = "route-id"
				creates++
				cfReply(w, 200, route)
			}
			return true
		case "/accounts/account-1/teamnet/routes/route-id":
			if r.Method == "GET" {
				cfReply(w, 200, route)
			} else {
				route = PrivateRoute{}
				cfReply(w, 200, nil)
			}
			return true
		}
		return false
	})
	p := testPublisher(t, c)
	if err := p.Store.SaveCloudflareResource(ctx, store.CloudflareResource{ConnectionID: "conn", OwnerID: svc.ID, Kind: "tunnel", RemoteID: "owned"}); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := p.Sync(ctx, svc); err != nil {
			t.Fatal(err)
		}
	}
	if !networkEnabled || creates != 1 || route.Network != svc.PrivateNetwork {
		t.Fatalf("incorrect route: %+v, creates %d", route, creates)
	}
	if err := p.Cleanup(ctx, svc); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(*calls, "\n")
	if strings.Contains(joined, "dns_records") || strings.Contains(joined, "access/apps") {
		t.Fatal("private network requires no DNS or Access mutation")
	}
	if strings.Index(joined, "DELETE /accounts/account-1/teamnet/routes/route-id") > strings.Index(joined, "DELETE /accounts/account-1/cfd_tunnel/owned") {
		t.Fatal("tunnel deleted before route")
	}
}

func TestPrivateRouteOverlapFailsWithoutMutation(t *testing.T) {
	c, calls := fixtureClient(t, func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path == "/accounts/account-1/teamnet/routes" {
			cfReply(w, 200, []PrivateRoute{{ID: "foreign", Network: "192.168.0.0/16", TunnelID: "foreign"}})
			return true
		}
		return false
	})
	p := testPublisher(t, c)
	_, err := p.syncPrivateRoute(context.Background(), c, store.CloudflareConnection{ID: "conn"}, testZone(), testService("warp"), "owned")
	if err == nil {
		t.Fatal("overlap accepted")
	}
	for _, call := range *calls {
		if !strings.HasPrefix(call, "GET ") {
			t.Fatal(call)
		}
	}
}
