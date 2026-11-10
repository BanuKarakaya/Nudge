package secure

import (
	"encoding/base64"
	"net/url"
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

func TestWorkspaceLinksAreShortLivedAndPurposeBound(t *testing.T) {
	signer, _ := NewLinkSigner(base64.StdEncoding.EncodeToString(make([]byte, 32)))
	now := time.Unix(1000, 0)
	link, err := signer.URL("https://nudgy.test", 7, "workspace", now)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(link)
	if err != nil {
		t.Fatal(err)
	}
	token := u.Query().Get("token")
	if u.Path != "/workspace/install" || !signer.Verify(token, 7, "workspace", now) {
		t.Fatal("invalid workspace link")
	}
	if signer.Verify(token, 8, "workspace", now) || signer.Verify(token, 7, "github", now) || signer.Verify(token, 7, "workspace", now.Add(20*time.Minute)) {
		t.Fatal("workspace capability escaped its user, purpose or expiry")
	}
}
