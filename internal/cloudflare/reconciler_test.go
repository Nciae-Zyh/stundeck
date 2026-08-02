package cloudflare

import (
	"strings"
	"testing"

	"github.com/Nciae-Zyh/stundeck/internal/store"
)

func TestBuildRedirectRule(t *testing.T) {
	service := store.Service{
		ID:             "service-1",
		Name:           "NAS",
		Scheme:         "http",
		EntryHostname:  "nas.example.com",
		RedirectStatus: 302,
		PreservePath:   true,
		PreserveQuery:  true,
		PublicIP:       "203.0.113.10",
		PublicPort:     45678,
	}
	rule, target, err := BuildRedirectRule(service)
	if err != nil {
		t.Fatal(err)
	}
	if target != "http://203.0.113.10:45678" {
		t.Fatalf("target = %q", target)
	}
	if !strings.Contains(rule.Expression, "nas.example.com") {
		t.Fatalf("expression = %q", rule.Expression)
	}
	fromValue := rule.ActionParameters["from_value"].(map[string]any)
	targetURL := fromValue["target_url"].(map[string]any)
	if !strings.Contains(targetURL["expression"].(string), "http.request.uri.path") {
		t.Fatalf("path expression = %v", targetURL)
	}
}

func TestBuildRedirectRuleRejectsPermanentStatus(t *testing.T) {
	_, _, err := BuildRedirectRule(store.Service{RedirectStatus: 301})
	if err == nil {
		t.Fatal("expected unsupported redirect status to fail")
	}
}
