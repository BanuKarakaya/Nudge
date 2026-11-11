package slack

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
)

func TestSignInVerifiesSlackToken(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	otherKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	cases := []string{"valid", "signature", "issuer", "audience", "expired", "nonce", "missing-team", "subject", "access-hash", "provider-error", "redirect"}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			claims := map[string]any{"iss": "https://slack.com", "aud": "client", "sub": "U123", "exp": time.Now().Add(time.Minute).Unix(), "iat": time.Now().Unix(), "nonce": "browser-nonce", "https://slack.com/team_id": "T123", "https://slack.com/user_id": "U123"}
			switch name {
			case "issuer":
				claims["iss"] = "https://attacker.test"
			case "audience":
				claims["aud"] = "other-client"
			case "expired":
				claims["exp"] = time.Now().Add(-time.Minute).Unix()
			case "nonce":
				claims["nonce"] = "other-browser"
			case "missing-team":
				delete(claims, "https://slack.com/team_id")
			case "subject":
				claims["sub"] = "other-user"
			case "access-hash":
				claims["at_hash"] = "wrong"
			}
			signingKey := key
			if name == "signature" {
				signingKey = otherKey
			}
			signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: signingKey}, nil)
			if err != nil {
				t.Fatal(err)
			}
			payload, _ := json.Marshal(claims)
			signed, err := signer.Sign(payload)
			if err != nil {
				t.Fatal(err)
			}
			token, err := signed.CompactSerialize()
			if err != nil {
				t.Fatal(err)
			}
			client := OAuthClient{ClientID: "client", ClientSecret: "secret", RedirectURL: "https://nudgy.test/slack/oauth/callback"}
			client.HTTPClient = &http.Client{Transport: codingTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host != "slack.com" {
					t.Fatal("credentials sent outside Slack")
				}
				var body []byte
				status := 200
				switch r.URL.Path {
				case "/api/openid.connect.token":
					if r.Method != "POST" {
						t.Fatal("wrong token method")
					}
					if err := r.ParseForm(); err != nil {
						t.Fatal(err)
					}
					if r.Form.Get("code") != "code" || r.Form.Get("client_secret") != "secret" || r.Form.Get("redirect_uri") != client.RedirectURL || r.Form.Get("grant_type") != "authorization_code" {
						t.Fatal("invalid code exchange")
					}
					body, _ = json.Marshal(map[string]any{"ok": true, "id_token": token, "access_token": "access"})
					if name == "provider-error" {
						body = []byte(`{"ok":false,"error":"DO-NOT-REFLECT-secret"}`)
					}
					if name == "redirect" {
						status = 302
					}
				case "/openid/connect/keys":
					body, _ = json.Marshal(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, Use: "sig", Algorithm: "RS256"}}})
				default:
					t.Fatalf("unexpected endpoint %s", r.URL.Path)
				}
				return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(string(body))), Header: http.Header{"Location": {"https://attacker.test/"}}}, nil
			})}
			identity, err := client.ExchangeSignIn(context.Background(), "code", "browser-nonce")
			if name == "valid" {
				if err != nil || identity.Team != "T123" || identity.User != "U123" {
					t.Fatalf("identity %+v, %v", identity, err)
				}
			} else if err == nil {
				t.Fatal("unverified token accepted")
			} else if strings.Contains(err.Error(), "secret") {
				t.Fatal("provider error leaked")
			}
		})
	}
}

func TestSignInURLDoesNotInstallBot(t *testing.T) {
	c := OAuthClient{ClientID: "client", RedirectURL: "https://nudgy.test/slack/oauth/callback"}
	u, err := url.Parse(c.SignInURL("state", "nonce"))
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if u.Host != "slack.com" || u.Path != "/openid/connect/authorize" || q.Get("scope") != "openid profile" || q.Get("state") != "state" || q.Get("nonce") != "nonce" || q.Get("redirect_uri") != c.RedirectURL || q.Get("response_type") != "code" {
		t.Fatal("wrong sign-in request", u)
	}
}
