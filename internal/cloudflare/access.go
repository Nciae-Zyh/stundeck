package cloudflare

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"path"
	"slices"
	"strings"

	"github.com/Nciae-Zyh/stundeck/internal/store"
)

type AccessPolicy struct {
	MFAConfig                    map[string]any   `json:"mfa_config,omitempty"`
	ApprovalRequired             bool             `json:"approval_required,omitempty"`
	IsolationRequired            bool             `json:"isolation_required,omitempty"`
	PurposeJustificationRequired bool             `json:"purpose_justification_required,omitempty"`
	ConnectionRules              map[string]any   `json:"connection_rules,omitempty"`
	ID                           string           `json:"id,omitempty"`
	Name                         string           `json:"name"`
	Decision                     string           `json:"decision"`
	Precedence                   int              `json:"precedence"`
	Include                      []map[string]any `json:"include"`
	Exclude                      []map[string]any `json:"exclude,omitempty"`
	Require                      []map[string]any `json:"require,omitempty"`
}

type AccessApplication struct {
	AllowedIDPs              []string       `json:"allowed_idps,omitempty"`
	MFAConfig                map[string]any `json:"mfa_config,omitempty"`
	EnableBindingCookie      *bool          `json:"enable_binding_cookie,omitempty"`
	AllowAuthenticateViaWarp *bool          `json:"allow_authenticate_via_warp,omitempty"`
	AutoRedirectToIdentity   *bool          `json:"auto_redirect_to_identity,omitempty"`
	CORSHeaders              map[string]any `json:"cors_headers,omitempty"`
	Managed                  bool           `json:"managed"`
	ID                       string         `json:"id,omitempty"`
	Name                     string         `json:"name"`
	Domain                   string         `json:"domain"`
	Type                     string         `json:"type"`
	SessionDuration          string         `json:"session_duration"`
	Destinations             []struct {
		Type string `json:"type"`
		URI  string `json:"uri"`
	} `json:"destinations,omitempty"`
	SelfHostedDomains []string       `json:"self_hosted_domains,omitempty"`
	Policies          []AccessPolicy `json:"policies"`
}

func (a AccessApplication) hosts() []string {
	hosts := []string{a.Domain}
	hosts = append(hosts, a.SelfHostedDomains...)
	for _, d := range a.Destinations {
		if d.Type == "public" {
			hosts = append(hosts, d.URI)
		}
	}
	return hosts
}

func (a AccessApplication) Covers(hostname string) bool {
	for _, uri := range a.hosts() {
		host := strings.ToLower(strings.SplitN(strings.TrimPrefix(strings.TrimPrefix(uri, "https://"), "http://"), "/", 2)[0])
		if matched, _ := path.Match(host, hostname); matched {
			return true
		}
	}
	return false
}

func (c *Client) AccessApplications(ctx context.Context, scope, id string) ([]AccessApplication, error) {
	return listAll[AccessApplication](ctx, c, "/"+scope+"/"+escaped(id)+"/access/apps", 100)
}

type AccessRequest struct {
	Hostname        string   `json:"hostname"`
	Emails          []string `json:"emails"`
	SessionDuration string   `json:"sessionDuration"`
}

func ValidateAccessRequest(input AccessRequest) (AccessRequest, error) {
	input.Hostname = strings.ToLower(strings.TrimSpace(input.Hostname))
	if input.Hostname == "" || strings.ContainsAny(input.Hostname, "*/: ") {
		return input, errors.New("Access 需要明确的完整域名，不支持通配符、端口或路径")
	}
	if len(input.Emails) == 0 || len(input.Emails) > 50 {
		return input, errors.New("请指定 1 至 50 个允许登录的完整邮箱；不允许空白或 Everyone 策略")
	}
	emails := []string{}
	for _, raw := range input.Emails {
		email := strings.ToLower(strings.TrimSpace(raw))
		parsed, err := mail.ParseAddress(email)
		if err != nil || parsed.Address != email || strings.Contains(email, "*") {
			return input, fmt.Errorf("邮箱格式不正确：%s", email)
		}
		if !slices.Contains(emails, email) {
			emails = append(emails, email)
		}
	}
	input.Emails = emails
	if input.SessionDuration == "" {
		input.SessionDuration = "24h"
	}
	if !slices.Contains([]string{"1h", "8h", "24h", "168h"}, input.SessionDuration) {
		return input, errors.New("会话时长必须为 1h、8h、24h 或 168h")
	}
	return input, nil
}

func (p *Publisher) Inspect(ctx context.Context, connectionID string) (Inspection, error) {
	connection, err := p.Store.CloudflareConnection(ctx, connectionID)
	if err != nil {
		return Inspection{}, err
	}
	token, err := p.Cipher.Decrypt(connection.TokenCiphertext)
	if err != nil {
		return Inspection{}, errors.New("无法解密 Cloudflare Token")
	}
	report, err := p.NewClient(token).Inspect(ctx, connection.ZoneID)
	if err != nil {
		return report, err
	}
	resources, err := p.Store.CloudflareResources(ctx, connectionID)
	if err != nil {
		return report, err
	}
	services, err := p.Store.Services(ctx)
	if err != nil {
		return report, err
	}
	for i := range report.Domains {
		d := &report.Domains[i]
		for _, resource := range resources {
			if resource.Kind == "access:"+report.AccessScope && resource.Hostname == d.Hostname && resource.RemoteID == d.ApplicationID {
				d.Managed = true
				d.AccessEligible = d.Proxied && report.Zone.Status == "active"
				d.Reason = "此域名的 Access 由 StunDeck 管理"
			}
		}
		for _, svc := range services {
			if svc.CloudflareConnectionID == connectionID && svc.EntryHostname == d.Hostname {
				d.ServiceID = svc.ID
				if svc.PublishMode == "redirect" || svc.PublishMode == "dns" || svc.PublishMode == "spectrum" || (svc.PublishMode == "proxy" && (svc.Scheme != "https" || svc.PublicPort != 443)) {
					d.AccessEligible = false
					d.Reason = "此服务的发布方式不适用 Access；请使用 Tunnel 或标准 HTTPS 443 代理"
				}
			}
		}
		for _, app := range report.Applications {
			if app.Covers(d.Hostname) && app.ID != d.ApplicationID {
				d.AccessEligible = false
				d.Reason = "有多个 Access 应用覆盖此域名，请先在 Cloudflare 整理规则"
			}
		}
	}
	// Return only applications related to this connection's zone.
	apps := []AccessApplication{}
	for _, app := range report.Applications {
		relevant := app.Covers(report.Zone.Name) || app.Covers("stundeck-probe."+report.Zone.Name)
		for _, uri := range app.hosts() {
			if HostInZone(strings.SplitN(uri, "/", 2)[0], report.Zone.Name) {
				relevant = true
			}
		}
		if relevant {
			apps = append(apps, app)
		}
	}
	for i := range apps {
		for _, resource := range resources {
			if resource.Kind == "access:"+report.AccessScope && resource.RemoteID == apps[i].ID {
				apps[i].Managed = true
			}
		}
	}
	report.Applications = apps
	return report, nil
}

func (p *Publisher) SaveAccess(ctx context.Context, connectionID string, input AccessRequest) (AccessApplication, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	input, err := ValidateAccessRequest(input)
	if err != nil {
		return AccessApplication{}, err
	}
	report, err := p.Inspect(ctx, connectionID)
	if err != nil {
		return AccessApplication{}, err
	}
	if !HostInZone(input.Hostname, report.Zone.Name) {
		return AccessApplication{}, errors.New("Access 域名不属于此连接的 Zone")
	}
	cap := report.Capabilities["access"]
	if cap.State == "unavailable" || cap.State == "error" || cap.State == "read_only" {
		return AccessApplication{}, errors.New(cap.Message + "；需要 " + cap.Permission)
	}
	eligible := false
	for _, d := range report.Domains {
		if d.Hostname == input.Hostname {
			eligible = d.AccessEligible
		}
	}
	if !eligible {
		return AccessApplication{}, errors.New("此域名不具备 Access 配置条件：需要有效的代理 DNS / Tunnel，且不能覆盖其他应用")
	}
	c, _, zone, err := p.ClientFor(ctx, connectionID)
	if err != nil {
		return AccessApplication{}, err
	}
	id := zone.Account.ID
	if report.AccessScope == "zones" {
		id = zone.ID
	}
	base := "/" + report.AccessScope + "/" + escaped(id) + "/access/apps"
	kind := "access:" + report.AccessScope
	resource, err := p.Store.CloudflareResource(ctx, connectionID, input.Hostname, kind)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return AccessApplication{}, err
	}
	policy := AccessPolicy{Name: "StunDeck allowed emails", Decision: "allow", Precedence: 1, Include: []map[string]any{}}
	for _, email := range input.Emails {
		policy.Include = append(policy.Include, map[string]any{"email": map[string]string{"email": email}})
	}
	method, endpoint := http.MethodPost, base
	var existing AccessApplication
	if resource.RemoteID != "" {
		if err = c.do(ctx, http.MethodGet, base+"/"+escaped(resource.RemoteID), nil, &existing); err != nil {
			return existing, err
		}
		if !ownedAccess(existing, input.Hostname) {
			return AccessApplication{}, errors.New("Access 应用已在外部改变域名或用途，拒绝覆盖")
		}
		if len(existing.Policies) != 1 {
			return AccessApplication{}, errors.New("Access 策略已在外部调整，拒绝覆盖多策略应用")
		}
		currentPolicy := existing.Policies[0]
		if currentPolicy.Decision != "allow" || len(currentPolicy.Exclude) > 0 || len(currentPolicy.Require) > 0 || currentPolicy.MFAConfig != nil || currentPolicy.ApprovalRequired || currentPolicy.IsolationRequired || currentPolicy.PurposeJustificationRequired || currentPolicy.ConnectionRules != nil {
			return AccessApplication{}, errors.New("Access 策略包含外部设置的拒绝或附加条件，拒绝覆盖")
		}
		for _, rule := range currentPolicy.Include {
			if len(rule) != 1 || rule["email"] == nil {
				return AccessApplication{}, errors.New("Access 策略包含非邮箱规则，拒绝覆盖")
			}
		}
		policy.ID = existing.Policies[0].ID
		method, endpoint = http.MethodPut, base+"/"+escaped(resource.RemoteID)
	}
	payload := map[string]any{"name": "StunDeck · " + input.Hostname, "domain": input.Hostname, "type": "self_hosted", "session_duration": input.SessionDuration, "http_only_cookie_attribute": true, "app_launcher_visible": false, "policies": []AccessPolicy{policy}}
	if existing.ID != "" {
		// An email allow-list edit must not broaden identity providers or remove
		// application-level MFA / cookie binding configured in Cloudflare.
		if existing.AllowedIDPs != nil {
			payload["allowed_idps"] = existing.AllowedIDPs
		}
		if existing.MFAConfig != nil {
			payload["mfa_config"] = existing.MFAConfig
		}
		if existing.EnableBindingCookie != nil {
			payload["enable_binding_cookie"] = *existing.EnableBindingCookie
		}
		if existing.AllowAuthenticateViaWarp != nil {
			payload["allow_authenticate_via_warp"] = *existing.AllowAuthenticateViaWarp
		}
		if existing.AutoRedirectToIdentity != nil {
			payload["auto_redirect_to_identity"] = *existing.AutoRedirectToIdentity
		}
		if existing.CORSHeaders != nil {
			payload["cors_headers"] = existing.CORSHeaders
		}
	}
	var app AccessApplication
	if err = c.do(ctx, method, endpoint, payload, &app); err != nil {
		return app, fmt.Errorf("配置 Access 失败，需要 Access: Apps and Policies Edit：%w", err)
	}
	if app.ID == "" {
		return app, errors.New("Cloudflare 未返回 Access 应用 ID")
	}
	// Persist before verification. A failed read-back must not orphan a protective
	// application or make a retry create a second one.
	if err = p.Store.SaveCloudflareResource(ctx, store.CloudflareResource{ConnectionID: connectionID, OwnerID: input.Hostname, Kind: kind, RemoteID: app.ID, Hostname: input.Hostname}); err != nil {
		return app, fmt.Errorf("Access 已写入，但本地所有权保存失败；请在 Cloudflare 检查应用 %s：%w", app.ID, err)
	}
	var verified AccessApplication
	if err = c.do(ctx, http.MethodGet, base+"/"+escaped(app.ID), nil, &verified); err != nil {
		return app, fmt.Errorf("Access 已写入但回读失败，请刷新确认：%w", err)
	}
	if !ownedAccess(verified, input.Hostname) || verified.SessionDuration != input.SessionDuration {
		return app, errors.New("Access 已写入，但回读配置不一致，请刷新检查")
	}
	policies, err := listAll[AccessPolicy](ctx, c, base+"/"+escaped(app.ID)+"/policies", 100)
	if err != nil {
		return app, fmt.Errorf("Access 已写入，但策略回读失败：%w", err)
	}
	if !matchesAccessPolicy(policies, input.Emails) {
		return app, errors.New("Access 已写入，但回读的邮箱允许策略不一致，请在 Cloudflare 检查")
	}
	verified.Policies = policies
	return verified, nil
}

func matchesAccessPolicy(policies []AccessPolicy, emails []string) bool {
	if len(policies) != 1 {
		return false
	}
	p := policies[0]
	if p.Decision != "allow" || len(p.Exclude) > 0 || len(p.Require) > 0 || len(p.Include) != len(emails) {
		return false
	}
	found := map[string]bool{}
	for _, rule := range p.Include {
		if len(rule) != 1 {
			return false
		}
		value, ok := rule["email"].(map[string]any)
		if !ok {
			return false
		}
		email, ok := value["email"].(string)
		if !ok || !slices.Contains(emails, email) || found[email] {
			return false
		}
		found[email] = true
	}
	return len(found) == len(emails)
}

func ownedAccess(app AccessApplication, hostname string) bool {
	if app.Domain != hostname || app.Name != "StunDeck · "+hostname || app.Type != "self_hosted" {
		return false
	}
	for _, host := range app.hosts() {
		if host != hostname {
			return false
		}
	}
	for _, d := range app.Destinations {
		if d.Type != "public" {
			return false
		}
	}
	return true
}

func (p *Publisher) DeleteAccess(ctx context.Context, connectionID, hostname string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	c, _, zone, err := p.ClientFor(ctx, connectionID)
	if err != nil {
		return err
	}
	if !HostInZone(hostname, zone.Name) {
		return errors.New("Access 域名不属于此 Zone")
	}
	found := false
	for _, scope := range []string{"accounts", "zones"} {
		resource, err := p.Store.CloudflareResource(ctx, connectionID, hostname, "access:"+scope)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		found = true
		id := zone.Account.ID
		if scope == "zones" {
			id = zone.ID
		}
		endpoint := "/" + scope + "/" + escaped(id) + "/access/apps/" + escaped(resource.RemoteID)
		var app AccessApplication
		if err = c.do(ctx, http.MethodGet, endpoint, nil, &app); err == nil {
			if !ownedAccess(app, hostname) {
				return errors.New("Access 应用已在外部改变，拒绝删除")
			}
			err = c.do(ctx, http.MethodDelete, endpoint, nil, nil)
		}
		if err != nil && !isNotFound(err) {
			return err
		}
		if err = p.Store.DeleteCloudflareResource(ctx, connectionID, hostname, "access:"+scope); err != nil {
			return err
		}
	}
	if !found {
		return errors.New("此域名没有由 StunDeck 管理的 Access 应用")
	}
	return nil
}
