package security

import (
	"strings"
	"testing"
	"time"
)

func TestTOTPValidation(t *testing.T) {
	secret := "JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP"
	now := time.Unix(1_700_000_000, 0)
	code, err := totpCode(secret, now.Unix()/totpPeriod)
	if err != nil {
		t.Fatal(err)
	}
	if !ValidateTOTP(secret, code, now) {
		t.Fatal("valid totp code rejected")
	}
	if ValidateTOTP(secret, "000000", now) && code != "000000" {
		t.Fatal("invalid totp code accepted")
	}
}

func TestTOTPURI(t *testing.T) {
	uri := TOTPURI("StunDeck", "admin@example.com", "ABCDEF234567")
	if !strings.HasPrefix(uri, "otpauth://totp/") || !strings.Contains(uri, "issuer=StunDeck") {
		t.Fatalf("unexpected totp uri: %s", uri)
	}
}
