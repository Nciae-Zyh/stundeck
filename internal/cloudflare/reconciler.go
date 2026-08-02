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
	RulesetID string `json:"rulesetId"`
	RuleID    string `json:"ruleId"`
	TargetURL string `json:"targetUrl"`
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
	var records []DNSRecord
	path := "/zones/" + escaped(zoneID) + "/dns_records?name=" + escaped(hostname)
	if err := c.do(ctx, http.MethodGet, path, nil, &records); err != nil {
		return err
	}
	marker := "managed-by=stundeck:" + serviceID
	payload := map[string]any{
		"type":    recordType,
		"name":    hostname,
		"content": publicIP,
		"proxied": proxied,
		"ttl":     1,
		"comment": marker,
	}
	if len(records) == 0 {
		var created DNSRecord
		return c.do(ctx, http.MethodPost, "/zones/"+escaped(zoneID)+"/dns_records", payload, &created)
	}
	record := records[0]
	if record.Type != recordType {
		return fmt.Errorf("hostname already has an incompatible %s record", record.Type)
	}
	if record.Comment != marker {
		return errors.New("hostname already exists and is not managed by StunDeck")
	}
	var updated DNSRecord
	return c.do(ctx, http.MethodPatch,
		"/zones/"+escaped(zoneID)+"/dns_records/"+escaped(record.ID),
		payload,
		&updated,
	)
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
	var created Rule
	err := c.do(ctx, http.MethodPost,
		"/zones/"+escaped(zoneID)+"/rulesets/"+escaped(rulesetID)+"/rules",
		rule,
		&created,
	)
	return created, err
}

func (c *Client) updateRule(ctx context.Context, zoneID, rulesetID, ruleID string, rule Rule) (Rule, error) {
	var updated Rule
	err := c.do(ctx, http.MethodPatch,
		"/zones/"+escaped(zoneID)+"/rulesets/"+escaped(rulesetID)+"/rules/"+escaped(ruleID),
		rule,
		&updated,
	)
	return updated, err
}
