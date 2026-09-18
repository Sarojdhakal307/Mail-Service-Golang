package store

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
)

// Cipher encrypts API keys at rest with AES-256-GCM so the admin can reveal them later.
type Cipher struct {
	aead cipher.AEAD
}

// NewCipher takes a 32-byte key encoded as 64 hex characters.
func NewCipher(hexKey string) (*Cipher, error) {
	key, err := hex.DecodeString(hexKey)
	if err != nil || len(key) != 32 {
		return nil, errors.New("API_KEY_ENCRYPTION_KEY must be 64 hex characters (generate with: openssl rand -hex 32)")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Cipher{aead: aead}, nil
}

// Seal encrypts plain, binding it to aad (the key hash) so ciphertexts cannot be swapped between rows.
func (c *Cipher) Seal(plain string, aad []byte) ([]byte, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return c.aead.Seal(nonce, nonce, []byte(plain), aad), nil
}

func (c *Cipher) Open(data, aad []byte) (string, error) {
	n := c.aead.NonceSize()
	if len(data) < n {
		return "", errors.New("ciphertext too short")
	}
	plain, err := c.aead.Open(nil, data[:n], data[n:], aad)
	if err != nil {
		return "", fmt.Errorf("cannot decrypt API key (was API_KEY_ENCRYPTION_KEY changed?): %w", err)
	}
	return string(plain), nil
}
