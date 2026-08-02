package httpapi

import (
	"net"
	"reflect"
	"testing"
)

func TestNormalizeAccessPolicy(t *testing.T) {
	policy, err := normalizeAccessPolicy("PUBLIC", []string{"Panel.Example.com.", "*.home.example.com", "192.168.1.10"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"panel.example.com", "*.home.example.com", "192.168.1.10"}
	if policy.Mode != "public" || !reflect.DeepEqual(policy.AllowedHosts, want) {
		t.Fatalf("unexpected policy: %#v", policy)
	}
}

func TestPublicAccessRequiresHostAllowlist(t *testing.T) {
	if _, err := normalizeAccessPolicy("public", nil); err == nil {
		t.Fatal("public policy without a Host allowlist was accepted")
	}
}

func TestAccessPolicyMatching(t *testing.T) {
	if !sourceAllowed("lan", net.ParseIP("192.168.1.20")) {
		t.Fatal("private source should be allowed in lan mode")
	}
	if sourceAllowed("lan", net.ParseIP("1.1.1.1")) {
		t.Fatal("public source should be denied in lan mode")
	}
	if !hostAllowed("node.home.example.com", []string{"*.home.example.com"}) {
		t.Fatal("wildcard hostname did not match")
	}
	if hostAllowed("home.example.com", []string{"*.home.example.com"}) {
		t.Fatal("wildcard unexpectedly matched its apex")
	}
	if got := requestHost("[2001:db8::1]:8080"); got != "2001:db8::1" {
		t.Fatalf("requestHost() = %q", got)
	}
}
