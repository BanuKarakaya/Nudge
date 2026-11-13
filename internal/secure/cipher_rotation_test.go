package secure

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

func TestKeyRotationAndRowBinding(t *testing.T) {
	old := base64.StdEncoding.EncodeToString(make([]byte, 32))
	next := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("n", 32)))
	config := func(active string) string {
		b, _ := json.Marshal(map[string]any{"active": active, "keys": map[string]string{"old": old, "next": next}})
		return string(b)
	}
	a, _ := NewCipherFromBase64(config("old"))
	ciphertext, err := a.EncryptFor("private-token", "user:1")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := NewCipherFromBase64(config("next"))
	plain, err := b.DecryptFor(ciphertext, "user:1")
	if err != nil || plain != "private-token" || !b.NeedsRotation(ciphertext) {
		t.Fatal("rotation failed", err)
	}
	if _, err = b.DecryptFor(ciphertext, "user:2"); err == nil {
		t.Fatal("cross-row ciphertext accepted")
	}
	updated, _ := b.EncryptFor(plain, "user:1")
	if b.NeedsRotation(updated) {
		t.Fatal("active key not used")
	}
}
