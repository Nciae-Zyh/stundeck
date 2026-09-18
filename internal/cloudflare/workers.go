package cloudflare

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"mime/multipart"
	"net"
	"net/http"
	"net/textproto"
	"regexp"
	"strconv"
	"strings"

	"github.com/Nciae-Zyh/stundeck/internal/store"
)

//go:embed worker_proxy.mjs
var workerProxySource string

type WorkerRoute struct {
	ID      string `json:"id,omitempty"`
	Pattern string `json:"pattern"`
	Script  string `json:"script"`
}

type workerSettings struct {
	Tags []string `json:"tags"`
}

func workerName(serviceID string) string {
	return fmt.Sprintf("stundeck-%x", sha256.Sum256([]byte(serviceID)))[:41]
}
func workerTag(serviceID string) string { return "stundeck:" + serviceID }
func (c *Client) workerOwned(ctx context.Context, base, serviceID string) error {
	var settings workerSettings
	if err := c.do(ctx, http.MethodGet, base+"/settings", nil, &settings); err != nil {
		return err
	}
	for _, tag := range settings.Tags {
		if tag == workerTag(serviceID) {
			return nil
		}
	}
	return errors.New("Worker 的所有权标记已在外部变更，拒绝覆盖或删除")
}

// Reject overlapping routes, including exclusions (routes with no script).
func workerRouteCovers(pattern, hostname string) bool {
	pattern = strings.TrimPrefix(strings.TrimPrefix(pattern, "https://"), "http://")
	host := strings.SplitN(pattern, "/", 2)[0]
	match := "^" + strings.ReplaceAll(regexp.QuoteMeta(strings.ToLower(host)), `\*`, ".*") + "$"
	ok, err := regexp.MatchString(match, strings.ToLower(hostname))
	return err != nil || ok
}

func (p *Publisher) syncWorker(ctx context.Context, c *Client, connection store.CloudflareConnection, zone Zone, service store.Service) (SyncResult, error) {
	if zone.Account.ID == "" || service.Protocol != "tcp" || !HostInZone(service.OriginHostname, zone.Name) || service.OriginHostname == service.EntryHostname {
		return SyncResult{}, errors.New("Workers 需要账户 ID、TCP Web 服务与同 Zone 的独立源站域名")
	}
	for _, hostname := range []string{service.EntryHostname, service.OriginHostname} {
		if _, err := c.ownedDNS(ctx, zone.ID, hostname, service.ID); err != nil {
			return SyncResult{}, err
		}
	}
	name := workerName(service.ID)
	base := "/accounts/" + escaped(zone.Account.ID) + "/workers/scripts/" + escaped(name)
	resource, err := p.Store.CloudflareResource(ctx, connection.ID, service.ID, "worker")
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return SyncResult{}, err
	}
	if resource.RemoteID != "" {
		if resource.RemoteID != name {
			return SyncResult{}, errors.New("Worker 名称与所有权记录不一致")
		}
		if err = c.workerOwned(ctx, base, service.ID); err != nil && !isNotFound(err) {
			return SyncResult{}, err
		}
	} else {
		var settings workerSettings
		err = c.do(ctx, http.MethodGet, base+"/settings", nil, &settings)
		if err == nil {
			return SyncResult{}, errors.New("同名 Worker 已存在，拒绝接管")
		}
		if !isNotFound(err) {
			return SyncResult{}, err
		}
	}
	routeResource, err := p.Store.CloudflareResource(ctx, connection.ID, service.ID, "worker_route")
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return SyncResult{}, err
	}
	routeBase := "/zones/" + escaped(zone.ID) + "/workers/routes"
	var routes []WorkerRoute
	if err = c.do(ctx, http.MethodGet, routeBase, nil, &routes); err != nil {
		return SyncResult{}, err
	}
	pattern := service.EntryHostname + "/*"
	found := false
	for _, route := range routes {
		if route.ID == routeResource.RemoteID {
			if route.Pattern != pattern || route.Script != name {
				return SyncResult{}, errors.New("Workers 路由已在外部变更")
			}
			found = true
		} else if workerRouteCovers(route.Pattern, service.EntryHostname) || workerRouteCovers(route.Pattern, service.OriginHostname) {
			return SyncResult{}, errors.New("已有 Workers 路由覆盖入口或源站域名，拒绝覆盖")
		}
	}
	if routeResource.RemoteID != "" && !found {
		return SyncResult{}, errors.New("已管理 Workers 路由不存在，请先清理旧发布")
	}
	// Record intent before writing DNS so partial failures remain cleanable.
	if err = p.Store.SaveCloudflareResource(ctx, store.CloudflareResource{ConnectionID: connection.ID, OwnerID: service.ID, Kind: "worker_dns", Hostname: service.EntryHostname}); err != nil {
		return SyncResult{}, err
	}
	if err = p.Store.SaveCloudflareResource(ctx, store.CloudflareResource{ConnectionID: connection.ID, OwnerID: service.ID, Kind: "worker", RemoteID: name, Hostname: service.EntryHostname}); err != nil {
		return SyncResult{}, err
	}
	if err = c.ensureDNSRecord(ctx, zone.ID, service.OriginHostname, service.PublicIP, false, service.ID); err != nil {
		return SyncResult{}, err
	}
	origin := service.Scheme + "://" + net.JoinHostPort(service.OriginHostname, strconv.Itoa(service.PublicPort))
	if err = c.uploadWorker(ctx, base, service.ID, origin, service.EntryHostname); err != nil {
		return SyncResult{}, err
	}
	if err = p.Store.SaveCloudflareResource(ctx, store.CloudflareResource{ConnectionID: connection.ID, OwnerID: service.ID, Kind: "worker", RemoteID: name, Hostname: service.EntryHostname}); err != nil {
		if resource.RemoteID == "" {
			err = errors.Join(err, c.do(ctx, http.MethodDelete, base, nil, nil))
		}
		return SyncResult{}, err
	}
	// Prevent alternate URLs bypassing the hostname's Access application.
	if err = c.do(ctx, http.MethodPost, base+"/subdomain", map[string]bool{"enabled": false, "previews_enabled": false}, nil); err != nil {
		return SyncResult{}, err
	}
	if err = c.ensureDNSRecord(ctx, zone.ID, service.EntryHostname, "192.0.2.1", true, service.ID); err != nil {
		return SyncResult{}, err
	}
	if !found {
		var route WorkerRoute
		if err = c.do(ctx, http.MethodPost, routeBase, WorkerRoute{Pattern: pattern, Script: name}, &route); err != nil {
			return SyncResult{}, err
		}
		if route.ID == "" {
			return SyncResult{}, errors.New("Cloudflare 未返回 Workers 路由 ID")
		}
		if err = p.Store.SaveCloudflareResource(ctx, store.CloudflareResource{ConnectionID: connection.ID, OwnerID: service.ID, Kind: "worker_route", RemoteID: route.ID, Hostname: service.EntryHostname}); err != nil {
			return SyncResult{}, errors.Join(err, c.do(ctx, http.MethodDelete, routeBase+"/"+escaped(route.ID), nil, nil))
		}
	}
	return SyncResult{ResourceID: name, TargetURL: "https://" + service.EntryHostname}, nil
}

func (c *Client) uploadWorker(ctx context.Context, base, serviceID, origin, hostname string) error {
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	metadata := map[string]any{"main_module": "proxy.mjs", "compatibility_date": "2026-09-18", "compatibility_flags": []string{"allow_custom_ports", "global_fetch_strictly_public"}, "tags": []string{workerTag(serviceID)}}
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	part, err := writer.CreatePart(textproto.MIMEHeader{"Content-Disposition": {`form-data; name="metadata"`}, "Content-Type": {"application/json"}})
	if err != nil {
		return err
	}
	if _, err = part.Write(encoded); err != nil {
		return err
	}
	part, err = writer.CreatePart(textproto.MIMEHeader{"Content-Disposition": {`form-data; name="proxy.mjs"; filename="proxy.mjs"`}, "Content-Type": {"application/javascript+module"}})
	if err != nil {
		return err
	}
	originJSON, _ := json.Marshal(origin)
	hostJSON, _ := json.Marshal(hostname)
	if _, err = fmt.Fprintf(part, "%s\nexport default createProxy(%s, %s);\n", workerProxySource, originJSON, hostJSON); err != nil {
		return err
	}
	if err = writer.Close(); err != nil {
		return err
	}
	_, err = c.requestBody(ctx, http.MethodPut, base, &buffer, writer.FormDataContentType(), nil)
	return err
}

func (p *Publisher) cleanupWorker(ctx context.Context, c *Client, zone Zone, service store.Service, resource store.CloudflareResource) error {
	name := workerName(service.ID)
	if resource.Kind == "worker_route" {
		path := "/zones/" + escaped(zone.ID) + "/workers/routes/" + escaped(resource.RemoteID)
		var route WorkerRoute
		if err := c.do(ctx, http.MethodGet, path, nil, &route); err != nil {
			return err
		}
		if route.Script != name || route.Pattern != resource.Hostname+"/*" {
			return errors.New("Workers 路由已在外部变更，拒绝删除")
		}
		return c.do(ctx, http.MethodDelete, path, nil, nil)
	}
	if resource.RemoteID != name {
		return errors.New("Worker 名称与所有权记录不一致")
	}
	base := "/accounts/" + escaped(zone.Account.ID) + "/workers/scripts/" + escaped(name)
	if err := c.workerOwned(ctx, base, service.ID); err != nil {
		return err
	}
	return c.do(ctx, http.MethodDelete, base, nil, nil)
}
