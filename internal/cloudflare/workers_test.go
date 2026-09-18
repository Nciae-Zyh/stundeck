package cloudflare

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Nciae-Zyh/stundeck/internal/store"
)

func TestWorkerPublishRetriesWithoutDuplicateRoutesAndCleansOwnedResources(t *testing.T) {
	ctx := context.Background()
	svc := testService("workers")
	svc.OriginHostname, svc.PublicPort = "origin.example.com", 54321
	name := workerName(svc.ID)
	base := "/accounts/account-1/workers/scripts/" + name
	scriptExists, disableFailed := false, false
	var uploads, creates int
	records := map[string]DNSRecord{}
	var route WorkerRoute
	c, _ := fixtureClient(t, func(w http.ResponseWriter, r *http.Request) bool {
		switch {
		case r.URL.Path == base+"/settings":
			if scriptExists {
				cfReply(w, 200, workerSettings{Tags: []string{workerTag(svc.ID)}})
			} else {
				cfReply(w, 404, nil)
			}
		case r.URL.Path == base && r.Method == "PUT":
			reader, err := r.MultipartReader()
			if err != nil {
				t.Error(err)
				cfReply(w, 400, nil)
				return true
			}
			parts := map[string]string{}
			for {
				part, err := reader.NextPart()
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Error(err)
					break
				}
				data, _ := io.ReadAll(part)
				parts[part.FormName()] = string(data)
			}
			var metadata struct {
				Main  string   `json:"main_module"`
				Date  string   `json:"compatibility_date"`
				Flags []string `json:"compatibility_flags"`
				Tags  []string `json:"tags"`
			}
			if json.Unmarshal([]byte(parts["metadata"]), &metadata) != nil || metadata.Main != "proxy.mjs" || metadata.Date != "2026-09-18" || len(metadata.Tags) != 1 || metadata.Tags[0] != workerTag(svc.ID) {
				t.Errorf("invalid metadata: %s", parts["metadata"])
			}
			if !strings.Contains(parts["proxy.mjs"], `createProxy("http://origin.example.com:54321", "nas.example.com")`) || strings.Contains(parts["proxy.mjs"], "fixture-secret") {
				t.Error("wrong fixed origin or token exposed")
			}
			if !strings.Contains(strings.Join(metadata.Flags, ","), "allow_custom_ports") {
				t.Error("missing custom port compatibility")
			}
			scriptExists = true
			uploads++
			cfReply(w, 200, map[string]string{"id": name})
		case r.URL.Path == base && r.Method == "DELETE":
			if route.ID != "" {
				t.Error("deleted worker before route")
			}
			scriptExists = false
			cfReply(w, 200, nil)
		case r.URL.Path == base+"/subdomain":
			var body map[string]bool
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["enabled"] || body["previews_enabled"] || len(body) != 2 {
				t.Error("alternate public endpoints enabled")
			}
			if !disableFailed {
				disableFailed = true
				cfReply(w, 403, nil)
			} else {
				cfReply(w, 200, body)
			}
		case r.URL.Path == "/zones/zone-1/workers/routes":
			if r.Method == "GET" {
				routes := []WorkerRoute{}
				if route.ID != "" {
					routes = append(routes, route)
				}
				cfReply(w, 200, routes)
			} else {
				_ = json.NewDecoder(r.Body).Decode(&route)
				route.ID = "route"
				creates++
				cfReply(w, 200, route)
			}
		case r.URL.Path == "/zones/zone-1/workers/routes/route":
			if r.Method == "GET" {
				if route.ID == "" {
					cfReply(w, 404, nil)
				} else {
					cfReply(w, 200, route)
				}
			} else {
				route = WorkerRoute{}
				cfReply(w, 200, nil)
			}
		case r.URL.Path == "/zones/zone-1/dns_records":
			if r.Method == "GET" {
				list := []DNSRecord{}
				for _, record := range records {
					if r.URL.Query().Get("name") == "" || r.URL.Query().Get("name") == record.Name {
						list = append(list, record)
					}
				}
				cfReply(w, 200, list)
			} else {
				var record DNSRecord
				_ = json.NewDecoder(r.Body).Decode(&record)
				record.ID = record.Name
				records[record.ID] = record
				cfReply(w, 200, record)
			}
		case strings.HasPrefix(r.URL.Path, "/zones/zone-1/dns_records/") && r.Method == "DELETE":
			delete(records, strings.TrimPrefix(r.URL.Path, "/zones/zone-1/dns_records/"))
			cfReply(w, 200, nil)
		default:
			return false
		}
		return true
	})
	p := testPublisher(t, c)
	if _, err := p.Sync(ctx, svc); err == nil {
		t.Fatal("subdomain failure ignored")
	}
	if creates != 0 {
		t.Fatal("published route before disabling alternate endpoints")
	}
	if _, err := p.Store.CloudflareResource(ctx, "conn", svc.ID, "worker"); err != nil {
		t.Fatal("partial failure lost Worker ownership")
	}
	for range 2 {
		if _, err := p.Sync(ctx, svc); err != nil {
			t.Fatal(err)
		}
	}
	if creates != 1 || uploads != 3 || route.Script != name || route.Pattern != "nas.example.com/*" {
		t.Fatalf("not idempotent: %d %d %+v", creates, uploads, route)
	}
	if records[svc.OriginHostname].Proxied || !records[svc.EntryHostname].Proxied || records[svc.EntryHostname].Content != "192.0.2.1" {
		t.Fatal("unsafe DNS defaults")
	}
	// Foreign records and independently managed Access must survive cleanup.
	records["foreign"] = DNSRecord{ID: "foreign", Name: "foreign.example.com"}
	if err := p.Store.SaveCloudflareResource(ctx, store.CloudflareResource{ConnectionID: "conn", OwnerID: svc.EntryHostname, Kind: "access:accounts", RemoteID: "app"}); err != nil {
		t.Fatal(err)
	}
	if err := p.Cleanup(ctx, svc); err != nil {
		t.Fatal(err)
	}
	if scriptExists || route.ID != "" || len(records) != 1 || records["foreign"].ID != "foreign" {
		t.Fatalf("bad cleanup: %+v", records)
	}
	resources, err := p.Store.CloudflareResources(ctx, "conn")
	if err != nil || len(resources) != 1 || resources[0].RemoteID != "app" {
		t.Fatalf("Access removed: %+v %v", resources, err)
	}
}

func TestWorkerRefusesForeignScriptAndOverlappingRoute(t *testing.T) {
	for _, foreignScript := range []bool{true, false} {
		c, calls := fixtureClient(t, func(w http.ResponseWriter, r *http.Request) bool {
			if strings.HasSuffix(r.URL.Path, "/settings") {
				if foreignScript {
					cfReply(w, 200, workerSettings{})
				} else {
					cfReply(w, 404, nil)
				}
				return true
			}
			if r.URL.Path == "/zones/zone-1/workers/routes" {
				cfReply(w, 200, []WorkerRoute{{ID: "foreign", Pattern: "https://*.example.com/private/*", Script: "existing"}})
				return true
			}
			return false
		})
		p := testPublisher(t, c)
		svc := testService("workers")
		svc.OriginHostname = "origin.example.com"
		if _, err := p.Sync(context.Background(), svc); err == nil {
			t.Fatal("foreign resource accepted")
		}
		for _, call := range *calls {
			if !strings.HasPrefix(call, "GET ") {
				t.Fatal("preflight mutated resource: " + call)
			}
		}
	}
}

func TestWorkerRouteHostBoundaries(t *testing.T) {
	for _, tc := range []struct {
		pattern string
		match   bool
	}{{"*.example.com/*", true}, {"*example.com/*", true}, {"http://nas.example.com/a*", true}, {"https://other.example.com/*", false}, {"https://nas.example.com.evil.test/*", false}, {"*/*", true}} {
		if got := workerRouteCovers(tc.pattern, "nas.example.com"); got != tc.match {
			t.Errorf("%s: %v", tc.pattern, got)
		}
	}
}
