package secure

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
)

const encryptedPrefix = "v1:"

type Cipher struct {
	gcm cipher.AEAD
}

func NewCipherFromBase64(key string) (Cipher, error) {
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(key))
	if err != nil {
		return Cipher{}, fmt.Errorf("decode encryption key: %w", err)
	}
	if len(decoded) != 32 {
		return Cipher{}, errors.New("encryption key must decode to exactly 32 bytes")
	}
	block, err := aes.NewCipher(decoded)
	if err != nil {
		return Cipher{}, fmt.Errorf("create AES cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return Cipher{}, fmt.Errorf("create GCM cipher: %w", err)
	}
	return Cipher{gcm: gcm}, nil
}

func (c Cipher) Encrypt(plaintext string) (string, error) {
	if c.gcm == nil {
		return "", errors.New("cipher is not configured")
	}
	nonce := make([]byte, c.gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("create encryption nonce: %w", err)
	}
	ciphertext := c.gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return encryptedPrefix + base64.StdEncoding.EncodeToString(ciphertext), nil
}

func (c Cipher) Decrypt(value string) (string, error) {
	if c.gcm == nil {
		return "", errors.New("cipher is not configured")
	}
	if !strings.HasPrefix(value, encryptedPrefix) {
		return "", errors.New("value is not encrypted with a supported version")
	}
	ciphertext, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(value, encryptedPrefix))
	if err != nil {
		return "", fmt.Errorf("decode encrypted value: %w", err)
	}
	nonceSize := c.gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return "", errors.New("encrypted value is too short")
	}
	plaintext, err := c.gcm.Open(nil, ciphertext[:nonceSize], ciphertext[nonceSize:], nil)
	if err != nil {
		return "", errors.New("decrypt value: authentication failed")
	}
	return string(plaintext), nil
}
