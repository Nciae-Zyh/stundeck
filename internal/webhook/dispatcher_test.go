package webhook

import (
	"net"
	"strings"
	"testing"
)

func TestValidateURLBlocksLocalTargets(t *testing.T) {
	if err := ValidateURL("http://127.0.0.1:8080/hook", false); err == nil {
		t.Fatal("expected local webhook to be blocked")
	}
	if err := ValidateURL("http://127.0.0.1:8080/hook", true); err != nil {
		t.Fatalf("explicitly allowed private webhook rejected: %v", err)
	}
}

func TestSign(t *testing.T) {
	signature := sign("secret", "1700000000", []byte(`{"ok":true}`))
	if len(signature) != 64 || strings.Contains(signature, "secret") {
		t.Fatalf("unexpected signature %q", signature)
	}
}

func TestPrivateAddressPolicyBlocksSpecialNetworks(t *testing.T) {
	blocked := []string{
		"0.0.0.1",
		"100.64.0.1",
		"192.0.2.10",
		"198.18.0.1",
		"224.0.0.1",
		"2001:db8::1",
	}
	for _, value := range blocked {
		if !isPrivateAddress(net.ParseIP(value)) {
			t.Errorf("expected %s to be blocked", value)
		}
	}
	if isPrivateAddress(net.ParseIP("1.1.1.1")) {
		t.Fatal("public address was blocked")
	}
}
