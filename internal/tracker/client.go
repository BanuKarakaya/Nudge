package tracker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

type Client struct {
	Provider, ClientID, ClientSecret, RedirectURL string
	HTTPClient                                    *http.Client
}

func (c Client) Kind() string { return c.Provider }
func (c Client) Validate() error {
	u, err := url.Parse(c.RedirectURL)
	if (c.Provider != "jira" && c.Provider != "trello") || strings.TrimSpace(c.ClientID) == "" || strings.TrimSpace(c.ClientSecret) == "" || err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "/"+c.Provider+"/oauth/callback" {
		return ErrArguments
	}
	return nil
}
func (c Client) scopes() string {
	if c.Kind() == "jira" {
		return "read:me read:jira-work read:jira-user write:jira-work offline_access"
	}
	return "read:member:trello read:board:trello write:board:trello offline_access"
}
func (c Client) AuthorizeURL(state, verifier string) string {
	q := url.Values{"client_id": {c.ClientID}, "redirect_uri": {c.RedirectURL}, "response_type": {"code"}, "prompt": {"consent"}, "scope": {c.scopes()}, "state": {state}}
	if c.Kind() == "jira" {
		q.Set("audience", "api.atlassian.com")
	} else {
		h := sha256.Sum256([]byte(verifier))
		q.Set("code_challenge", base64.RawURLEncoding.EncodeToString(h[:]))
		q.Set("code_challenge_method", "S256")
	}
	return "https://auth.atlassian.com/authorize?" + q.Encode()
}

// All destinations are fixed provider origins. Never follow API-supplied URLs
// or redirects with credentials; errors and response bodies stay out of logs.
func (c Client) request(ctx context.Context, endpoint, token string, body any, out any) error {
	method := http.MethodGet
	if body != nil {
		method = http.MethodPost
	}
	return c.requestMethod(ctx, method, endpoint, token, body, out)
}
func (c Client) requestMethod(ctx context.Context, method, endpoint, token string, body any, out any) error {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Fragment != "" || (u.Host != "auth.atlassian.com" && !(c.Kind() == "jira" && u.Host == "api.atlassian.com") && !(c.Kind() == "trello" && u.Host == "api.trello.com")) {
		return ErrArguments
	}
	if method != http.MethodGet && method != http.MethodPost && method != http.MethodPut {
		return ErrArguments
	}
	mutation := method != http.MethodGet && u.Host != "auth.atlassian.com"
	failure := ErrUnavailable
	if mutation {
		failure = ErrOutcomeUnknown
	}
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return ErrArguments
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return ErrArguments
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	client := http.Client{Timeout: 15 * time.Second}
	if c.HTTPClient != nil {
		client = *c.HTTPClient
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		return failure
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 {
		return ErrConnect
	}
	if resp.StatusCode == 403 || resp.StatusCode == 404 {
		return ErrDenied
	}
	if resp.StatusCode == 400 && u.Host == "auth.atlassian.com" {
		return ErrConnect
	}
	if mutation && resp.StatusCode >= 400 && resp.StatusCode < 500 {
		return ErrRejected
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return failure
	}
	if out == nil && resp.StatusCode == http.StatusNoContent {
		return nil
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (4<<20)+1))
	if err != nil || len(raw) > 4<<20 || json.Unmarshal(raw, out) != nil {
		return failure
	}
	return nil
}
func (c Client) token(ctx context.Context, body map[string]string) (Credentials, error) {
	body["client_id"] = c.ClientID
	body["client_secret"] = c.ClientSecret
	var token struct {
		Access  string `json:"access_token"`
		Refresh string `json:"refresh_token"`
		Expires int64  `json:"expires_in"`
		Scope   string `json:"scope"`
	}
	if err := c.request(ctx, "https://auth.atlassian.com/oauth/token", "", body, &token); err != nil {
		return Credentials{}, err
	}
	if token.Access == "" || token.Refresh == "" || token.Expires <= 0 || token.Expires > 366*86400 {
		return Credentials{}, ErrConnect
	}
	if token.Scope != "" {
		for _, scope := range strings.Fields(c.scopes()) {
			if scope != "offline_access" && scope != "read:jira-user" && !strings.HasPrefix(scope, "write:") && !strings.Contains(" "+token.Scope+" ", " "+scope+" ") {
				return Credentials{}, ErrDenied
			}
		}
	}
	return Credentials{Access: token.Access, Refresh: token.Refresh, Expires: time.Now().Add(time.Duration(token.Expires) * time.Second), Scopes: token.Scope}, nil
}
func (c Client) Exchange(ctx context.Context, code, verifier string) (Credentials, error) {
	body := map[string]string{"grant_type": "authorization_code", "code": code, "redirect_uri": c.RedirectURL}
	if c.Kind() == "trello" {
		body["code_verifier"] = verifier
	}
	creds, err := c.token(ctx, body)
	if err == nil && creds.Scopes == "" {
		creds.Scopes = c.scopes()
	}
	return creds, err
}
func (c Client) Refresh(ctx context.Context, token string) (Credentials, error) {
	return c.token(ctx, map[string]string{"grant_type": "refresh_token", "refresh_token": token})
}
func (c Client) Account(ctx context.Context, token string) (Account, error) {
	var result struct {
		ID        string `json:"id"`
		AccountID string `json:"account_id"`
		Name      string `json:"name"`
		Username  string `json:"username"`
	}
	endpoint := "https://api.atlassian.com/me"
	if c.Kind() == "trello" {
		endpoint = "https://api.trello.com/1/members/me?fields=username"
	}
	if err := c.request(ctx, endpoint, token, nil, &result); err != nil {
		return Account{}, err
	}
	account := Account{result.AccountID, result.Name}
	if c.Kind() == "trello" {
		account = Account{result.ID, result.Username}
	}
	if account.ID == "" || account.Name == "" {
		return Account{}, ErrConnect
	}
	return account, nil
}

var decimalID = regexp.MustCompile(`^[1-9][0-9]{0,19}$`)
var cloudID = regexp.MustCompile(`^[a-zA-Z0-9-]{1,100}$`)
var trelloID = regexp.MustCompile(`^[0-9a-f]{24}$`)
var issueKey = regexp.MustCompile(`^(?:[A-Z][A-Z0-9_]*-[1-9][0-9]*|[1-9][0-9]*)$`)

func safeURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return ""
	}
	return raw
}
