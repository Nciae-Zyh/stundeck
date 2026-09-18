package cloudflare

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/Nciae-Zyh/stundeck/internal/security"
	"github.com/Nciae-Zyh/stundeck/internal/store"
)

type Publisher struct {
	Store     *store.Store
	Cipher    *security.Cipher
	NewClient func(string) *Client
	mu        sync.Mutex
}

func NewPublisher(database *store.Store, cipher *security.Cipher) *Publisher {
	return &Publisher{Store: database, Cipher: cipher, NewClient: New}
}

func (p *Publisher) ClientFor(ctx context.Context, connectionID string) (*Client, store.CloudflareConnection, Zone, error) {
	connection, err := p.Store.CloudflareConnection(ctx, connectionID)
	if err != nil {
		return nil, connection, Zone{}, errors.New("Cloudflare 连接不存在")
	}
	token, err := p.Cipher.Decrypt(connection.TokenCiphertext)
	if err != nil {
		return nil, connection, Zone{}, errors.New("无法解密 Cloudflare Token")
	}
	c := p.NewClient(token)
	zone, err := c.Zone(ctx, connection.ZoneID)
	if err == nil && !strings.EqualFold(zone.Name, connection.ZoneName) {
		err = errors.New("Cloudflare Zone 与保存的连接不匹配")
	}
	return c, connection, zone, err
}

func (p *Publisher) Sync(ctx context.Context, service store.Service) (SyncResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.sync(ctx, service)
}

func (p *Publisher) sync(ctx context.Context, service store.Service) (SyncResult, error) {
	c, connection, zone, err := p.ClientFor(ctx, service.CloudflareConnectionID)
	if err != nil {
		return SyncResult{}, err
	}
	if service.PublishMode == "warp" {
		return p.syncTunnel(ctx, c, connection, zone, service)
	}
	if zone.Status != "active" {
		return SyncResult{}, errors.New("Cloudflare Zone 尚未激活")
	}
	if !HostInZone(service.EntryHostname, zone.Name) || (service.ManageDNS && service.OriginHostname != "" && !HostInZone(service.OriginHostname, zone.Name)) {
		return SyncResult{}, errors.New("发布域名必须属于所选 Zone")
	}
	if service.PublishMode == "tunnel" {
		return p.syncTunnel(ctx, c, connection, zone, service)
	}
	if net.ParseIP(service.PublicIP) == nil || service.PublicPort < 1 || service.PublicPort > 65535 {
		return SyncResult{}, errors.New("服务尚无有效公网映射，请先启动服务")
	}
	var result SyncResult
	switch service.PublishMode {
	case "redirect":
		result, err = c.ReconcileService(ctx, zone.ID, service)
	case "dns", "proxy":
		if service.PublishMode == "proxy" && !ProxyPortAllowed(service.Scheme, service.PublicPort) {
			return result, fmt.Errorf("Cloudflare %s 代理不支持公网端口 %d；请选择 Tunnel 或 Redirect", service.Scheme, service.PublicPort)
		}
		err = c.ensureDNSRecord(ctx, zone.ID, service.EntryHostname, service.PublicIP, service.PublishMode == "proxy", service.ID)
		result.TargetURL = fmt.Sprintf("%s://%s", service.Scheme, net.JoinHostPort(service.EntryHostname, strconv.Itoa(service.PublicPort)))
	case "spectrum":
		return p.syncSpectrum(ctx, c, connection, zone, service)
	case "workers":
		return p.syncWorker(ctx, c, connection, zone, service)
	default:
		return result, errors.New("此服务未配置 Cloudflare 发布")
	}
	if err != nil {
		return result, err
	}
	err = p.Store.SaveCloudflareResource(ctx, store.CloudflareResource{ConnectionID: connection.ID, OwnerID: service.ID, Kind: service.PublishMode, RemoteID: result.RulesetID + ":" + result.RuleID, Hostname: service.EntryHostname})
	return result, err
}

func ProxyPortAllowed(scheme string, port int) bool {
	var ports []int
	if scheme == "http" {
		ports = []int{80, 8080, 8880, 2052, 2082, 2086, 2095}
	}
	if scheme == "https" {
		ports = []int{443, 2053, 2083, 2087, 2096, 8443}
	}
	for _, p := range ports {
		if p == port {
			return true
		}
	}
	return false
}

type Tunnel struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Status    string `json:"status"`
	ConfigSrc string `json:"config_src"`
}

func tunnelName(serviceID string) string { return "stundeck-" + serviceID }

func (p *Publisher) syncTunnel(ctx context.Context, c *Client, connection store.CloudflareConnection, zone Zone, service store.Service) (SyncResult, error) {
	if zone.Account.ID == "" {
		return SyncResult{}, errors.New("Tunnel 需要所选 Zone 所属的账户 ID")
	}
	protocol := service.TunnelProtocol
	if protocol == "" {
		protocol = "http"
	}
	private := service.PublishMode == "warp"
	if !private && (service.Protocol != "tcp" || !TunnelProtocolAllowed(protocol)) {
		return SyncResult{}, errors.New("公网 Tunnel 支持 HTTP、HTTPS、TCP、SSH、RDP；UDP 请使用 WARP 私网或 Spectrum")
	}
	// Check DNS ownership before creating a tunnel, and again when publishing.
	if private {
		if _, err := PrivateNetwork(service.TargetHost, service.PrivateNetwork); err != nil {
			return SyncResult{}, err
		}
	} else {
		if _, err := c.ownedDNS(ctx, zone.ID, service.EntryHostname, service.ID); err != nil {
			return SyncResult{}, err
		}
	}
	base := "/accounts/" + escaped(zone.Account.ID) + "/cfd_tunnel"
	resource, err := p.Store.CloudflareResource(ctx, connection.ID, service.ID, "tunnel")
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return SyncResult{}, err
	}
	var tunnel Tunnel
	if resource.RemoteID != "" {
		err = c.do(ctx, http.MethodGet, base+"/"+escaped(resource.RemoteID), nil, &tunnel)
		if err != nil {
			return SyncResult{}, fmt.Errorf("读取已管理 Tunnel 失败，请先清理旧发布：%w", err)
		}
		if tunnel.Name != tunnelName(service.ID) || tunnel.ConfigSrc != "cloudflare" {
			return SyncResult{}, errors.New("Tunnel 的名称或管理方式已变更，拒绝覆盖")
		}
	} else {
		err = c.do(ctx, http.MethodPost, base, map[string]any{"name": tunnelName(service.ID), "config_src": "cloudflare"}, &tunnel)
		if err != nil {
			return SyncResult{}, fmt.Errorf("创建 Tunnel 失败，需要 Account · Cloudflare Tunnel · Edit：%w", err)
		}
		if tunnel.ID == "" {
			return SyncResult{}, errors.New("Cloudflare 未返回 Tunnel ID")
		}
		resource = store.CloudflareResource{ConnectionID: connection.ID, OwnerID: service.ID, Kind: "tunnel", RemoteID: tunnel.ID, Hostname: service.EntryHostname}
		if err = p.Store.SaveCloudflareResource(ctx, resource); err != nil {
			cleanupErr := c.do(ctx, http.MethodDelete, base+"/"+escaped(tunnel.ID), nil, nil)
			return SyncResult{}, errors.Join(err, cleanupErr)
		}
	}
	config := map[string]any{"config": map[string]any{"ingress": []map[string]any{
		{"hostname": service.EntryHostname, "service": protocol + "://" + net.JoinHostPort(service.TargetHost, strconv.Itoa(service.TargetPort))},
		{"service": "http_status:404"},
	}}}
	if private {
		config = map[string]any{"config": map[string]any{"warp-routing": map[string]bool{"enabled": true}, "ingress": []map[string]string{{"service": "http_status:404"}}}}
	}
	if err = c.do(ctx, http.MethodPut, base+"/"+escaped(tunnel.ID)+"/configurations", config, nil); err != nil {
		return SyncResult{}, err
	}
	if private {
		return p.syncPrivateRoute(ctx, c, connection, zone, service, tunnel.ID)
	}
	if err = c.ensureRecord(ctx, zone.ID, service.EntryHostname, "CNAME", tunnel.ID+".cfargotunnel.com", true, service.ID); err != nil {
		return SyncResult{}, err
	}
	return SyncResult{ResourceID: tunnel.ID, TargetURL: "https://" + service.EntryHostname}, nil
}

func TunnelProtocolAllowed(protocol string) bool {
	return protocol == "http" || protocol == "https" || protocol == "tcp" || protocol == "ssh" || protocol == "rdp"
}

// Connector tokens are fetched only by the process supervisor, never returned
// to the dashboard, placed in argv, or stored alongside API responses.
func (p *Publisher) TunnelToken(ctx context.Context, service store.Service) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	result, err := p.sync(ctx, service)
	if err != nil {
		return "", err
	}
	c, _, zone, err := p.ClientFor(ctx, service.CloudflareConnectionID)
	if err != nil {
		return "", err
	}
	var token string
	err = c.do(ctx, http.MethodGet, "/accounts/"+escaped(zone.Account.ID)+"/cfd_tunnel/"+escaped(result.ResourceID)+"/token", nil, &token)
	if err == nil && token == "" {
		err = errors.New("Cloudflare 未返回 Tunnel connector token")
	}
	return token, err
}

type SpectrumApplication struct {
	ProxyProtocol    string         `json:"proxy_protocol"`
	TLS              string         `json:"tls"`
	IPFirewall       bool           `json:"ip_firewall"`
	EdgeIPs          map[string]any `json:"edge_ips,omitempty"`
	ArgoSmartRouting *bool          `json:"argo_smart_routing,omitempty"`
	TrafficType      string         `json:"traffic_type,omitempty"`
	ID               string         `json:"id"`
	Protocol         string         `json:"protocol"`
	DNS              struct {
		Type string `json:"type"`
		Name string `json:"name"`
	} `json:"dns"`
}

func (p *Publisher) syncSpectrum(ctx context.Context, c *Client, connection store.CloudflareConnection, zone Zone, service store.Service) (SyncResult, error) {
	if service.EdgePort < 1 || service.EdgePort > 65535 {
		return SyncResult{}, errors.New("Spectrum 需要有效的边缘端口")
	}
	resource, err := p.Store.CloudflareResource(ctx, connection.ID, service.ID, "spectrum")
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return SyncResult{}, err
	}
	base := "/zones/" + escaped(zone.ID) + "/spectrum/apps"
	apps, err := listAll[SpectrumApplication](ctx, c, base, 50)
	if err != nil {
		return SyncResult{}, fmt.Errorf("Spectrum 不可用，请检查 Zone Settings Edit 和套餐授权：%w", err)
	}
	found := false
	var existing SpectrumApplication
	for _, app := range apps {
		if app.DNS.Name == service.EntryHostname && app.ID != resource.RemoteID {
			return SyncResult{}, errors.New("已有其他 Spectrum 应用使用此域名")
		}
		if app.ID == resource.RemoteID {
			if app.DNS.Name != service.EntryHostname {
				return SyncResult{}, errors.New("Spectrum 应用域名已在外部变更，拒绝覆盖")
			}
			found = true
			existing = app
		}
	}
	if resource.RemoteID != "" && !found {
		return SyncResult{}, errors.New("已管理 Spectrum 应用不存在，请先清理旧发布")
	}
	if resource.RemoteID == "" {
		records, err := c.DNSRecords(ctx, zone.ID, service.EntryHostname)
		if err != nil {
			return SyncResult{}, err
		}
		if len(records) > 0 {
			return SyncResult{}, errors.New("Spectrum 域名已有 DNS 记录，请使用未占用的域名")
		}
	}
	payload := map[string]any{"protocol": service.Protocol + "/" + strconv.Itoa(service.EdgePort), "dns": map[string]string{"type": "CNAME", "name": service.EntryHostname}, "origin_direct": []string{service.Protocol + "://" + net.JoinHostPort(service.PublicIP, strconv.Itoa(service.PublicPort))}, "proxy_protocol": "off", "tls": "off", "ip_firewall": false}
	// Mapping changes must not disable provider-side TLS, firewall, or proxy
	// settings that the administrator has configured on this owned application.
	if found {
		payload["ip_firewall"] = existing.IPFirewall
		if existing.ProxyProtocol != "" {
			payload["proxy_protocol"] = existing.ProxyProtocol
		}
		if existing.TLS != "" {
			payload["tls"] = existing.TLS
		}
		if existing.EdgeIPs != nil {
			payload["edge_ips"] = existing.EdgeIPs
		}
		if existing.ArgoSmartRouting != nil {
			payload["argo_smart_routing"] = *existing.ArgoSmartRouting
		}
		if existing.TrafficType != "" {
			payload["traffic_type"] = existing.TrafficType
		}
	}
	method, path := http.MethodPost, base
	if found {
		method, path = http.MethodPut, base+"/"+escaped(resource.RemoteID)
	}
	var app SpectrumApplication
	if err = c.do(ctx, method, path, payload, &app); err != nil {
		return SyncResult{}, fmt.Errorf("Spectrum 配置失败，可能缺少权限、协议套餐或配额：%w", err)
	}
	if app.ID == "" {
		return SyncResult{}, errors.New("Cloudflare 未返回 Spectrum ID")
	}
	if err = p.Store.SaveCloudflareResource(ctx, store.CloudflareResource{ConnectionID: connection.ID, OwnerID: service.ID, Kind: "spectrum", RemoteID: app.ID, Hostname: service.EntryHostname}); err != nil {
		if method == http.MethodPost {
			err = errors.Join(err, c.do(ctx, http.MethodDelete, base+"/"+escaped(app.ID), nil, nil))
		}
		return SyncResult{}, err
	}
	return SyncResult{ResourceID: app.ID, TargetURL: service.Protocol + "://" + net.JoinHostPort(service.EntryHostname, strconv.Itoa(service.EdgePort))}, nil
}

func (p *Publisher) Cleanup(ctx context.Context, service store.Service) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	c, connection, zone, err := p.ClientFor(ctx, service.CloudflareConnectionID)
	if err != nil {
		return err
	}
	resources, err := p.Store.CloudflareResources(ctx, connection.ID)
	if err != nil {
		return err
	}
	cleanedRedirect := false
	priority := map[string]int{"private_route": 0, "worker_route": 0, "worker": 1, "tunnel": 2}
	sort.SliceStable(resources, func(i, j int) bool { return priority[resources[i].Kind] < priority[resources[j].Kind] })
	// Only the recorded service-owned resource is removable. Access is separate:
	// stopping or deleting a connector must never remove its authentication policy.
	for _, resource := range resources {
		if resource.OwnerID != service.ID {
			continue
		}
		switch resource.Kind {
		case "private_route":
			err = p.cleanupPrivateRoute(ctx, c, zone, service, resource)
		case "worker_route", "worker":
			err = p.cleanupWorker(ctx, c, zone, service, resource)
		case "tunnel":
			path := "/accounts/" + escaped(zone.Account.ID) + "/cfd_tunnel/" + escaped(resource.RemoteID)
			var tunnel Tunnel
			if err = c.do(ctx, http.MethodGet, path, nil, &tunnel); err == nil {
				if tunnel.Name != tunnelName(service.ID) {
					return errors.New("Tunnel 已在外部变更，拒绝删除")
				}
				err = c.do(ctx, http.MethodDelete, path, nil, nil)
			}
		case "spectrum":
			path := "/zones/" + escaped(zone.ID) + "/spectrum/apps/" + escaped(resource.RemoteID)
			var app SpectrumApplication
			if err = c.do(ctx, http.MethodGet, path, nil, &app); err == nil {
				if app.DNS.Name != resource.Hostname {
					return errors.New("Spectrum 域名已在外部变更，拒绝删除")
				}
				err = c.do(ctx, http.MethodDelete, path, nil, nil)
			}
		case "redirect":
			err = c.cleanupRedirect(ctx, zone.ID, service.ID)
			cleanedRedirect = true
		}
		if err != nil && !isNotFound(err) {
			return err
		}
	}
	// Older versions did not persist rule IDs. Stable rule refs still prove ownership.
	if service.PublishMode == "redirect" && !cleanedRedirect {
		if err = c.cleanupRedirect(ctx, zone.ID, service.ID); err != nil {
			return err
		}
	}
	if service.PublishMode != "spectrum" && service.PublishMode != "warp" && (service.PublishMode != "redirect" || service.ManageDNS) {
		records, err := c.DNSRecords(ctx, zone.ID, "")
		if err != nil {
			return err
		}
		for _, record := range records {
			if record.Comment == "managed-by=stundeck:"+service.ID {
				if err = c.do(ctx, http.MethodDelete, "/zones/"+escaped(zone.ID)+"/dns_records/"+escaped(record.ID), nil, nil); err != nil && !isNotFound(err) {
					return err
				}
			}
		}
	}
	for _, resource := range resources {
		if resource.OwnerID == service.ID {
			if err = p.Store.DeleteCloudflareResource(ctx, connection.ID, service.ID, resource.Kind); err != nil {
				return err
			}
		}
	}
	return nil
}

func (c *Client) cleanupRedirect(ctx context.Context, zoneID, serviceID string) error {
	summary, err := c.redirectRuleset(ctx, zoneID)
	if isNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if summary.ID == "" {
		return nil
	}
	ruleset, err := c.ruleset(ctx, zoneID, summary.ID)
	if isNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	ref := "stundeck_" + strings.ReplaceAll(serviceID, "-", "_")
	for _, rule := range ruleset.Rules {
		if rule.Ref == ref {
			if err = c.do(ctx, http.MethodDelete, "/zones/"+escaped(zoneID)+"/rulesets/"+escaped(summary.ID)+"/rules/"+escaped(rule.ID), nil, nil); err != nil && !isNotFound(err) {
				return err
			}
		}
	}
	return nil
}
