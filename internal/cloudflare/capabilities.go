package cloudflare

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

type Capability struct {
	State      string `json:"state"` // available, read_only, unverified, unavailable, error
	Permission string `json:"permission"`
	Message    string `json:"message"`
}

type Domain struct {
	Hostname       string `json:"hostname"`
	Proxied        bool   `json:"proxied"`
	AccessEligible bool   `json:"accessEligible"`
	Reason         string `json:"reason"`
	ApplicationID  string `json:"applicationId,omitempty"`
	Managed        bool   `json:"managed"`
	ServiceID      string `json:"serviceId,omitempty"`
}

type Inspection struct {
	Zone         Zone                  `json:"zone"`
	Capabilities map[string]Capability `json:"capabilities"`
	Domains      []Domain              `json:"domains"`
	Applications []AccessApplication   `json:"applications"`
	AccessScope  string                `json:"accessScope"`
	CheckedAt    time.Time             `json:"checkedAt"`
}

type tokenDetails struct {
	Policies []struct {
		Effect           string                     `json:"effect"`
		Resources        map[string]json.RawMessage `json:"resources"`
		PermissionGroups []struct {
			Name string `json:"name"`
		} `json:"permission_groups"`
	} `json:"policies"`
}

// VerifyToken only proves validity. Inspection uses GETs exclusively; it never
// creates a disposable resource to probe write permissions.
func (c *Client) Inspect(ctx context.Context, zoneID string) (Inspection, error) {
	zone, err := c.Zone(ctx, zoneID)
	if err != nil {
		return Inspection{}, err
	}
	var details tokenDetails
	var records []DNSRecord
	var apps []AccessApplication
	var dnsErr, redirectErr, tunnelErr, accessErr, spectrumErr error
	var networkErr, workerErr, workerRouteErr error
	scope := "accounts"
	var wg sync.WaitGroup
	wg.Add(9)
	go func() {
		defer wg.Done()
		status, err := c.VerifyToken(ctx)
		if err == nil {
			_ = c.do(ctx, http.MethodGet, "/user/tokens/"+escaped(status.ID), nil, &details)
		} else if zone.Account.ID != "" {
			if c.do(ctx, http.MethodGet, "/accounts/"+escaped(zone.Account.ID)+"/tokens/verify", nil, &status) == nil && status.Status == "active" {
				_ = c.do(ctx, http.MethodGet, "/accounts/"+escaped(zone.Account.ID)+"/tokens/"+escaped(status.ID), nil, &details)
			}
		}
	}()
	go func() { defer wg.Done(); records, dnsErr = c.DNSRecords(ctx, zone.ID, "") }()
	go func() { defer wg.Done(); _, redirectErr = c.redirectRuleset(ctx, zone.ID) }()
	go func() {
		defer wg.Done()
		if zone.Account.ID == "" {
			tunnelErr = errors.New("Zone 未返回账户 ID")
			return
		}
		var result []Tunnel
		tunnelErr = c.do(ctx, http.MethodGet, "/accounts/"+escaped(zone.Account.ID)+"/cfd_tunnel?is_deleted=false&per_page=1", nil, &result)
	}()
	go func() {
		defer wg.Done()
		if zone.Account.ID != "" {
			apps, accessErr = c.AccessApplications(ctx, "accounts", zone.Account.ID)
		} else {
			accessErr = errors.New("Zone 未返回账户 ID")
		}
		if accessErr != nil && (permissionDenied(accessErr) || isNotFound(accessErr) || zone.Account.ID == "") {
			scope = "zones"
			apps, accessErr = c.AccessApplications(ctx, scope, zone.ID)
		}
	}()
	go func() {
		defer wg.Done()
		var result []SpectrumApplication
		spectrumErr = c.do(ctx, http.MethodGet, "/zones/"+escaped(zone.ID)+"/spectrum/apps?per_page=1", nil, &result)
	}()
	go func() {
		defer wg.Done()
		if zone.Account.ID == "" {
			networkErr = errors.New("Zone 未返回账户 ID")
			return
		}
		var result []PrivateRoute
		networkErr = c.do(ctx, http.MethodGet, "/accounts/"+escaped(zone.Account.ID)+"/teamnet/routes?is_deleted=false&per_page=1", nil, &result)
	}()
	go func() {
		defer wg.Done()
		if zone.Account.ID == "" {
			workerErr = errors.New("Zone 未返回账户 ID")
			return
		}
		var result []json.RawMessage
		workerErr = c.do(ctx, http.MethodGet, "/accounts/"+escaped(zone.Account.ID)+"/workers/scripts", nil, &result)
	}()
	go func() {
		defer wg.Done()
		var result []WorkerRoute
		workerRouteErr = c.do(ctx, http.MethodGet, "/zones/"+escaped(zone.ID)+"/workers/routes", nil, &result)
	}()
	wg.Wait()
	capabilities := map[string]Capability{
		"private_network": capability(networkErr, "Account · Cloudflare One Networks · Edit 或 Cloudflare Tunnel · Edit", details.writeEvidence(zone, true, "Cloudflare One Networks Write", "Cloudflare Tunnel Write")),
		"workers":         capability(workerErr, "Account · Workers Scripts · Edit", details.writeEvidence(zone, true, "Workers Scripts Write")),
		"worker_routes":   capability(workerRouteErr, "Zone · Workers Routes · Edit", details.writeEvidence(zone, false, "Workers Routes Write")),
		"dns":             capability(dnsErr, "Zone · DNS · Edit", details.writeEvidence(zone, false, "DNS Write", "DNS Edit")),
		"redirect":        capability(redirectErr, "Zone · Single Redirect · Edit", details.writeEvidence(zone, false, "Single Redirect Write", "Single Redirect Edit")),
		"tunnel":          capability(tunnelErr, "Account · Cloudflare Tunnel · Edit", details.writeEvidence(zone, true, "Cloudflare Tunnel Write", "Cloudflare One Connectors Write", "Cloudflare One Connector: cloudflared Write")),
		"access":          capability(accessErr, "Access: Apps and Policies · Edit（所选账户或 Zone）", details.writeEvidence(zone, scope == "accounts", "Access: Apps and Policies Write")),
		"spectrum":        capability(spectrumErr, "Zone · Zone Settings · Edit；另需 Spectrum 套餐授权", details.writeEvidence(zone, false, "Zone Settings Write")),
	}
	// The zone response can carry effective DNS permissions even when token
	// introspection is forbidden. Do not guess other product permission names.
	if dnsErr == nil && len(details.Policies) == 0 {
		for _, p := range zone.Permissions {
			if p == "#dns_records:edit" {
				capabilities["dns"] = capability(nil, "Zone · DNS · Edit", "write")
			}
		}
	}
	domains := map[string]Domain{}
	for _, record := range records {
		if (record.Type != "A" && record.Type != "AAAA" && record.Type != "CNAME") || !HostInZone(record.Name, zone.Name) {
			continue
		}
		domain, exists := domains[record.Name]
		if !exists {
			domain = Domain{Hostname: record.Name, Proxied: true}
		}
		domain.Proxied = domain.Proxied && record.Proxied
		domains[record.Name] = domain
	}
	list := []Domain{}
	for _, domain := range domains {
		domain.AccessEligible = domain.Proxied && zone.Status == "active" && !strings.Contains(domain.Hostname, "*") && accessErr == nil
		switch {
		case zone.Status != "active":
			domain.Reason = "Zone 尚未激活"
		case strings.Contains(domain.Hostname, "*"):
			domain.Reason = "请使用明确的主机名，不能直接选择通配符"
		case !domain.Proxied:
			domain.Reason = "DNS only 不经过 Access；需先改为代理或 Tunnel"
		case accessErr != nil:
			domain.Reason = capabilities["access"].Message
		default:
			domain.Reason = "可为此主机配置 Access；请确认服务通过 HTTPS 或 Tunnel 客户端访问"
		}
		for _, app := range apps {
			if app.Covers(domain.Hostname) {
				domain.ApplicationID = app.ID
				domain.AccessEligible = false
				domain.Reason = "已有 Access 应用覆盖此域名；由原应用管理"
			}
		}
		list = append(list, domain)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Hostname < list[j].Hostname })
	if apps == nil {
		apps = []AccessApplication{}
	}
	return Inspection{Zone: zone, Capabilities: capabilities, Domains: list, Applications: apps, AccessScope: scope, CheckedAt: time.Now()}, nil
}

func capability(err error, permission, evidence string) Capability {
	c := Capability{State: "unverified", Permission: permission, Message: "读取成功；写权限尚未证实，提交配置时由 Cloudflare 校验"}
	if err != nil {
		c.State, c.Message = "error", "检测失败，请重试；不能据此判断权限不足"
		if permissionDenied(err) {
			c.State, c.Message = "unavailable", "缺少读取权限或不在 Token 资源范围内，请核对所需权限"
		}
		var apiErr *APIError
		if errors.As(err, &apiErr) && apiErr.StatusCode == 404 {
			c.State, c.Message = "unavailable", "资源尚未开通或不可访问，请核对账户、套餐与权限"
		}
		return c
	}
	if evidence == "write" {
		c.State, c.Message = "available", "已确认写权限；实际配置仍受套餐、配额和资源状态限制"
	}
	if evidence == "read" {
		c.State, c.Message = "read_only", "仅有读取权限；配置需要 Edit / Write 权限"
	}
	return c
}

func permissionDenied(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && (apiErr.StatusCode == 401 || apiErr.StatusCode == 403)
}

func (t tokenDetails) writeEvidence(zone Zone, accountScope bool, names ...string) string {
	// Unknown group IDs, deny policies and resource conditions must never be
	// interpreted as proof of write access.
	for _, policy := range t.Policies {
		if policy.Effect != "allow" {
			return ""
		}
	}
	read := false
	for _, policy := range t.Policies {
		if !resourceMatches(policy.Resources, zone, accountScope) {
			continue
		}
		for _, group := range policy.PermissionGroups {
			for _, name := range names {
				if group.Name == name {
					return "write"
				}
				if group.Name == strings.TrimSuffix(name, "Write")+"Read" {
					read = true
				}
			}
		}
	}
	if read {
		return "read"
	}
	return ""
}

func resourceMatches(resources map[string]json.RawMessage, zone Zone, accountScope bool) bool {
	for key, value := range resources {
		account := key == "com.cloudflare.api.account.*" || (zone.Account.ID != "" && key == "com.cloudflare.api.account."+zone.Account.ID)
		zoneMatch := !accountScope && (key == "com.cloudflare.api.account.zone.*" || key == "com.cloudflare.api.account.zone."+zone.ID)
		if !account && !zoneMatch {
			continue
		}
		var leaf string
		if json.Unmarshal(value, &leaf) == nil && leaf == "*" {
			if zoneMatch || account {
				return true
			}
		}
		var nested map[string]json.RawMessage
		if account && !accountScope && json.Unmarshal(value, &nested) == nil && resourceMatches(nested, zone, false) {
			return true
		}
	}
	return false
}

func (c *Client) DNSRecords(ctx context.Context, zoneID, hostname string) ([]DNSRecord, error) {
	path := "/zones/" + escaped(zoneID) + "/dns_records"
	if hostname != "" {
		path += "?name=" + escaped(hostname)
	}
	return listAll[DNSRecord](ctx, c, path, 100)
}
