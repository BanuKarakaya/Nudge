package slack

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
)

// Sign-in is separate from installation: it never requests bot permissions.
func (c OAuthClient) SignInURL(state, nonce string) string {
	q := url.Values{
		"client_id": {c.ClientID}, "redirect_uri": {c.RedirectURL},
		"response_type": {"code"}, "scope": {"openid profile"},
		"state": {state}, "nonce": {nonce},
	}
	return "https://slack.com/openid/connect/authorize?" + q.Encode()
}

type SignInIdentity struct {
	Team string `json:"https://slack.com/team_id"`
	User string `json:"https://slack.com/user_id"`
}

// ExchangeSignIn validates the signed ID token, including issuer, audience,
// expiration and browser-bound nonce. Neither the ID nor access token is stored.
func (c OAuthClient) ExchangeSignIn(ctx context.Context, code, nonce string) (SignInIdentity, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	client := &http.Client{Timeout: 15 * time.Second}
	if c.HTTPClient != nil {
		*client = *c.HTTPClient
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	form := url.Values{
		"client_id": {c.ClientID}, "client_secret": {c.ClientSecret},
		"redirect_uri": {c.RedirectURL}, "code": {code}, "grant_type": {"authorization_code"},
	}
	req, err := http.NewRequestWithContext(ctx, "POST", "https://slack.com/api/openid.connect.token", strings.NewReader(form.Encode()))
	if err != nil {
		return SignInIdentity{}, errors.New("could not start Slack sign-in exchange")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		return SignInIdentity{}, errors.New("Slack sign-in unavailable")
	}
	defer resp.Body.Close()
	var result struct {
		OK          bool   `json:"ok"`
		IDToken     string `json:"id_token"`
		AccessToken string `json:"access_token"`
	}
	if resp.StatusCode != 200 || json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&result) != nil || !result.OK || result.IDToken == "" {
		return SignInIdentity{}, errors.New("Slack sign-in exchange failed")
	}
	keyContext := oidc.ClientContext(ctx, client)
	keys := oidc.NewRemoteKeySet(keyContext, "https://slack.com/openid/connect/keys")
	verifier := oidc.NewVerifier("https://slack.com", keys, &oidc.Config{ClientID: c.ClientID, SupportedSigningAlgs: []string{"RS256"}})
	token, err := verifier.Verify(keyContext, result.IDToken)
	if err != nil || nonce == "" {
		return SignInIdentity{}, errors.New("Slack identity could not be verified")
	}
	if subtle.ConstantTimeCompare([]byte(token.Nonce), []byte(nonce)) != 1 || (token.AccessTokenHash != "" && token.VerifyAccessToken(result.AccessToken) != nil) {
		return SignInIdentity{}, errors.New("Slack sign-in binding mismatch")
	}
	var identity SignInIdentity
	if token.Claims(&identity) != nil || identity.Team == "" || identity.User == "" || token.Subject != identity.User {
		return SignInIdentity{}, errors.New("Slack workspace identity missing")
	}
	return identity, nil
}
