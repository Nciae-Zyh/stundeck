package security

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
)

func RandomToken(bytes int) (string, error) {
	buffer := make([]byte, bytes)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("generate random token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}
