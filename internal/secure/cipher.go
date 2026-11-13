package secure

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
)

// A keyring accepts either one base64 key (legacy configuration) or JSON:
// {"active":"2026-11","keys":{"2026-11":"base64...","2026-10":"base64..."}}.
// Retain old keys until all ciphertext has been rewritten and backups expired.
type Cipher struct {
	active string
	keys   map[string]cipher.AEAD
}

func NewCipherFromBase64(config string) (Cipher, error) {
	var ring struct {
		Active string            `json:"active"`
		Keys   map[string]string `json:"keys"`
	}
	config = strings.TrimSpace(config)
	if strings.HasPrefix(config, "{") {
		if json.Unmarshal([]byte(config), &ring) != nil || ring.Active == "" || len(ring.Keys) == 0 || len(ring.Keys) > 16 {
			return Cipher{}, errors.New("invalid encryption keyring")
		}
	} else {
		sum := sha256.Sum256([]byte(config))
		ring.Active = hex.EncodeToString(sum[:8])
		ring.Keys = map[string]string{ring.Active: config}
	}
	c := Cipher{active: ring.Active, keys: map[string]cipher.AEAD{}}
	for id, key := range ring.Keys {
		if id == "" || len(id) > 64 || strings.ContainsAny(id, ": \r\n") {
			return Cipher{}, errors.New("invalid encryption key ID")
		}
		raw, err := base64.StdEncoding.DecodeString(key)
		if err != nil || len(raw) != 32 {
			return Cipher{}, errors.New("encryption key must decode to exactly 32 bytes")
		}
		block, err := aes.NewCipher(raw)
		if err != nil {
			return Cipher{}, err
		}
		gcm, err := cipher.NewGCM(block)
		if err != nil {
			return Cipher{}, err
		}
		c.keys[id] = gcm
	}
	if c.keys[c.active] == nil {
		return Cipher{}, errors.New("active encryption key missing")
	}
	return c, nil
}
func (c Cipher) Configured() bool                     { return c.keys[c.active] != nil }
func (c Cipher) Encrypt(plain string) (string, error) { return c.EncryptFor(plain, "") }
func (c Cipher) EncryptFor(plain, binding string) (string, error) {
	gcm := c.keys[c.active]
	if gcm == nil {
		return "", errors.New("cipher is not configured")
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	prefix := "v2:" + c.active + ":"
	sealed := gcm.Seal(nonce, nonce, []byte(plain), []byte(prefix+binding))
	return prefix + base64.StdEncoding.EncodeToString(sealed), nil
}
func (c Cipher) Decrypt(value string) (string, error) { return c.DecryptFor(value, "") }
func (c Cipher) DecryptFor(value, binding string) (string, error) {
	if !c.Configured() {
		return "", errors.New("cipher is not configured")
	}
	if strings.HasPrefix(value, "v1:") {
		// v1 did not have key IDs/AAD. Read only for migration compatibility.
		data, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(value, "v1:"))
		if err != nil {
			return "", errors.New("invalid ciphertext")
		}
		for _, gcm := range c.keys {
			if len(data) >= gcm.NonceSize() {
				plain, err := gcm.Open(nil, data[:gcm.NonceSize()], data[gcm.NonceSize():], nil)
				if err == nil {
					return string(plain), nil
				}
			}
		}
		return "", errors.New("ciphertext authentication failed")
	}
	parts := strings.SplitN(value, ":", 3)
	if len(parts) != 3 || parts[0] != "v2" {
		return "", errors.New("unsupported ciphertext version")
	}
	gcm := c.keys[parts[1]]
	if gcm == nil {
		return "", errors.New("encryption key unavailable")
	}
	data, err := base64.StdEncoding.DecodeString(parts[2])
	if err != nil || len(data) < gcm.NonceSize() {
		return "", errors.New("invalid ciphertext")
	}
	plain, err := gcm.Open(nil, data[:gcm.NonceSize()], data[gcm.NonceSize():], []byte("v2:"+parts[1]+":"+binding))
	if err != nil {
		return "", errors.New("ciphertext authentication failed")
	}
	return string(plain), nil
}

func (c Cipher) NeedsRotation(value string) bool {
	return c.Configured() && !strings.HasPrefix(value, "v2:"+c.active+":")
}
