package encryption

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
)

// Cipher encrypts integration secrets with AES-256-GCM. GCM authenticates the
// ciphertext, so modified database values fail instead of decrypting silently.
type Cipher struct {
	aead cipher.AEAD
}

func New(key []byte) (*Cipher, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("encryption key must contain exactly 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create encryption cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create authenticated encryption: %w", err)
	}
	return &Cipher{aead: aead}, nil
}

func (c *Cipher) Encrypt(plaintext string) (string, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("create encryption nonce: %w", err)
	}
	sealed := c.aead.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.RawStdEncoding.EncodeToString(sealed), nil
}

func (c *Cipher) Decrypt(encodedCiphertext string) (string, error) {
	ciphertext, err := base64.RawStdEncoding.DecodeString(encodedCiphertext)
	if err != nil || len(ciphertext) < c.aead.NonceSize() {
		return "", fmt.Errorf("invalid encrypted value")
	}
	nonce := ciphertext[:c.aead.NonceSize()]
	plaintext, err := c.aead.Open(nil, nonce, ciphertext[c.aead.NonceSize():], nil)
	if err != nil {
		return "", fmt.Errorf("decrypt value: %w", err)
	}
	return string(plaintext), nil
}
