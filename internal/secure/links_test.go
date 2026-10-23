package secure

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

func TestAccountLinksBindUserPurposeAndExpiry(t *testing.T) {
	signer, err := NewLinkSigner(base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32))))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1000, 0)
	token, err := signer.Token(7, "feedbin", now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, token, purpose string
		user                 int64
		at                   time.Time
		want                 bool
	}{
		{"valid", token, "feedbin", 7, now, true},
		{"different user", token, "feedbin", 8, now, false},
		{"different service", token, "raindrop", 7, now, false},
		{"expired", token, "feedbin", 7, now.Add(time.Hour), false},
		{"tampered", "x" + token[1:], "feedbin", 7, now, false},
		{"missing", "", "feedbin", 7, now, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := signer.Verify(tc.token, tc.user, tc.purpose, tc.at); got != tc.want {
				t.Fatalf("Verify=%v", got)
			}
		})
	}
	if (LinkSigner{}).Verify(token, 7, "feedbin", now) {
		t.Fatal("unconfigured signer accepted token")
	}
	if _, err := NewLinkSigner(""); err == nil {
		t.Fatal("accepted empty key")
	}
}
