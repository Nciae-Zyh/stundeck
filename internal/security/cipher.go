package security

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const keySize = 32

type Cipher struct {
	aead cipher.AEAD
}

func LoadOrCreateCipher(path string) (*Cipher, error) {
	key, err := loadOrCreateKey(path)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create gcm: %w", err)
	}
	return &Cipher{aead: aead}, nil
}

func (c *Cipher) Encrypt(plaintext string) (string, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("generate encryption nonce: %w", err)
	}
	sealed := c.aead.Seal(nonce, nonce, []byte(plaintext), []byte("stundeck:v1"))
	return "v1." + base64.RawURLEncoding.EncodeToString(sealed), nil
}

func (c *Cipher) Decrypt(value string) (string, error) {
	if len(value) < 4 || value[:3] != "v1." {
		return "", errors.New("unsupported ciphertext version")
	}
	encoded, err := base64.RawURLEncoding.DecodeString(value[3:])
	if err != nil {
		return "", errors.New("invalid ciphertext")
	}
	nonceSize := c.aead.NonceSize()
	if len(encoded) < nonceSize {
		return "", errors.New("invalid ciphertext length")
	}
	plaintext, err := c.aead.Open(nil, encoded[:nonceSize], encoded[nonceSize:], []byte("stundeck:v1"))
	if err != nil {
		return "", errors.New("decrypt ciphertext")
	}
	return string(plaintext), nil
}

func loadOrCreateKey(path string) ([]byte, error) {
	key, err := os.ReadFile(path)
	if err == nil {
		if len(key) != keySize {
			return nil, fmt.Errorf("master key must be exactly %d bytes", keySize)
		}
		return key, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read master key: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create master key directory: %w", err)
	}
	key = make([]byte, keySize)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generate master key: %w", err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return loadOrCreateKey(path)
		}
		return nil, fmt.Errorf("create master key: %w", err)
	}
	defer file.Close()
	if _, err := file.Write(key); err != nil {
		return nil, fmt.Errorf("write master key: %w", err)
	}
	return key, nil
}
