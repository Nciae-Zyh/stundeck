package security

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
)

func RandomToken(bytes int) (string, error) {
	buffer, err := randomBytes(bytes)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}

func randomBytes(size int) ([]byte, error) {
	buffer := make([]byte, size)
	if _, err := rand.Read(buffer); err != nil {
		return nil, fmt.Errorf("generate random bytes: %w", err)
	}
	return buffer, nil
}
