package cloudflare

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/Nciae-Zyh/stundeck/internal/store"
)

const redirectPhase = "http_request_dynamic_redirect"

type DNSRecord struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	Proxied bool   `json:"proxied"`
	Comment string `json:"comment"`
}

type Ruleset struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Kind  string `json:"kind"`
	Phase string `json:"phase"`
	Rules []Rule `json:"rules"`
}

type Rule struct {
	ID               string         `json:"id,omitempty"`
	Ref              string         `json:"ref"`
	Action           string         `json:"action"`
	ActionParameters map[string]any `json:"action_parameters"`
	Expression       string         `json:"expression"`
	Description      string         `json:"description"`
	Enabled          bool           `json:"enabled"`
}

type SyncResult struct {
	ResourceID string `json:"resourceId,omitempty"`
	RulesetID  string `json:"rulesetId"`
	RuleID     string `json:"ruleId"`
	TargetURL  string `json:"targetUrl"`
}

func (c *Client) ReconcileService(ctx context.Context, zoneID string, service store.Service) (SyncResult, error) {
	if service.PublishMode != "redirect" {
		return SyncResult{}, nil
	}
	if net.ParseIP(service.PublicIP) == nil || service.PublicPort < 1 {
		return SyncResult{}, errors.New("service has no active public mapping")
	}
	if service.ManageDNS {
		if err := c.ensureDNSRecord(ctx, zoneID, service.EntryHostname, service.PublicIP, true, service.ID); err != nil {
			return SyncResult{}, fmt.Errorf("sync entry dns: %w", err)
		}
		if service.OriginHostname != "" {
			if err := c.ensureDNSRecord(ctx, zoneID, service.OriginHostname, service.PublicIP, false, service.ID); err != nil {
				return SyncResult{}, fmt.Errorf("sync origin dns: %w", err)
			}
		}
	}

	rule, targetURL, err := BuildRedirectRule(service)
	if err != nil {
		return SyncResult{}, err
	}
	ruleset, err := c.redirectRuleset(ctx, zoneID)
	if err != nil && !isNotFound(err) {
		return SyncResult{}, err
	}
	if ruleset.ID == "" {
		created, err := c.createRedirectRuleset(ctx, zoneID, rule)
		if err != nil {
			return SyncResult{}, err
		}
		if len(created.Rules) == 0 {
			return SyncResult{}, errors.New("cloudflare created a ruleset without a rule")
		}
		return SyncResult{RulesetID: created.ID, RuleID: created.Rules[0].ID, TargetURL: targetURL}, nil
	}

	fullRuleset, err := c.ruleset(ctx, zoneID, ruleset.ID)
	if err != nil {
		return SyncResult{}, err
	}
	for _, existing := range fullRuleset.Rules {
		if existing.Ref == rule.Ref {
			updated, err := c.updateRule(ctx, zoneID, ruleset.ID, existing.ID, rule)
			if err != nil {
				return SyncResult{}, err
			}
			return SyncResult{RulesetID: ruleset.ID, RuleID: updated.ID, TargetURL: targetURL}, nil
		}
	}
	created, err := c.createRule(ctx, zoneID, ruleset.ID, rule)
	if err != nil {
		return SyncResult{}, err
	}
	return SyncResult{RulesetID: ruleset.ID, RuleID: created.ID, TargetURL: targetURL}, nil
}

func BuildRedirectRule(service store.Service) (Rule, string, error) {
	if service.RedirectStatus != 302 && service.RedirectStatus != 307 {
		return Rule{}, "", errors.New("redirect status must be 302 or 307")
	}
	if service.EntryHostname == "" {
		return Rule{}, "", errors.New("entry hostname is required")
	}
	host := service.OriginHostname
	if host == "" {
		host = service.PublicIP
		if strings.Contains(host, ":") {
			host = "[" + host + "]"
		}
	}
	targetURL := fmt.Sprintf("%s://%s:%d", service.Scheme, host, service.PublicPort)
	target := map[string]any{"value": targetURL}
	if service.PreservePath {
		target = map[string]any{"expression": "concat(" + strconv.Quote(targetURL) + ", http.request.uri.path)"}
	}
	rule := Rule{
		Ref:         "stundeck_" + strings.ReplaceAll(service.ID, "-", "_"),
		Action:      "redirect",
		Expression:  `(http.host eq ` + strconv.Quote(strings.ToLower(service.EntryHostname)) + `)`,
		Description: "Managed by StunDeck for " + service.Name,
		Enabled:     true,
		ActionParameters: map[string]any{
			"from_value": map[string]any{
				"target_url":            target,
				"status_code":           service.RedirectStatus,
				"preserve_query_string": service.PreserveQuery,
			},
		},
	}
	return rule, targetURL, nil
}

func (c *Client) ensureDNSRecord(ctx context.Context, zoneID, hostname, publicIP string, proxied bool, serviceID string) error {
	recordType := "A"
	if strings.Contains(publicIP, ":") {
		recordType = "AAAA"
	}
	return c.ensureRecord(ctx, zoneID, hostname, recordType, publicIP, proxied, serviceID)
}

func (c *Client) ownedDNS(ctx context.Context, zoneID, hostname, serviceID string) ([]DNSRecord, error) {
	records, err := c.DNSRecords(ctx, zoneID, hostname)
	if err != nil {
		return nil, err
	}
	if len(records) > 1 {
		return nil, errors.New("hostname has multiple DNS records; resolve the conflict before publishing")
	}
	for _, record := range records {
		if record.Comment != "managed-by=stundeck:"+serviceID {
			return nil, errors.New("hostname already exists and is not managed by this StunDeck service")
		}
	}
	return records, nil
}

func (c *Client) ensureRecord(ctx context.Context, zoneID, hostname, recordType, content string, proxied bool, serviceID string) error {
	records, err := c.ownedDNS(ctx, zoneID, hostname, serviceID)
	if err != nil {
		return err
	}
	payload := map[string]any{"type": recordType, "name": hostname, "content": content, "proxied": proxied, "ttl": 1, "comment": "managed-by=stundeck:" + serviceID}
	path := "/zones/" + escaped(zoneID) + "/dns_records"
	method := http.MethodPost
	if len(records) > 0 {
		if records[0].Type == recordType && records[0].Content == content && records[0].Proxied == proxied {
			return nil
		}
		if records[0].Type != recordType {
			return fmt.Errorf("hostname already has an incompatible %s record; clean up the old publishing configuration first", records[0].Type)
		}
		path += "/" + escaped(records[0].ID)
		method = http.MethodPatch
	}
	return c.do(ctx, method, path, payload, nil)
}

func (c *Client) redirectRuleset(ctx context.Context, zoneID string) (Ruleset, error) {
	var rulesets []Ruleset
	if err := c.do(ctx, http.MethodGet, "/zones/"+escaped(zoneID)+"/rulesets", nil, &rulesets); err != nil {
		return Ruleset{}, err
	}
	for _, ruleset := range rulesets {
		if ruleset.Phase == redirectPhase && ruleset.Kind == "zone" {
			return ruleset, nil
		}
	}
	return Ruleset{}, nil
}

func (c *Client) ruleset(ctx context.Context, zoneID, rulesetID string) (Ruleset, error) {
	var ruleset Ruleset
	err := c.do(ctx, http.MethodGet,
		"/zones/"+escaped(zoneID)+"/rulesets/"+escaped(rulesetID),
		nil,
		&ruleset,
	)
	return ruleset, err
}

func (c *Client) createRedirectRuleset(ctx context.Context, zoneID string, rule Rule) (Ruleset, error) {
	payload := map[string]any{
		"name":  "StunDeck redirects",
		"kind":  "zone",
		"phase": redirectPhase,
		"rules": []Rule{rule},
	}
	var ruleset Ruleset
	err := c.do(ctx, http.MethodPost, "/zones/"+escaped(zoneID)+"/rulesets", payload, &ruleset)
	return ruleset, err
}

func (c *Client) createRule(ctx context.Context, zoneID, rulesetID string, rule Rule) (Rule, error) {
	return c.writeRule(ctx, http.MethodPost, "/zones/"+escaped(zoneID)+"/rulesets/"+escaped(rulesetID)+"/rules", rule)
}

func (c *Client) updateRule(ctx context.Context, zoneID, rulesetID, ruleID string, rule Rule) (Rule, error) {
	return c.writeRule(ctx, http.MethodPatch, "/zones/"+escaped(zoneID)+"/rulesets/"+escaped(rulesetID)+"/rules/"+escaped(ruleID), rule)
}

func (c *Client) writeRule(ctx context.Context, method, path string, rule Rule) (Rule, error) {
	var ruleset Ruleset
	if err := c.do(ctx, method, path, rule, &ruleset); err != nil {
		return Rule{}, err
	}
	for _, existing := range ruleset.Rules {
		if existing.Ref == rule.Ref {
			return existing, nil
		}
	}
	return Rule{}, errors.New("Cloudflare response did not contain the managed redirect rule")
}
