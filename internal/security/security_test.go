package security

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCipherPersistsKeyAndRoundTrips(t *testing.T) {
	keyPath := filepath.Join(t.TempDir(), "master.key")
	cipher, err := LoadOrCreateCipher(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := cipher.Encrypt("cloudflare-token-value")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(ciphertext, "cloudflare-token-value") {
		t.Fatal("ciphertext contains plaintext")
	}
	reloaded, err := LoadOrCreateCipher(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	plaintext, err := reloaded.Decrypt(ciphertext)
	if err != nil {
		t.Fatal(err)
	}
	if plaintext != "cloudflare-token-value" {
		t.Fatalf("plaintext = %q", plaintext)
	}
	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("master key mode = %o", info.Mode().Perm())
	}
}

func TestPasswordHash(t *testing.T) {
	password := "a long local password"
	hash, err := HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyPassword(hash, password) {
		t.Fatal("valid password rejected")
	}
	if VerifyPassword(hash, "a different password") {
		t.Fatal("invalid password accepted")
	}
}

func TestPasswordMinimumLength(t *testing.T) {
	if _, err := HashPassword("too-short"); err == nil {
		t.Fatal("short password accepted")
	}
}
