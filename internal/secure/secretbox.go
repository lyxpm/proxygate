// Package secure provides small cryptography helpers shared by services:
// authenticated encryption for secrets at rest and random credential generation.
package secure

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"math/big"
)

// Box encrypts short secrets (DNS API credentials, tokens) with AES-256-GCM.
// The associated data binds a ciphertext to its record, e.g. "dns-provider:12".
type Box struct {
	aead cipher.AEAD
}

// NewBox creates a Box from a 32-byte key.
func NewBox(key []byte) (*Box, error) {
	if len(key) != 32 {
		return nil, errors.New("secure: key must be 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead}, nil
}

// Seal encrypts plaintext and returns base64(nonce||ciphertext).
func (b *Box) Seal(plaintext []byte, aad string) (string, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	out := b.aead.Seal(nonce, nonce, plaintext, []byte(aad))
	return base64.StdEncoding.EncodeToString(out), nil
}

// Open decrypts a value produced by Seal.
func (b *Box) Open(encoded, aad string) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("secure: decode: %w", err)
	}
	ns := b.aead.NonceSize()
	if len(raw) < ns+16 {
		return nil, errors.New("secure: ciphertext too short")
	}
	out, err := b.aead.Open(nil, raw[:ns], raw[ns:], []byte(aad))
	if err != nil {
		return nil, errors.New("secure: authentication failed")
	}
	return out, nil
}

const alphabet = "abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// RandomPassword returns a URL-safe password with the given length.
func RandomPassword(n int) (string, error) {
	if n < 8 {
		n = 8
	}
	out := make([]byte, n)
	max := big.NewInt(int64(len(alphabet)))
	for i := range out {
		v, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		out[i] = alphabet[v.Int64()]
	}
	return string(out), nil
}

// RandomToken returns n random bytes encoded as URL-safe base64 without padding.
func RandomToken(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
